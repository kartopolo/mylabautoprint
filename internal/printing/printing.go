// Package printing contains the print job model, the job queue/history and
// the platform specific print backends.
package printing

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Job types.
const (
	TypeRaw   = "raw"
	TypePDF   = "pdf"
	TypeImage = "image"
	TypeText  = "text"
	TypeHTML  = "html"
)

// MaxPayload is the maximum size of a print payload (64 MB).
const MaxPayload = 64 << 20

// Request is a print request as sent by the web app (HTTP or WebSocket).
type Request struct {
	Printer     string `json:"printer,omitempty"`     // printer name, "tcp://ip:9100" or empty for default
	Type        string `json:"type"`                  // raw | pdf | image | text | html
	Data        string `json:"data,omitempty"`        // base64 payload
	Text        string `json:"text,omitempty"`        // plain text payload (alternative to data)
	URL         string `json:"url,omitempty"`         // http(s) URL to fetch payload from
	Copies      int    `json:"copies,omitempty"`      // default from config
	Paper       string `json:"paper,omitempty"`       // A4, A5, A3, Letter, Legal
	Orientation string `json:"orientation,omitempty"` // portrait | landscape
	JobName     string `json:"jobName,omitempty"`
	Scale       string `json:"scale,omitempty"`    // image: fit|width|noscale ; pdf: fit|shrink|noscale
	Pages       string `json:"pages,omitempty"`    // pdf: e.g. "1-3,5"
	TextMode    string `json:"textMode,omitempty"` // text: gdi (default) | raw
	FontSize    int    `json:"fontSize,omitempty"` // text (gdi): points, default 10
	Async       bool   `json:"async,omitempty"`    // HTTP: return immediately with the queued job

	Profile  string   `json:"profile,omitempty"`  // named print profile (see /profiles)
	Color    string   `json:"color,omitempty"`    // "" | color | mono
	Duplex   string   `json:"duplex,omitempty"`   // "" | simplex | long | short
	Tray     string   `json:"tray,omitempty"`     // paper source: bin name or number
	MarginMM *float64 `json:"marginMm,omitempty"` // image/text (gdi): page margin in mm
	Cut      *bool    `json:"cut,omitempty"`      // raw: append ESC/POS paper cut (GS V B 0)
}

// Paper describes a paper size supported by a printer.
type Paper struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	WidthMM  float64 `json:"width_mm"`
	HeightMM float64 `json:"height_mm"`
}

// Bin describes a paper source / tray.
type Bin struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Capabilities of a printer (best effort, from the driver).
type Capabilities struct {
	Printer   string  `json:"printer"`
	Papers    []Paper `json:"papers"`
	Bins      []Bin   `json:"bins"`
	Duplex    bool    `json:"duplex"`
	Color     bool    `json:"color"`
	MaxCopies int     `json:"max_copies"`
}

// Printer describes an installed printer.
type Printer struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Network bool   `json:"network,omitempty"`
	Server  string `json:"server,omitempty"`
}

// Payload is a fully resolved print job given to a backend.
type Payload struct {
	Request
	Printer string
	Bytes   []byte
	TmpDir  string
	AppDir  string
	// Warn adds a non-fatal warning to the job result.
	Warn func(string)
}

// Backend is implemented per platform.
type Backend interface {
	Name() string
	ListPrinters() ([]Printer, string, error)
	Capabilities(printer string) (*Capabilities, error)
	Print(ctx context.Context, p *Payload) error
}

// ErrUnsupported is returned for job types a backend cannot handle.
var ErrUnsupported = errors.New("tipe job tidak didukung")

// ValidType reports whether t is a known job type.
func ValidType(t string) bool {
	switch t {
	case TypeRaw, TypePDF, TypeImage, TypeText, TypeHTML:
		return true
	}
	return false
}

// resolveBytes turns data/text/url into bytes.
func resolveBytes(ctx context.Context, r *Request) ([]byte, error) {
	switch {
	case r.Data != "":
		d := r.Data
		if i := strings.Index(d, ";base64,"); i >= 0 && strings.HasPrefix(d, "data:") {
			d = d[i+8:]
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(d))
		if err != nil {
			if b2, err2 := base64.RawStdEncoding.DecodeString(strings.TrimSpace(d)); err2 == nil {
				return b2, nil
			}
			return nil, fmt.Errorf("data bukan base64 yang valid: %w", err)
		}
		return b, nil
	case r.Text != "":
		return []byte(r.Text), nil
	case r.URL != "":
		if r.Type == TypeHTML {
			return nil, nil // html URLs are rendered directly by the browser engine
		}
		return download(ctx, r.URL)
	}
	return nil, errors.New("tidak ada data: isi salah satu dari data (base64), text, atau url")
}

func download(ctx context.Context, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, errors.New("url harus http:// atau https://")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxPayload+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxPayload {
		return nil, errors.New("file terlalu besar (maks 64 MB)")
	}
	return b, nil
}

// IsNetworkRaw reports whether printer is a tcp://host:port raw target.
func IsNetworkRaw(printer string) bool { return strings.HasPrefix(strings.ToLower(printer), "tcp://") }

// printNetworkRaw sends bytes to a raw TCP printer port (JetDirect / 9100).
func printNetworkRaw(ctx context.Context, target string, data []byte, copies int) error {
	addr := target[len("tcp://"):]
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "9100")
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("tidak bisa konek ke printer %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
	for i := 0; i < copies; i++ {
		if _, err := conn.Write(data); err != nil {
			return fmt.Errorf("kirim ke %s gagal: %w", addr, err)
		}
	}
	return nil
}
