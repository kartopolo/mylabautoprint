package printing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"mylabautoprint/internal/config"
)

// Job statuses.
const (
	StatusQueued   = "queued"
	StatusPrinting = "printing"
	StatusDone     = "done"
	StatusError    = "error"
)

// Job is a print job and its state (as kept in history).
type Job struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Printer  string      `json:"printer"`
	JobName  string      `json:"jobName"`
	Copies   int         `json:"copies"`
	Size     int         `json:"size"`
	Origin   string      `json:"origin,omitempty"`
	Profile  string      `json:"profile,omitempty"`
	Settings JobSettings `json:"settings"`
	Warnings []string    `json:"warnings,omitempty"`
	Status   string      `json:"status"`
	Error    string      `json:"error,omitempty"`
	Created  time.Time   `json:"created"`
	Started  *time.Time  `json:"started,omitempty"`
	Finished *time.Time  `json:"finished,omitempty"`

	req  Request
	done chan struct{}
}

// JobSettings are the resolved print settings of a job.
type JobSettings struct {
	Paper       string   `json:"paper,omitempty"`
	Orientation string   `json:"orientation,omitempty"`
	Scale       string   `json:"scale,omitempty"`
	Color       string   `json:"color,omitempty"`
	Duplex      string   `json:"duplex,omitempty"`
	Tray        string   `json:"tray,omitempty"`
	MarginMM    *float64 `json:"margin_mm,omitempty"`
	Cut         bool     `json:"cut,omitempty"`
	TextMode    string   `json:"text_mode,omitempty"`
	FontSize    int      `json:"font_size,omitempty"`
}

// applyProfile fills request fields that were not set explicitly.
func applyProfile(r *Request, p config.Profile) {
	str := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	str(&r.Printer, p.Printer)
	str(&r.Paper, p.Paper)
	str(&r.Orientation, p.Orientation)
	str(&r.Scale, p.Scale)
	str(&r.Color, p.Color)
	str(&r.Duplex, p.Duplex)
	str(&r.Tray, p.Tray)
	str(&r.TextMode, p.TextMode)
	if r.Copies <= 0 && p.Copies > 0 {
		r.Copies = p.Copies
	}
	if r.FontSize <= 0 && p.FontSize > 0 {
		r.FontSize = p.FontSize
	}
	if r.MarginMM == nil && p.MarginMM != nil {
		m := *p.MarginMM
		r.MarginMM = &m
	}
	if r.Cut == nil && p.Cut != nil {
		c := *p.Cut
		r.Cut = &c
	}
}

func normalize(r *Request) {
	r.Orientation = strings.ToLower(strings.TrimSpace(r.Orientation))
	r.Color = strings.ToLower(strings.TrimSpace(r.Color))
	switch r.Color {
	case "monochrome", "grayscale", "bw":
		r.Color = "mono"
	case "colour":
		r.Color = "color"
	}
	r.Duplex = strings.ToLower(strings.TrimSpace(r.Duplex))
	switch r.Duplex {
	case "duplex", "longedge", "long-edge", "vertical":
		r.Duplex = "long"
	case "shortedge", "short-edge", "horizontal":
		r.Duplex = "short"
	case "none", "off":
		r.Duplex = "simplex"
	}
}

// Event is pushed to subscribers on every job state change.
type Event struct {
	Type string `json:"type"` // "job"
	Job  Job    `json:"job"`
}

// Manager owns the per-printer queues, job history and event fan-out.
type Manager struct {
	backend Backend
	cfg     *config.Store
	logger  *log.Logger
	tmpDir  string
	appDir  string

	mu      sync.Mutex
	history []*Job
	byID    map[string]*Job
	queues  map[string]chan *Job
	subs    map[chan Event]struct{}
	active  int
}

// NewManager creates a job manager.
func NewManager(b Backend, cfg *config.Store, logger *log.Logger, tmpDir, appDir string) *Manager {
	return &Manager{backend: b, cfg: cfg, logger: logger, tmpDir: tmpDir, appDir: appDir,
		byID: map[string]*Job{}, queues: map[string]chan *Job{}, subs: map[chan Event]struct{}{}}
}

