package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"mylabautoprint/internal/printing"
)

type wsIn struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
}

type wsResp struct {
	Type   string `json:"type"`
	ID     string `json:"id,omitempty"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// handleWS: origin + API key are checked by the guard middleware.
//
// Client -> server: {"id":"1","action":"print|printers|status|jobs|ping","params":{...}}
// Server -> client: {"type":"response","id":"1","ok":true,"result":...}
//
//	{"type":"job","job":{...}}       job status events (queued/printing/done/error)
//	{"type":"printers", ...}         on connect and whenever the list changes
//	{"type":"hello", ...}            on connect
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c.SetReadLimit(printing.MaxPayload*4/3 + 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer c.CloseNow()

	out := make(chan any, 64)
	jobs := s.Mgr.Subscribe()
	defer s.Mgr.Unsubscribe(jobs)
	s.wsMu.Lock()
	s.wsSubs[out] = struct{}{}
	s.wsMu.Unlock()
	defer func() { s.wsMu.Lock(); delete(s.wsSubs, out); s.wsMu.Unlock() }()
	origin := r.Header.Get("Origin")

	// writer
	go func() {
		defer cancel()
		var lastPrinters any
		sendPrinters := func() error {
			p, err := s.printers()
			if err != nil {
				return nil
			}
			if reflect.DeepEqual(p, lastPrinters) {
				return nil
			}
			lastPrinters = p
			p["type"] = "printers"
			return s.wsWrite(ctx, c, p)
		}
		_ = s.wsWrite(ctx, c, map[string]any{"type": "hello", "app": "MyLab AutoPrint", "version": s.Version})
		_ = sendPrinters()
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		for {
			var err error
			select {
			case <-ctx.Done():
				return
			case ev := <-jobs:
				err = s.wsWrite(ctx, c, ev)
			case m := <-out:
				err = s.wsWrite(ctx, c, m)
			case <-tick.C:
				err = sendPrinters()
				if err == nil {
					err = c.Ping(ctx)
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		var in wsIn
		if err := wsjson.Read(ctx, c, &in); err != nil {
			return
		}
		resp := wsResp{Type: "response", ID: in.ID, OK: true}
		switch in.Action {
		case "ping":
			resp.Result = "pong"
		case "status":
			resp.Result = s.status()
		case "printers":
			p, err := s.printers()
			if err != nil {
				resp.OK, resp.Error = false, err.Error()
			} else {
				resp.Result = p
			}
		case "jobs":
			resp.Result = s.Mgr.Jobs()
		case "print":
			var req printing.Request
			if err := json.Unmarshal(in.Params, &req); err != nil {
				resp.OK, resp.Error = false, "params tidak valid: "+err.Error()
				break
			}
			job, err := s.Mgr.Submit(req, origin)
			if err != nil {
				resp.OK, resp.Error = false, err.Error()
			}
			if job != nil {
				resp.Result = map[string]any{"job": *job}
			}
		default:
			resp.OK, resp.Error = false, "action tidak dikenal: "+in.Action
		}
		select {
		case out <- resp:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) wsWrite(ctx context.Context, c *websocket.Conn, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c, v)
}
