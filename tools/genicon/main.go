// genicon draws the MyLab AutoPrint icon and writes PNG/ICO variants.
// Usage: go run ./tools/genicon
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

func draw(size int, badge bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	blue := color.NRGBA{31, 95, 191, 255}
	white := color.NRGBA{255, 255, 255, 255}
	dark := color.NRGBA{20, 60, 125, 255}
	r := s * 0.2
	in := func(x, y, x0, y0, x1, y1 float64) bool { return x >= x0 && x < x1 && y >= y0 && y < y1 }
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			// rounded square
			cx := math.Max(r-fx, math.Max(fx-(s-r), 0))
			cy := math.Max(r-fy, math.Max(fy-(s-r), 0))
			if cx*cx+cy*cy > r*r {
				continue
			}
			c := blue
			u, v := fx/s, fy/s
			switch {
			case in(u, v, 0.30, 0.16, 0.70, 0.40): // paper in
				c = white
			case in(u, v, 0.16, 0.38, 0.84, 0.70): // printer body
				c = white
				if in(u, v, 0.66, 0.45, 0.74, 0.52) {
					c = color.NRGBA{46, 157, 91, 255} // led
				}
			case in(u, v, 0.30, 0.62, 0.70, 0.86): // paper out
				c = white
				if in(u, v, 0.36, 0.70, 0.64, 0.73) || in(u, v, 0.36, 0.77, 0.58, 0.80) {
					c = dark
				}
			}
			if in(u, v, 0.30, 0.62, 0.70, 0.645) {
				c = dark // slot
			}
			if badge {
				dx, dy := u-0.80, v-0.20
				if dx*dx+dy*dy < 0.18*0.18 {
					c = color.NRGBA{230, 126, 34, 255}
				}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func pngBytes(img image.Image) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// bmpEntry encodes an icon image as a 32bpp BMP DIB (with AND mask).
func bmpEntry(img *image.NRGBA) []byte {
	n := img.Rect.Dx()
	var b bytes.Buffer
	maskRow := ((n + 31) / 32) * 4
	binary.Write(&b, binary.LittleEndian, struct {
		Size                  uint32
		W, H                  int32
		Planes, Bits          uint16
		Comp, ImgSize         uint32
		XPPM, YPPM            int32
		ClrUsed, ClrImportant uint32
	}{40, int32(n), int32(n * 2), 1, 32, 0, uint32(n*n*4 + maskRow*n), 0, 0, 0, 0})
	for y := n - 1; y >= 0; y-- {
		for x := 0; x < n; x++ {
			c := img.NRGBAAt(x, y)
			b.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	b.Write(make([]byte, maskRow*n))
	return b.Bytes()
}

func ico(sizes []int, badge bool, pngLarge bool) []byte {
	var entries [][]byte
	for _, s := range sizes {
		img := draw(s, badge)
		if s >= 256 && pngLarge {
			entries = append(entries, pngBytes(img))
		} else {
			entries = append(entries, bmpEntry(img))
		}
	}
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, []uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, s := range sizes {
		d := byte(s)
		if s >= 256 {
			d = 0
		}
		b.Write([]byte{d, d, 0, 0})
		binary.Write(&b, binary.LittleEndian, []uint16{1, 32})
		binary.Write(&b, binary.LittleEndian, []uint32{uint32(len(entries[i])), uint32(off)})
		off += len(entries[i])
	}
	for _, e := range entries {
		b.Write(e)
	}
	return b.Bytes()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func write(p string, b []byte) {
	must(os.MkdirAll(filepath.Dir(p), 0o755))
	must(os.WriteFile(p, b, 0o644))
}

func main() {
	write("internal/server/web/icon.png", pngBytes(draw(64, false)))
	write("cmd/mylabautoprint/assets/tray.ico", ico([]int{16, 24, 32, 48}, false, false))
	write("cmd/mylabautoprint/assets/tray_update.ico", ico([]int{16, 24, 32, 48}, true, false))
	write("winres/icon.png", pngBytes(draw(256, false)))
	write("winres/icon16.png", pngBytes(draw(16, false)))
	write("winres/icon32.png", pngBytes(draw(32, false)))
	write("winres/icon48.png", pngBytes(draw(48, false)))
	write("installer/icon.ico", ico([]int{16, 32, 48}, false, false))
}