// Backend returns the print backend.
func (m *Manager) Backend() Backend { return m.backend }

// Busy reports whether jobs are queued or printing.
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active > 0
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return time.Now().Format("150405") + "-" + hex.EncodeToString(b)
}

// ResolvePrinter picks the printer for a job type.
func (m *Manager) ResolvePrinter(typ, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	c := m.cfg.Get()
	var p string
	switch typ {
	case TypeRaw:
		p = c.Printers.Raw
	case TypePDF:
		p = c.Printers.PDF
	case TypeImage:
		p = c.Printers.Image
	case TypeText:
		p = c.Printers.Text
	case TypeHTML:
		p = c.Printers.HTML
	}
	if p == "" {
		p = c.Printers.Default
	}
	if p != "" {
		return p, nil
	}
	_, def, err := m.backend.ListPrinters()
	if err != nil {
		return "", err
	}
	if def == "" {
		return "", errors.New("tidak ada printer default; pilih printer di Pengaturan")
	}
	return def, nil
}

// Submit validates and queues a request.
func (m *Manager) Submit(req Request, origin string) (*Job, error) {
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	if req.Type == "" {
		req.Type = TypeRaw
	}
	if !ValidType(req.Type) {
		return nil, fmt.Errorf("type tidak dikenal: %q (pakai raw|pdf|image|text|html)", req.Type)
	}
	c := m.cfg.Get()
	var warnings []string
	if req.Profile != "" {
		if p, ok := c.Profiles[req.Profile]; ok {
			applyProfile(&req, p)
		} else {
			warnings = append(warnings, fmt.Sprintf("profil %q tidak ditemukan; memakai printer & pengaturan default", req.Profile))
		}
	}
	normalize(&req)
	if req.Copies <= 0 {
		req.Copies = c.Defaults.Copies
	}
	if req.Copies > 99 {
		req.Copies = 99
	}
	if req.Orientation == "" {
		req.Orientation = c.Defaults.Orientation
	}
	if req.Paper == "" {
		req.Paper = c.Defaults.Paper
	}
	if req.Scale == "" && req.Type == TypePDF {
		req.Scale = c.Defaults.PDFScale
	}
	if req.JobName == "" {
		req.JobName = "MyLab AutoPrint " + req.Type
	}
	if req.Data == "" && req.Text == "" && req.URL == "" {
		return nil, errors.New("tidak ada data: isi salah satu dari data (base64), text, atau url")
	}
	printer, err := m.ResolvePrinter(req.Type, req.Printer)
	if err != nil {
		return nil, err
	}
	if IsNetworkRaw(printer) && req.Type != TypeRaw && req.Type != TypeText {
		return nil, errors.New("printer tcp:// hanya untuk type raw atau text")
	}
	if req.Cut != nil && *req.Cut && req.Type != TypeRaw {
		warnings = append(warnings, "opsi cut hanya berlaku untuk type raw (diabaikan)")
	}
	j := &Job{ID: newID(), Type: req.Type, Printer: printer, JobName: req.JobName, Copies: req.Copies,
		Origin: origin, Status: StatusQueued, Created: time.Now(), req: req, done: make(chan struct{}),
		Profile: req.Profile, Warnings: warnings,
		Settings: JobSettings{Paper: req.Paper, Orientation: req.Orientation, Scale: req.Scale, Color: req.Color,
			Duplex: req.Duplex, Tray: req.Tray, MarginMM: req.MarginMM, Cut: req.Cut != nil && *req.Cut && req.Type == TypeRaw,
			TextMode: req.TextMode, FontSize: req.FontSize}}
	for _, w := range warnings {
		m.logger.Printf("job %s: PERINGATAN: %s", j.ID, w)
	}

	m.mu.Lock()
	m.history = append(m.history, j)
	m.byID[j.ID] = j
	if max := c.HistorySize; len(m.history) > max {
		for _, old := range m.history[:len(m.history)-max] {
			delete(m.byID, old.ID)
		}
		m.history = append([]*Job(nil), m.history[len(m.history)-max:]...)
	}
	m.active++
	q, ok := m.queues[printer]
	if !ok {
		q = make(chan *Job, 256)
		m.queues[printer] = q
		go m.worker(q)
	}
	m.mu.Unlock()

	m.emit(j)
	select {
	case q <- j:
	default:
		m.finish(j, errors.New("antrian printer penuh"))
		return j, errors.New("antrian printer penuh")
	}
	return j, nil
}

