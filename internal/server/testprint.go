package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"time"

	"mylabautoprint/internal/printing"
)

// TestRequest builds a sample print job of the given type.
func TestRequest(typ, version string) (printing.Request, error) {
	now := time.Now().Format("02-01-2006 15:04:05")
	switch typ {
	case "", "text":
		return printing.Request{Type: "text", JobName: "MyLab AutoPrint - Test Text",
			Text: fmt.Sprintf("MyLab AutoPrint v%s\nTEST PRINT (text)\n%s\n\nJika tulisan ini tercetak,\nprinter siap dipakai.\n", version, now)}, nil
	case "raw":
		var b bytes.Buffer
		b.Write([]byte{0x1b, 0x40})       // ESC @ init
		b.Write([]byte{0x1b, 0x61, 0x01}) // center
		b.Write([]byte{0x1b, 0x45, 0x01}) // bold on
		b.WriteString("MyLab AutoPrint\n")
		b.Write([]byte{0x1b, 0x45, 0x00})
		b.WriteString("TEST PRINT (raw ESC/POS)\n" + now + "\n")
		b.Write([]byte{0x1b, 0x61, 0x00})
		b.WriteString("--------------------------------\nItem A            1 x  10.000\nItem B            2 x   5.000\n--------------------------------\nTOTAL                   20.000\n\n\n\n")
		b.Write([]byte{0x1d, 0x56, 0x42, 0x00}) // GS V B 0: feed + cut
		return printing.Request{Type: "raw", JobName: "MyLab AutoPrint - Test Raw", Data: base64.StdEncoding.EncodeToString(b.Bytes())}, nil
	case "image":
		return printing.Request{Type: "image", JobName: "MyLab AutoPrint - Test Image", Data: base64.StdEncoding.EncodeToString(TestPNG())}, nil
	case "pdf":
		return printing.Request{Type: "pdf", JobName: "MyLab AutoPrint - Test PDF",
			Data: base64.StdEncoding.EncodeToString(TestPDF("MyLab AutoPrint v" + version + " - TEST PRINT (PDF) " + now))}, nil
	}
	return printing.Request{}, fmt.Errorf("type test tidak dikenal: %s", typ)
}

// TestPNG renders a simple test pattern.
func TestPNG() []byte {
	const w, h = 600, 300
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{255, 255, 255, 255}
			switch {
			case x < 6 || y < 6 || x >= w-6 || y >= h-6:
				c = color.NRGBA{0, 0, 0, 255}
			case y > 40 && y < 120 && x > 40 && x < 560:
				c = color.NRGBA{uint8(x * 255 / w), 90, 200, 255}
			case y > 150 && y < 260 && (x/40)%2 == 0:
				c = color.NRGBA{30, 30, 30, 255}
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// TestPDF builds a minimal single page A4 PDF with one line of text.
func TestPDF(text string) []byte {
	esc := func(s string) string {
		var o bytes.Buffer
		for _, r := range s {
			if r == '(' || r == ')' || r == '\\' {
				o.WriteByte('\\')
			}
			if r < 128 {
				o.WriteRune(r)
			}
		}
		return o.String()
	}
	content := fmt.Sprintf("BT /F1 16 Tf 60 780 Td (%s) Tj ET\n0 0 1 RG 4 w 50 700 495 50 re S\n", esc(text))
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}
