// MyLab AutoPrint: lightweight local print server (QZ Tray alternative).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"mylabautoprint/internal/config"
	"mylabautoprint/internal/platform"
	"mylabautoprint/internal/printing"
	"mylabautoprint/internal/server"
	"mylabautoprint/internal/update"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "0.0.0-dev"

// App bundles the running components (used by the tray UI).
type App struct {
	Cfg     *config.Store
	Mgr     *printing.Manager
	Upd     *update.Updater
	Srv     *server.Server
	Logger  *log.Logger
	LogPath string
	Ctx     context.Context
	Quit    func()
	onState func(update.State)
}

// SettingsURL returns the local settings page URL.
func (a *App) SettingsURL() string { return fmt.Sprintf("http://127.0.0.1:%d/", a.Srv.Port()) }

func openLog(dataDir string) (*log.Logger, string, io.Closer) {
	dir := filepath.Join(dataDir, "logs")
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "mylabautoprint.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 2<<20 {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return log.New(os.Stderr, "", log.LstdFlags), p, io.NopCloser(nil)
	}
	var w io.Writer = f
	if consoleLog {
		w = io.MultiWriter(f, os.Stderr)
	}
	return log.New(w, "", log.LstdFlags), p, f
}

func main() {
	showVersion := flag.Bool("version", false, "tampilkan versi")
	flag.Parse()
	if *showVersion {
		fmt.Println("MyLab AutoPrint", Version)
		return
	}
	// Keep the heap small: we are an idle background service most of the time.
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(64 << 20)

	dataDir := platform.DataDir()
	cfg, err := config.Load(filepath.Join(dataDir, "config.json"), filepath.Join(platform.ExeDir(), "defaults.json"))
	if err != nil {
		platform.MessageBox("MyLab AutoPrint", "Config tidak bisa dibaca: "+err.Error(), false)
		os.Exit(1)
	}

	release, ok := platform.SingleInstance()
	if !ok {
		// Already running: just open the settings page of the running instance.
		_ = platform.Open(fmt.Sprintf("http://127.0.0.1:%d/", cfg.Get().Port))
		return
	}
	defer release()

	logger, logPath, lf := openLog(dataDir)
	defer lf.Close()
	logger.Printf("MyLab AutoPrint %s mulai (data: %s)", Version, dataDir)

	if c := cfg.Get(); c.Autostart != platform.AutostartEnabled() {
		if err := platform.SetAutostart(c.Autostart); err != nil {
			logger.Printf("autostart: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := &App{Cfg: cfg, Logger: logger, LogPath: logPath, Ctx: ctx}
	app.Mgr = printing.NewManager(printing.NewBackend(), cfg, logger, platform.TempDir(), platform.ExeDir())
	app.Srv = &server.Server{Version: Version, Cfg: cfg, Mgr: app.Mgr, Logger: logger, LogPath: logPath,
		Hooks: server.Hooks{SetAutostart: platform.SetAutostart, OpenLog: func() error { return platform.Open(logPath) }}}
	app.Upd = update.New(cfg, Version, logger, filepath.Join(platform.TempDir(), "update"), update.Hooks{
		Busy: app.Mgr.Busy,
		RunInstaller: func(path string) error {
			if err := platform.RunInstaller(path); err != nil {
				return err
			}
			logger.Printf("installer berjalan, aplikasi keluar untuk diganti")
			go func() { time.Sleep(1500 * time.Millisecond); app.Quit() }()
			return nil
		},
		OnState: func(s update.State) {
			app.Srv.Broadcast(map[string]any{"type": "update", "update": s})
			if app.onState != nil {
				app.onState(s)
			}
		},
	})
	app.Srv.Upd = app.Upd
	if err := app.Srv.Start(); err != nil {
		logger.Printf("GAGAL start server: %v", err)
		platform.MessageBox("MyLab AutoPrint", err.Error()+"\n\nUbah \"port\" di:\n"+cfg.Path(), false)
		os.Exit(2)
	}
	go app.Upd.Loop(ctx)

	runUI(app) // blocks until quit
	cancel()
	app.Srv.Shutdown()
	logger.Printf("MyLab AutoPrint berhenti")
}
