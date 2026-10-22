// Package server implements the local HTTP + WebSocket API and serves the
// embedded settings UI.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"mylabautoprint/internal/config"
	"mylabautoprint/internal/printing"
	"mylabautoprint/internal/update"
)

//go:embed web
var webFS embed.FS

// Hooks are platform callbacks.
type Hooks struct {
	SetAutostart func(bool) error
	OpenLog      func() error
}

// Server is the local API server.
type Server struct {
	Version string
	Cfg     *config.Store
	Mgr     *printing.Manager
	Upd     *update.Updater
	Logger  *log.Logger
	LogPath string
	Hooks   Hooks

	started time.Time
	mu      sync.Mutex
	srv     *http.Server
	port    int
	wsMu    sync.Mutex
	wsSubs  map[chan any]struct{}
}

// Start binds 127.0.0.1:<port> and serves in the background.
func (s *Server) Start() error {
	s.started = time.Now()
	s.wsSubs = map[chan any]struct{}{}
	return s.listen(s.Cfg.Get().Port)
}

// Port returns the port currently served.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

func (s *Server) listen(port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("port %d tidak bisa dipakai (mungkin sudah dipakai aplikasi lain): %w", port, err)
	}
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	s.mu.Lock()
	s.srv, s.port = srv, port
	s.mu.Unlock()
	s.Logger.Printf("API aktif di http://127.0.0.1:%d", port)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.Logger.Printf("server berhenti: %v", err)
		}
	}()
	return nil
}

// Rebind moves the server to a new port (after a config change).
func (s *Server) Rebind(port int) error {
	s.mu.Lock()
	old, oldPort := s.srv, s.port
	s.mu.Unlock()
	if port == oldPort {
		return nil
	}
	if old != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = old.Shutdown(ctx)
		cancel()
	}
	if err := s.listen(port); err != nil {
		s.Logger.Printf("pindah port gagal, kembali ke %d: %v", oldPort, err)
		return errors.Join(err, s.listen(oldPort))
	}
	return nil
}

// Shutdown stops the server.
func (s *Server) Shutdown() {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// Broadcast sends an event to all WebSocket clients.
func (s *Server) Broadcast(ev any) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	for ch := range s.wsSubs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /printers", s.handlePrinters)
	mux.HandleFunc("POST /print", s.handlePrint)
	mux.HandleFunc("GET /jobs", s.handleJobs)
	mux.HandleFunc("GET /jobs/{id}", s.handleJob)
	mux.HandleFunc("GET /config", s.handleGetConfig)
	mux.HandleFunc("PUT /config", s.handlePutConfig)
	mux.HandleFunc("POST /config", s.handlePutConfig)
	mux.HandleFunc("POST /test-print", s.handleTestPrint)
	mux.HandleFunc("GET /update", s.handleUpdateState)
	mux.HandleFunc("POST /update/check", s.handleUpdateCheck)
	mux.HandleFunc("POST /update/install", s.handleUpdateInstall)
	mux.HandleFunc("GET /log", s.handleLog)
	mux.HandleFunc("GET /ws", s.handleWS)
	return s.guard(mux)
}

// ---- security middleware ---------------------------------------------------

func (s *Server) selfOrigins() []string {
	p := strconv.Itoa(s.Port())
	return []string{"http://127.0.0.1:" + p, "http://localhost:" + p, "http://[::1]:" + p}
}

func (s *Server) isSelf(origin string) bool {
	for _, o := range s.selfOrigins() {
		if strings.EqualFold(origin, o) {
			return true
		}
	}
	return false
}

// OriginAllowed checks an Origin header against the allowed list. Supports
// "*", exact origins and wildcard subdomains like "https://*.domain.com".
func OriginAllowed(origin string, allowed []string) bool {
	origin = strings.TrimRight(strings.ToLower(origin), "/")
	for _, a := range allowed {
		a = strings.TrimRight(strings.ToLower(strings.TrimSpace(a)), "/")
		switch {
		case a == "*":
			return true
		case a == origin:
			return true
		case strings.Contains(a, "://*."):
			i := strings.Index(a, "://*.")
			scheme, suffix := a[:i+3], a[i+4:] // suffix starts with "."
			if strings.HasPrefix(origin, scheme) && strings.HasSuffix(origin, suffix) {
				return true
			}
		}
	}
	return false
}

func validHost(host string, port int) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		h, p = host, "80"
	}
	if p != strconv.Itoa(port) {
		return false
	}
	return h == "127.0.0.1" || strings.EqualFold(h, "localhost") || h == "::1"
}

type ctxKey int

