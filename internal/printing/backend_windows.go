//go:build windows

package printing

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winspool = windows.NewLazySystemDLL("winspool.drv")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	procEnumPrintersW       = winspool.NewProc("EnumPrintersW")
	procGetDefaultPrinterW  = winspool.NewProc("GetDefaultPrinterW")
	procOpenPrinterW        = winspool.NewProc("OpenPrinterW")
	procClosePrinter        = winspool.NewProc("ClosePrinter")
	procStartDocPrinterW    = winspool.NewProc("StartDocPrinterW")
	procEndDocPrinter       = winspool.NewProc("EndDocPrinter")
	procStartPagePrinter    = winspool.NewProc("StartPagePrinter")
	procEndPagePrinter      = winspool.NewProc("EndPagePrinter")
	procWritePrinter        = winspool.NewProc("WritePrinter")
	procDocumentPropertiesW = winspool.NewProc("DocumentPropertiesW")

	procCreateDCW            = gdi32.NewProc("CreateDCW")
	procDeleteDC             = gdi32.NewProc("DeleteDC")
	procStartDocW            = gdi32.NewProc("StartDocW")
	procEndDoc               = gdi32.NewProc("EndDoc")
	procAbortDoc             = gdi32.NewProc("AbortDoc")
	procStartPage            = gdi32.NewProc("StartPage")
	procEndPage              = gdi32.NewProc("EndPage")
	procGetDeviceCaps        = gdi32.NewProc("GetDeviceCaps")
	procSetStretchBltMode    = gdi32.NewProc("SetStretchBltMode")
	procSetBrushOrgEx        = gdi32.NewProc("SetBrushOrgEx")
	procStretchDIBits        = gdi32.NewProc("StretchDIBits")
	procCreateFontW          = gdi32.NewProc("CreateFontW")
	procSelectObject         = gdi32.NewProc("SelectObject")
	procDeleteObject         = gdi32.NewProc("DeleteObject")
	procTextOutW             = gdi32.NewProc("TextOutW")
	procGetTextMetricsW      = gdi32.NewProc("GetTextMetricsW")
	procGetTextExtentPoint32 = gdi32.NewProc("GetTextExtentPoint32W")
)

const (
	printerEnumLocal       = 0x2
	printerEnumConnections = 0x4
	printerAttrNetwork     = 0x10

	dmOrientation = 0x1
	dmPaperSize   = 0x2
	dmOutBuffer   = 2
	dmInBuffer    = 8

	horzRes    = 8
	vertRes    = 10
	logPixelsX = 88
	logPixelsY = 90

	halftone       = 4
	srcCopy        = 0x00CC0020
	createNoWindow = 0x08000000
)

type printerInfo4 struct {
	PrinterName *uint16
	ServerName  *uint16
	Attributes  uint32
}

type docInfo1 struct {
	DocName    *uint16
	OutputFile *uint16
	Datatype   *uint16
}

type docInfo struct {
	Size     int32
	DocName  *uint16
	Output   *uint16
	Datatype *uint16
	Type     uint32
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
	Colors        [4]byte
}

// WinBackend prints through the Windows spooler (winspool + GDI).
type WinBackend struct{}

// NewBackend returns the platform backend.
func NewBackend() Backend { return &WinBackend{} }

// Name implements Backend.
func (WinBackend) Name() string { return "windows" }

func u16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

// ListPrinters implements Backend.
func (WinBackend) ListPrinters() ([]Printer, string, error) {
	def := defaultPrinter()
	flags := uintptr(printerEnumLocal | printerEnumConnections)
	var needed, returned uint32
	procEnumPrintersW.Call(flags, 0, 4, 0, 0, uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)))
	if needed == 0 {
		return []Printer{}, def, nil
	}
	buf := make([]byte, needed)
	r, _, err := procEnumPrintersW.Call(flags, 0, 4, uintptr(unsafe.Pointer(&buf[0])), uintptr(needed),
		uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)))
	if r == 0 {
		return nil, def, fmt.Errorf("EnumPrinters: %v", err)
	}
	infos := unsafe.Slice((*printerInfo4)(unsafe.Pointer(&buf[0])), returned)
	out := make([]Printer, 0, returned)
	for _, in := range infos {
		p := Printer{Name: windows.UTF16PtrToString(in.PrinterName)}
		if in.ServerName != nil {
			p.Server = windows.UTF16PtrToString(in.ServerName)
		}
		p.Network = in.Attributes&printerAttrNetwork != 0 || p.Server != ""
		p.Default = p.Name == def
		out = append(out, p)
	}
	return out, def, nil
}