func (m *Manager) worker(q chan *Job) {
	for j := range q {
		m.run(j)
	}
}

func (m *Manager) run(j *Job) {
	now := time.Now()
	m.mu.Lock()
	j.Status = StatusPrinting
	j.Started = &now
	m.mu.Unlock()
	m.emit(j)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		data, err := resolveBytes(ctx, &j.req)
		if err != nil {
			return err
		}
		m.mu.Lock()
		j.Size = len(data)
		m.mu.Unlock()
		if j.req.Type == TypeRaw && j.req.Cut != nil && *j.req.Cut {
			data = append(append([]byte(nil), data...), 0x0a, 0x0a, 0x0a, 0x1d, 0x56, 0x42, 0x00) // feed + GS V B 0
		}
		p := &Payload{Request: j.req, Printer: j.Printer, Bytes: data, TmpDir: m.tmpDir, AppDir: m.appDir,
			Warn: func(w string) {
				m.mu.Lock()
				j.Warnings = append(j.Warnings, w)
				m.mu.Unlock()
				m.logger.Printf("job %s: PERINGATAN: %s", j.ID, w)
			}}
		if IsNetworkRaw(j.Printer) {
			return printNetworkRaw(ctx, j.Printer, data, j.Copies)
		}
		return m.backend.Print(ctx, p)
	}()
	m.finish(j, err)
	if j.Size > 1<<20 || j.Type == TypeImage || j.Type == TypeHTML {
		// Return large payload buffers to the OS right away to keep idle RAM low.
		debug.FreeOSMemory()
	}
}

func (m *Manager) finish(j *Job, err error) {
	now := time.Now()
	m.mu.Lock()
	j.Finished = &now
	if err != nil {
		j.Status = StatusError
		j.Error = err.Error()
	} else {
		j.Status = StatusDone
	}
	m.active--
	m.mu.Unlock()
	if err != nil {
		m.logger.Printf("job %s (%s -> %s) GAGAL: %v", j.ID, j.Type, j.Printer, err)
	} else {
		m.logger.Printf("job %s (%s -> %s, %d bytes, %dx) selesai", j.ID, j.Type, j.Printer, j.Size, j.Copies)
	}
	m.emit(j)
	close(j.done)
}

// Wait blocks until the job is finished or the timeout elapses.
func (m *Manager) Wait(j *Job, timeout time.Duration) Job {
	select {
	case <-j.done:
	case <-time.After(timeout):
	}
	return m.snapshot(j)
}

func (m *Manager) snapshot(j *Job) Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *j
	c.done = nil
	c.Warnings = append([]string(nil), j.Warnings...)
	return c
}

// Get returns a job by id.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	j, ok := m.byID[id]
	m.mu.Unlock()
	if !ok {
		return Job{}, false
	}
	return m.snapshot(j), true
}

// Jobs returns the history, newest first.
func (m *Manager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.history))
	for i := len(m.history) - 1; i >= 0; i-- {
		c := *m.history[i]
		c.done = nil
		out = append(out, c)
	}
	return out
}

// Subscribe returns a channel receiving job events.
func (m *Manager) Subscribe() chan Event {
	ch := make(chan Event, 64)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber.
func (m *Manager) Unsubscribe(ch chan Event) {
	m.mu.Lock()
	delete(m.subs, ch)
	m.mu.Unlock()
}

func (m *Manager) emit(j *Job) {
	ev := Event{Type: "job", Job: m.snapshot(j)}
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs {
		select {
		case ch <- ev:
		default: // slow subscriber: drop event
		}
	}
}
