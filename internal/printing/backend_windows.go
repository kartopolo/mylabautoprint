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
	"regexp"
	"strconv"
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
	procDeviceCapabilitiesW = winspool.NewProc("DeviceCapabilitiesW")

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

	dmOrientation   = 0x1
	dmPaperSize     = 0x2
	dmPaperLength   = 0x4
	dmPaperWidth    = 0x8
	dmDefaultSource = 0x200
	dmColor         = 0x800
	dmDuplex        = 0x1000

	dcPapers      = 2
	dcPaperSize   = 3
	dcBins        = 6
	dcDuplex      = 7
	dcBinNames    = 12
	dcPaperNames  = 16
	dcCopies      = 18
	dcColorDevice = 32
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

var paperSizes = map[string]int16{"letter": 1, "tabloid": 3, "legal": 5, "statement": 6, "executive": 7,
	"a3": 8, "a4": 9, "a5": 11, "b4": 12, "b5": 13, "f4": 14, "folio": 14, "a6": 70}

var binNames = map[string]int16{"upper": 1, "onlyone": 1, "lower": 2, "middle": 3, "manual": 4, "envelope": 5,
	"auto": 7, "automatic": 7, "tractor": 8, "largecapacity": 11, "cassette": 14}

var customPaper = regexp.MustCompile(`^(\d+(?:[.,]\d+)?)\s*(?:[x×*]\s*(\d+(?:[.,]\d+)?))?\s*mm$`)

func parseMM(s string) float64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	return f
}

func devCaps(printer string, cap uintptr, out unsafe.Pointer) int {
	name := u16(printer)
	r, _, _ := procDeviceCapabilitiesW.Call(uintptr(unsafe.Pointer(name)), 0, cap, uintptr(out), 0)
	return int(int32(r))
}

// Capabilities implements Backend using DeviceCapabilitiesW.
func (WinBackend) Capabilities(printer string) (*Capabilities, error) {
	c := &Capabilities{Printer: printer, Papers: []Paper{}, Bins: []Bin{}}
	n := devCaps(printer, dcPapers, nil)
	if n < 0 {
		return nil, fmt.Errorf("printer %q tidak ditemukan / driver tidak menjawab", printer)
	}
	if n > 0 && n < 2000 {
		ids := make([]uint16, n)
		devCaps(printer, dcPapers, unsafe.Pointer(&ids[0]))
		names := make([]uint16, n*64)
		devCaps(printer, dcPaperNames, unsafe.Pointer(&names[0]))
		sizes := make([]int32, n*2)
		devCaps(printer, dcPaperSize, unsafe.Pointer(&sizes[0]))
		for i := 0; i < n; i++ {
			c.Papers = append(c.Papers, Paper{ID: int(ids[i]), Name: windows.UTF16ToString(names[i*64 : i*64+64]),
				WidthMM: float64(sizes[i*2]) / 10, HeightMM: float64(sizes[i*2+1]) / 10})
		}
	}
	if n := devCaps(printer, dcBins, nil); n > 0 && n < 500 {
		ids := make([]uint16, n)
		devCaps(printer, dcBins, unsafe.Pointer(&ids[0]))
		names := make([]uint16, n*24)
		devCaps(printer, dcBinNames, unsafe.Pointer(&names[0]))
		for i := 0; i < n; i++ {
			c.Bins = append(c.Bins, Bin{ID: int(ids[i]), Name: windows.UTF16ToString(names[i*24 : i*24+24])})
		}
	}
	c.Duplex = devCaps(printer, dcDuplex, nil) == 1
	c.Color = devCaps(printer, dcColorDevice, nil) == 1
	if v := devCaps(printer, dcCopies, nil); v > 0 {
		c.MaxCopies = v
	}
	return c, nil
}