func defaultPrinter() string {
	var n uint32
	procGetDefaultPrinterW.Call(0, uintptr(unsafe.Pointer(&n)))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n)
	r, _, _ := procGetDefaultPrinterW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// Print implements Backend.
func (b WinBackend) Print(ctx context.Context, p *Payload) error {
	switch p.Type {
	case TypeRaw:
		return printRaw(p.Printer, p.JobName, "RAW", p.Bytes, p.Copies)
	case TypeText:
		if p.TextMode == "raw" {
			return printRaw(p.Printer, p.JobName, "RAW", p.Bytes, p.Copies)
		}
		return printTextGDI(p)
	case TypeImage:
		return printImageGDI(p)
	case TypePDF:
		return printPDF(ctx, p, p.Bytes)
	case TypeHTML:
		return printHTML(ctx, p)
	}
	return ErrUnsupported
}

func openPrinter(name string) (windows.Handle, error) {
	var h windows.Handle
	r, _, err := procOpenPrinterW.Call(uintptr(unsafe.Pointer(u16(name))), uintptr(unsafe.Pointer(&h)), 0)
	if r == 0 {
		return 0, fmt.Errorf("printer %q tidak bisa dibuka: %v", name, err)
	}
	return h, nil
}

func printRaw(printer, jobName, datatype string, data []byte, copies int) error {
	h, err := openPrinter(printer)
	if err != nil {
		return err
	}
	defer procClosePrinter.Call(uintptr(h))
	for c := 0; c < copies; c++ {
		di := docInfo1{DocName: u16(jobName), Datatype: u16(datatype)}
		r, _, err := procStartDocPrinterW.Call(uintptr(h), 1, uintptr(unsafe.Pointer(&di)))
		if r == 0 {
			return fmt.Errorf("StartDocPrinter: %v", err)
		}
		procStartPagePrinter.Call(uintptr(h))
		off := 0
		for off < len(data) {
			var written uint32
			chunk := data[off:]
			if len(chunk) > 1<<20 {
				chunk = chunk[:1<<20]
			}
			r, _, err := procWritePrinter.Call(uintptr(h), uintptr(unsafe.Pointer(&chunk[0])), uintptr(len(chunk)), uintptr(unsafe.Pointer(&written)))
			if r == 0 || written == 0 {
				procEndPagePrinter.Call(uintptr(h))
				procEndDocPrinter.Call(uintptr(h))
				return fmt.Errorf("WritePrinter: %v", err)
			}
			off += int(written)
		}
		procEndPagePrinter.Call(uintptr(h))
		procEndDocPrinter.Call(uintptr(h))
	}
	return nil
}

var paperSizes = map[string]int16{"letter": 1, "legal": 5, "a3": 8, "a4": 9, "a5": 11, "b5": 13, "f4": 14, "folio": 14}

// devMode builds a DEVMODE for the printer with orientation / paper applied.
func devMode(printer, orientation, paper string) []byte {
	h, err := openPrinter(printer)
	if err != nil {
		return nil
	}
	defer procClosePrinter.Call(uintptr(h))
	name := u16(printer)
	n, _, _ := procDocumentPropertiesW.Call(0, uintptr(h), uintptr(unsafe.Pointer(name)), 0, 0, 0)
	if int32(n) <= 0 {
		return nil
	}
	dm := make([]byte, n)
	r, _, _ := procDocumentPropertiesW.Call(0, uintptr(h), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&dm[0])), 0, dmOutBuffer)
	if int32(r) < 0 {
		return nil
	}
	fields := binary.LittleEndian.Uint32(dm[72:])
	if orientation == "landscape" {
		binary.LittleEndian.PutUint16(dm[76:], 2)
		fields |= dmOrientation
	} else if orientation == "portrait" {
		binary.LittleEndian.PutUint16(dm[76:], 1)
		fields |= dmOrientation
	}
	if ps, ok := paperSizes[strings.ToLower(paper)]; ok {
		binary.LittleEndian.PutUint16(dm[78:], uint16(ps))
		fields |= dmPaperSize
	}
	binary.LittleEndian.PutUint32(dm[72:], fields)
	out := make([]byte, n)
	r, _, _ = procDocumentPropertiesW.Call(0, uintptr(h), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&out[0])),
		uintptr(unsafe.Pointer(&dm[0])), dmInBuffer|dmOutBuffer)
	if int32(r) < 0 {
		return dm
	}
	return out
}