const ctxSelf ctxKey = 1

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// DNS-rebinding protection: only accept localhost Host headers.
		if !validHost(r.Host, s.Port()) {
			http.Error(w, "host tidak diizinkan", http.StatusForbidden)
			return
		}
		cfg := s.Cfg.Get()
		origin := r.Header.Get("Origin")
		self := origin == "" || s.isSelf(origin)
		if origin != "" && !self && !OriginAllowed(origin, cfg.AllowedOrigins) {
			s.Logger.Printf("ditolak: origin %s tidak ada di allowed_origins (%s %s)", origin, r.Method, r.URL.Path)
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false,
				"error": "origin " + origin + " tidak diizinkan. Tambahkan di Pengaturan > Website yang diizinkan."})
			return
		}
		if origin != "" {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")
			h.Set("Access-Control-Max-Age", "600")
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				h.Set("Access-Control-Allow-Private-Network", "true")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// API key: required for browser requests from other origins when set.
		isUI := r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/config") && !apiPath(r.URL.Path)
		if cfg.APIKey != "" && !self && !isUI {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			}
			if key == "" {
				key = r.URL.Query().Get("apiKey")
			}
			if key != cfg.APIKey {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "API key salah atau tidak ada"})
				return
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxSelf, self))
		next.ServeHTTP(w, r)
	})
}

func apiPath(p string) bool {
	for _, x := range []string{"/status", "/printers", "/print", "/jobs", "/config", "/test-print", "/update", "/log", "/ws"} {
		if p == x || strings.HasPrefix(p, x+"/") {
			return true
		}
	}
	return false
}

func isSelfReq(r *http.Request) bool { v, _ := r.Context().Value(ctxSelf).(bool); return v }

// ---- handlers --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) status() map[string]any {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return map[string]any{
		"ok": true, "app": "MyLab AutoPrint", "version": s.Version,
		"os": runtime.GOOS + "/" + runtime.GOARCH, "backend": s.Mgr.Backend().Name(),
		"port": s.Port(), "uptime_sec": int(time.Since(s.started).Seconds()),
		"busy": s.Mgr.Busy(), "go_heap_mb": float64(ms.HeapAlloc) / (1 << 20),
		"go_sys_mb":   float64(ms.Sys) / (1 << 20),
		"config_path": s.Cfg.Path(), "log_path": s.LogPath, "update": s.Upd.State(),
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.status())
}

func (s *Server) printers() (map[string]any, error) {
	ps, def, err := s.Mgr.Backend().ListPrinters()
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "default": def, "printers": ps}, nil
}

func (s *Server) handlePrinters(w http.ResponseWriter, r *http.Request) {
	res, err := s.printers()
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handlePrint(w http.ResponseWriter, r *http.Request) {
	var req printing.Request
	body := http.MaxBytesReader(w, r.Body, printing.MaxPayload*4/3+1<<20)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "JSON tidak valid: " + err.Error()})
		return
	}
	job, err := s.Mgr.Submit(req, r.Header.Get("Origin"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error(), "job": job})
		return
	}
	if req.Async {
		writeJSON(w, 202, map[string]any{"ok": true, "job": *job})
		return
	}
	res := s.Mgr.Wait(job, 3*time.Minute)
	code := 200
	if res.Status == printing.StatusError {
		code = 500
	}
	writeJSON(w, code, map[string]any{"ok": res.Status == printing.StatusDone, "job": res, "error": res.Error})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "jobs": s.Mgr.Jobs()})
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.Mgr.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, 404, map[string]any{"ok": false, "error": "job tidak ditemukan"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "job": j})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	c := s.Cfg.Get()
	locked := []string{}
	for k := range c.Locked {
		locked = append(locked, k)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "config": c, "locked_keys": locked, "path": s.Cfg.Path()})
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	before := s.Cfg.Get()
	ignored, err := s.Cfg.Patch(b)
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	after := s.Cfg.Get()
	s.Logger.Printf("config diubah dari %s", map[bool]string{true: "pengaturan lokal", false: r.Header.Get("Origin")}[isSelfReq(r)])
	if after.Autostart != before.Autostart && s.Hooks.SetAutostart != nil {
		if err := s.Hooks.SetAutostart(after.Autostart); err != nil {
			s.Logger.Printf("set autostart gagal: %v", err)
		}
	}
	resp := map[string]any{"ok": true, "config": after, "ignored_locked": ignored}
	if after.Port != before.Port {
		resp["new_url"] = fmt.Sprintf("http://127.0.0.1:%d/", after.Port)
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := s.Rebind(after.Port); err != nil {
				s.Logger.Printf("rebind: %v", err)
			}
		}()
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleTestPrint(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Type    string `json:"type"`
		Printer string `json:"printer"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&p)
	req, err := TestRequest(p.Type, s.Version)
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Printer = p.Printer
	job, err := s.Mgr.Submit(req, "test-print")
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	res := s.Mgr.Wait(job, 2*time.Minute)
	writeJSON(w, 200, map[string]any{"ok": res.Status == printing.StatusDone, "job": res, "error": res.Error})
}

func (s *Server) handleUpdateState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "update": s.Upd.State(), "config": s.Cfg.Get().Update})
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	_, _, err := s.Upd.Check(ctx)
	res := map[string]any{"ok": err == nil, "update": s.Upd.State()}
	if err != nil {
		res["error"] = err.Error()
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if !isSelfReq(r) {
		writeJSON(w, 403, map[string]any{"ok": false, "error": "install update hanya dari pengaturan lokal"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := s.Upd.Install(ctx); err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "installer dijalankan, aplikasi akan restart"})
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n <= 0 || n > 2000 {
		n = 200
	}
	b, err := os.ReadFile(s.LogPath)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": true, "lines": []string{}})
		return
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	writeJSON(w, 200, map[string]any{"ok": true, "lines": lines})
}