// devMode builds a DEVMODE for the printer with the job settings applied.
func devMode(p *Payload) []byte {
	printer := p.Printer
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
	put16 := func(off int, v int16) { binary.LittleEndian.PutUint16(dm[off:], uint16(v)) }
	fields := binary.LittleEndian.Uint32(dm[72:])
	switch p.Orientation {
	case "landscape":
		put16(76, 2)
		fields |= dmOrientation
	case "portrait":
		put16(76, 1)
		fields |= dmOrientation
	}
	if paper := strings.ToLower(strings.TrimSpace(p.Paper)); paper != "" {
		if ps, ok := paperSizes[paper]; ok {
			put16(78, ps)
			fields |= dmPaperSize
		} else if id, ok := driverPaper(printer, paper); ok {
			put16(78, int16(id))
			fields |= dmPaperSize
		} else if m := customPaper.FindStringSubmatch(paper); m != nil {
			w, ht := parseMM(m[1]), 297.0
			if m[2] != "" {
				ht = parseMM(m[2])
			}
			if id, ok := driverPaperBySize(printer, w, ht); ok {
				// the driver has a form with (almost) this size, e.g. "4 x 6 in" for 100x150mm
				put16(78, int16(id))
				fields |= dmPaperSize
			} else {
				// custom size: dmPaperLength/dmPaperWidth (0.1 mm) override dmPaperSize
				put16(78, 0)
				put16(80, int16(ht*10))
				put16(82, int16(w*10))
				fields = fields&^dmPaperSize | dmPaperLength | dmPaperWidth
				if p.Warn != nil {
					p.Warn(fmt.Sprintf("kertas custom %gx%g mm dikirim ke driver; jika driver tidak mendukung ukuran custom, kertas default printer dipakai (buat form kertas di Windows / driver)", w, ht))
				}
			}
		} else if p.Warn != nil {
			p.Warn(fmt.Sprintf("ukuran kertas %q tidak dikenal printer; memakai default printer", p.Paper))
		}
	}
	if p.Tray != "" {
		if id, ok := resolveBin(printer, p.Tray); ok {
			put16(88, int16(id))
			fields |= dmDefaultSource
		} else if p.Warn != nil {
			p.Warn(fmt.Sprintf("tray %q tidak ditemukan; memakai default printer", p.Tray))
		}
	}
	switch p.Color {
	case "mono":
		put16(92, 1)
		fields |= dmColor
	case "color":
		put16(92, 2)
		fields |= dmColor
	}
	switch p.Duplex {
	case "simplex":
		put16(94, 1)
		fields |= dmDuplex
	case "long":
		put16(94, 2)
		fields |= dmDuplex
	case "short":
		put16(94, 3)
		fields |= dmDuplex
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

func driverPaper(printer, paper string) (int, bool) {
	c, err := WinBackend{}.Capabilities(printer)
	if err != nil {
		return 0, false
	}
	for _, pp := range c.Papers {
		if strings.EqualFold(strings.TrimSpace(pp.Name), paper) {
			return pp.ID, true
		}
	}
	return 0, false
}

func driverPaperBySize(printer string, w, h float64) (int, bool) {
	c, err := WinBackend{}.Capabilities(printer)
	if err != nil {
		return 0, false
	}
	near := func(a, b float64) bool { return a-b < 2.6 && b-a < 2.6 }
	for _, pp := range c.Papers {
		if (near(pp.WidthMM, w) && near(pp.HeightMM, h)) || (near(pp.WidthMM, h) && near(pp.HeightMM, w)) {
			return pp.ID, true
		}
	}
	return 0, false
}

func resolveBin(printer, tray string) (int, bool) {
	if n, err := strconv.Atoi(tray); err == nil && n > 0 {
		return n, true
	}
	if c, err := (WinBackend{}).Capabilities(printer); err == nil {
		for _, b := range c.Bins {
			if strings.EqualFold(strings.TrimSpace(b.Name), strings.TrimSpace(tray)) {
				return b.ID, true
			}
		}
	}
	id, ok := binNames[strings.ToLower(strings.ReplaceAll(tray, " ", ""))]
	return int(id), ok
}

type gdiDoc struct {
	hdc        uintptr
	w, h       int // printable area (minus margins)
	ox, oy     int // margin offset
	dpiX, dpiY int
}

func startGDIDoc(p *Payload) (*gdiDoc, error) {
	dm := devMode(p)
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
	dc := func(i uintptr) int { v, _, _ := procGetDeviceCaps.Call(hdc, i); return int(int32(v)) }
	d := &gdiDoc{hdc: hdc, w: dc(horzRes), h: dc(vertRes), dpiX: dc(logPixelsX), dpiY: dc(logPixelsY)}
	if p.MarginMM != nil && *p.MarginMM > 0 {
		mx, my := int(*p.MarginMM*float64(d.dpiX)/25.4), int(*p.MarginMM*float64(d.dpiY)/25.4)
		if d.w-2*mx > 0 && d.h-2*my > 0 {
			d.ox, d.oy, d.w, d.h = mx, my, d.w-2*mx, d.h-2*my
		}
	}
	return d, nil
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
		r, _, e := procStretchDIBits.Call(d.hdc, uintptr(x+d.ox), uintptr(y+d.oy), uintptr(w), uintptr(h), 0, 0, uintptr(iw), uintptr(ih),
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
					procTextOutW.Call(d.hdc, uintptr(d.ox), uintptr(d.oy+(i-start)*lineH), uintptr(unsafe.Pointer(&s[0])), uintptr(len(s)-1))
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
		switch strings.ToLower(p.Paper) {
		case "a2", "a3", "a4", "a5", "a6", "letter", "legal", "tabloid", "statement":
			settings = append(settings, "paper="+strings.ToLower(p.Paper))
		default:
			if p.Warn != nil {
				p.Warn(fmt.Sprintf("PDF: ukuran kertas %q tidak didukung SumatraPDF (hanya A2-A6, Letter, Legal, Tabloid, Statement); memakai kertas default printer", p.Paper))
			}
		}
	}
	switch p.Color {
	case "mono":
		settings = append(settings, "monochrome")
	case "color":
		settings = append(settings, "color")
	}
	switch p.Duplex {
	case "simplex":
		settings = append(settings, "simplex")
	case "long":
		settings = append(settings, "duplexlong")
	case "short":
		settings = append(settings, "duplexshort")
	}
	if p.Tray != "" {
		settings = append(settings, "bin="+p.Tray)
	}
	if p.MarginMM != nil && p.Warn != nil {
		p.Warn("PDF: margin_mm diabaikan (margin mengikuti PDF)")
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