type gdiDoc struct {
	hdc        uintptr
	w, h       int
	dpiX, dpiY int
}

func startGDIDoc(p *Payload) (*gdiDoc, error) {
	dm := devMode(p.Printer, p.Orientation, p.Paper)
	var dmPtr uintptr
	if len(dm) > 0 {
		dmPtr = uintptr(unsafe.Pointer(&dm[0]))
	}
	hdc, _, err := procCreateDCW.Call(uintptr(unsafe.Pointer(u16("WINSPOOL"))), uintptr(unsafe.Pointer(u16(p.Printer))), 0, dmPtr)
	if hdc == 0 {
		return nil, fmt.Errorf("CreateDC untuk printer %q gagal: %v", p.Printer, err)
	}
	di := docInfo{DocName: u16(p.JobName)}
	di.Size = int32(unsafe.Sizeof(di))
	r, _, err := procStartDocW.Call(hdc, uintptr(unsafe.Pointer(&di)))
	if int32(r) <= 0 {
		procDeleteDC.Call(hdc)
		return nil, fmt.Errorf("StartDoc gagal: %v", err)
	}
	cap := func(i uintptr) int { v, _, _ := procGetDeviceCaps.Call(hdc, i); return int(int32(v)) }
	return &gdiDoc{hdc: hdc, w: cap(horzRes), h: cap(vertRes), dpiX: cap(logPixelsX), dpiY: cap(logPixelsY)}, nil
}

func (d *gdiDoc) end(failed bool) {
	if failed {
		procAbortDoc.Call(d.hdc)
	} else {
		procEndDoc.Call(d.hdc)
	}
	procDeleteDC.Call(d.hdc)
}

func printImageGDI(p *Payload) error {
	iw, ih, pix, err := decodeImageBGRA(p.Bytes)
	if err != nil {
		return err
	}
	d, err := startGDIDoc(p)
	if err != nil {
		return err
	}
	bi := bitmapInfoHeader{Width: int32(iw), Height: -int32(ih), Planes: 1, BitCount: 32}
	bi.Size = 40
	x, y, w, h := fitRect(iw, ih, d.w, d.h, d.dpiX, d.dpiY, p.Scale)
	for c := 0; c < p.Copies; c++ {
		procStartPage.Call(d.hdc)
		procSetStretchBltMode.Call(d.hdc, halftone)
		procSetBrushOrgEx.Call(d.hdc, 0, 0, 0)
		r, _, e := procStretchDIBits.Call(d.hdc, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, uintptr(iw), uintptr(ih),
			uintptr(unsafe.Pointer(&pix[0])), uintptr(unsafe.Pointer(&bi)), 0, srcCopy)
		procEndPage.Call(d.hdc)
		if r == 0 {
			d.end(true)
			return fmt.Errorf("StretchDIBits gagal: %v", e)
		}
	}
	d.end(false)
	return nil
}

