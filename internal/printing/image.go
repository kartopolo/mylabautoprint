package printing

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // register GIF decoder
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
)

// decodeImageBGRA decodes PNG/JPEG/GIF and returns top-down 32bpp BGRA
// pixels with alpha composited onto white (ready for a GDI DIB).
func decodeImageBGRA(b []byte) (w, h int, pix []byte, err error) {
	img, format, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, 0, nil, fmt.Errorf("gambar tidak bisa dibaca (dukungan: PNG, JPG, GIF): %w", err)
	}
	_ = format
	r := img.Bounds()
	w, h = r.Dx(), r.Dy()
	if w <= 0 || h <= 0 || w*h > 80_000_000 {
		return 0, 0, nil, fmt.Errorf("ukuran gambar tidak valid: %dx%d", w, h)
	}
	// Fast path: convert RGBA/NRGBA buffers in place (no second full-size copy).
	switch m := img.(type) {
	case *image.NRGBA:
		if m.Stride == w*4 && r.Min == (image.Point{}) {
			p := m.Pix
			for i := 0; i < len(p); i += 4 {
				a := uint32(p[i+3])
				rr := uint8((uint32(p[i])*a + 255*(255-a)) / 255)
				p[i+1] = uint8((uint32(p[i+1])*a + 255*(255-a)) / 255)
				p[i] = uint8((uint32(p[i+2])*a + 255*(255-a)) / 255)
				p[i+2], p[i+3] = rr, 0
			}
			return w, h, p, nil
		}
	case *image.RGBA: // premultiplied
		if m.Stride == w*4 && r.Min == (image.Point{}) {
			p := m.Pix
			for i := 0; i < len(p); i += 4 {
				ia := 255 - uint32(p[i+3])
				rr := uint8(uint32(p[i]) + ia)
				p[i+1] = uint8(uint32(p[i+1]) + ia)
				p[i] = uint8(uint32(p[i+2]) + ia)
				p[i+2], p[i+3] = rr, 0
			}
			return w, h, p, nil
		}
	}
	pix = make([]byte, w*h*4)
	i := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			a := uint32(c.A)
			// composite on white
			pix[i+0] = uint8((uint32(c.B)*a + 255*(255-a)) / 255)
			pix[i+1] = uint8((uint32(c.G)*a + 255*(255-a)) / 255)
			pix[i+2] = uint8((uint32(c.R)*a + 255*(255-a)) / 255)
			pix[i+3] = 0
			i += 4
		}
	}
	return w, h, pix, nil
}

// fitRect computes destination size for an image of iw x ih on a page of
// pw x ph device pixels with the given scale mode and printer dpi.
func fitRect(iw, ih, pw, ph, dpiX, dpiY int, scale string) (x, y, w, h int) {
	switch scale {
	case "noscale": // treat image as 96 dpi
		w = iw * dpiX / 96
		h = ih * dpiY / 96
	case "width":
		w = pw
		h = ih * pw / iw
	default: // fit
		if iw*ph > ih*pw { // wider than page
			w = pw
			h = ih * pw / iw
		} else {
			h = ph
			w = iw * ph / ih
		}
	}
	if scale != "width" && scale != "noscale" {
		x = (pw - w) / 2
	}
	return x, 0, w, h
}
