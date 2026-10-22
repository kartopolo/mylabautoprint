//go:build !windows

package printing

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// StubBackend is used on non-Windows platforms for development and testing.
// It does not print; it validates the payload and writes it to OutDir.
type StubBackend struct{ OutDir string }

// NewBackend returns the platform backend.
func NewBackend() Backend {
	dir := os.Getenv("MYLABAUTOPRINT_STUB_OUT")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "mylabautoprint-stub-out")
	}
	_ = os.MkdirAll(dir, 0o755)
	return &StubBackend{OutDir: dir}
}

// Name implements Backend.
func (s *StubBackend) Name() string { return "stub (" + s.OutDir + ")" }

// ListPrinters implements Backend.
func (s *StubBackend) ListPrinters() ([]Printer, string, error) {
	return []Printer{
		{Name: "Stub Thermal 80mm", Default: true},
		{Name: "Stub Laser A4"},
		{Name: `\\server\Stub Shared`, Network: true, Server: `\\server`},
	}, "Stub Thermal 80mm", nil
}

// Capabilities implements Backend (fake data for testing).
func (s *StubBackend) Capabilities(printer string) (*Capabilities, error) {
	switch printer {
	case "Stub Thermal 80mm":
		return &Capabilities{Printer: printer, Papers: []Paper{{ID: 256, Name: "80mm x 297mm", WidthMM: 80, HeightMM: 297}},
			Bins: []Bin{{ID: 7, Name: "Automatic"}}, MaxCopies: 99}, nil
	case "Stub Laser A4", `\\server\Stub Shared`:
		return &Capabilities{Printer: printer, Papers: []Paper{{ID: 9, Name: "A4", WidthMM: 210, HeightMM: 297},
			{ID: 11, Name: "A5", WidthMM: 148, HeightMM: 210}, {ID: 1, Name: "Letter", WidthMM: 215.9, HeightMM: 279.4}},
			Bins:   []Bin{{ID: 7, Name: "Automatic"}, {ID: 1, Name: "Tray 1"}, {ID: 2, Name: "Tray 2"}, {ID: 4, Name: "Manual Feed"}},
			Duplex: true, Color: true, MaxCopies: 999}, nil
	}
	return nil, fmt.Errorf("printer %q tidak ditemukan", printer)
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Print implements Backend.
func (s *StubBackend) Print(ctx context.Context, p *Payload) error {
	known := false
	ps, _, _ := s.ListPrinters()
	for _, x := range ps {
		if x.Name == p.Printer {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("printer %q tidak ditemukan", p.Printer)
	}
	ext := ".bin"
	switch p.Type {
	case TypePDF:
		if !bytes.HasPrefix(p.Bytes, []byte("%PDF")) {
			return fmt.Errorf("data bukan PDF")
		}
		ext = ".pdf"
	case TypeImage:
		w, h, _, err := decodeImageBGRA(p.Bytes)
		if err != nil {
			return err
		}
		ext = fmt.Sprintf("_%dx%d.img", w, h)
	case TypeText:
		ext = ".txt"
	case TypeHTML:
		return fmt.Errorf("html: %w di stub backend", ErrUnsupported)
	}
	time.Sleep(150 * time.Millisecond) // simulate spooling
	name := fmt.Sprintf("%d_%s_%s_x%d%s", time.Now().UnixNano(), p.Type,
		strings.Trim(unsafeName.ReplaceAllString(p.Printer, "_"), "_"), p.Copies, ext)
	return os.WriteFile(filepath.Join(s.OutDir, name), p.Bytes, 0o644)
}
