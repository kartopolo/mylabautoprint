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
