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
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Printer  string     `json:"printer"`
	JobName  string     `json:"jobName"`
	Copies   int        `json:"copies"`
	Size     int        `json:"size"`
	Origin   string     `json:"origin,omitempty"`
	Status   string     `json:"status"`
	Error    string     `json:"error,omitempty"`
	Created  time.Time  `json:"created"`
	Started  *time.Time `json:"started,omitempty"`
	Finished *time.Time `json:"finished,omitempty"`

	req  Request
	done chan struct{}
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
	j := &Job{ID: newID(), Type: req.Type, Printer: printer, JobName: req.JobName, Copies: req.Copies,
		Origin: origin, Status: StatusQueued, Created: time.Now(), req: req, done: make(chan struct{})}

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
		p := &Payload{Request: j.req, Printer: j.Printer, Bytes: data, TmpDir: m.tmpDir, AppDir: m.appDir}
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