func printTextGDI(p *Payload) error {
	d, err := startGDIDoc(p)
	if err != nil {
		return err
	}
	size := p.FontSize
	if size <= 0 {
		size = 10
	}
	height := -(size * d.dpiY / 72)
	font, _, _ := procCreateFontW.Call(uintptr(int32(height)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0x31,
		uintptr(unsafe.Pointer(u16("Consolas"))))
	if font != 0 {
		old, _, _ := procSelectObject.Call(d.hdc, font)
		defer func() { procSelectObject.Call(d.hdc, old); procDeleteObject.Call(font) }()
	}
	var tm [16]int32
	procGetTextMetricsW.Call(d.hdc, uintptr(unsafe.Pointer(&tm[0])))
	lineH := int(tm[0] + tm[4])
	if lineH <= 0 {
		lineH = size * d.dpiY / 72
	}
	var sz [2]int32
	procGetTextExtentPoint32.Call(d.hdc, uintptr(unsafe.Pointer(u16("M"))), 1, uintptr(unsafe.Pointer(&sz[0])))
	maxChars := 80
	if sz[0] > 0 {
		maxChars = d.w / int(sz[0])
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(p.Bytes), "\r\n", "\n"), "\t", "    ")
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		r := []rune(l)
		for len(r) > maxChars && maxChars > 0 {
			lines = append(lines, string(r[:maxChars]))
			r = r[maxChars:]
		}
		lines = append(lines, string(r))
	}
	perPage := d.h / lineH
	if perPage < 1 {
		perPage = 1
	}
	for c := 0; c < p.Copies; c++ {
		for start := 0; start < len(lines); start += perPage {
			procStartPage.Call(d.hdc)
			for i := start; i < start+perPage && i < len(lines); i++ {
				s, _ := windows.UTF16FromString(lines[i])
				if len(s) > 1 {
					procTextOutW.Call(d.hdc, 0, uintptr((i-start)*lineH), uintptr(unsafe.Pointer(&s[0])), uintptr(len(s)-1))
				}
			}
			procEndPage.Call(d.hdc)
		}
	}
	d.end(false)
	return nil
}

func hiddenCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}

func printPDF(ctx context.Context, p *Payload, data []byte) error {
	if len(data) < 4 || string(data[:4]) != "%PDF" {
		return errors.New("data bukan file PDF")
	}
	sumatra := filepath.Join(p.AppDir, "SumatraPDF.exe")
	if _, err := os.Stat(sumatra); err != nil {
		return fmt.Errorf("SumatraPDF.exe tidak ditemukan di %s (dibutuhkan untuk print PDF)", p.AppDir)
	}
	f, err := os.CreateTemp(p.TmpDir, "job-*.pdf")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	f.Close()

	settings := []string{fmt.Sprintf("%dx", p.Copies)}
	switch p.Scale {
	case "noscale", "shrink", "fit":
		settings = append(settings, p.Scale)
	default:
		settings = append(settings, "fit")
	}
	if p.Orientation == "landscape" || p.Orientation == "portrait" {
		settings = append(settings, p.Orientation)
	}
	if p.Paper != "" {
		settings = append(settings, "paper="+p.Paper)
	}
	if p.Pages != "" {
		settings = append(settings, p.Pages)
	}
	appData := filepath.Join(p.TmpDir, "sumatra")
	_ = os.MkdirAll(appData, 0o755)
	cmd := hiddenCmd(ctx, sumatra, "-print-to", p.Printer, "-print-settings", strings.Join(settings, ","),
		"-silent", "-exit-when-done", "-appdata", appData, tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("SumatraPDF gagal: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// findBrowser locates Edge or Chrome for best-effort HTML -> PDF rendering.
func findBrowser() string {
	cands := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// printHTML renders HTML (or an http(s) URL) to PDF with headless Edge/Chrome
// and prints the PDF with SumatraPDF. Best effort.
func printHTML(ctx context.Context, p *Payload) error {
	browser := findBrowser()
	if browser == "" {
		return fmt.Errorf("html: %w (Edge/Chrome tidak ditemukan)", ErrUnsupported)
	}
	work, err := os.MkdirTemp(p.TmpDir, "html-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	target := p.URL
	if len(p.Bytes) > 0 {
		src := filepath.Join(work, "page.html")
		if err := os.WriteFile(src, p.Bytes, 0o600); err != nil {
			return err
		}
		target = "file:///" + filepath.ToSlash(src)
	}
	out := filepath.Join(work, "out.pdf")
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := hiddenCmd(cctx, browser, "--headless", "--disable-gpu", "--no-first-run", "--no-pdf-header-footer",
		"--user-data-dir="+filepath.Join(work, "profile"), "--print-to-pdf="+out, target)
	if b, err := cmd.CombinedOutput(); err != nil {
		if _, e2 := os.Stat(out); e2 != nil {
			return fmt.Errorf("render html gagal: %v %s", err, strings.TrimSpace(string(b)))
		}
	}
	pdf, err := os.ReadFile(out)
	if err != nil {
		return fmt.Errorf("render html gagal: %v", err)
	}
	return printPDF(ctx, p, pdf)
}
