package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// adam7 lists the passes of PNG interlacing: x0, y0, dx, dy.
var adam7 = [7][4]int{
	{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4},
	{0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2},
}

// buildInterlacedPNG writes an interlaced PNG by hand, which image/png
// cannot do. px returns the channel values of a pixel at the bit depth.
// Every pass row uses filter type 0.
func buildInterlacedPNG(t *testing.T, w, h int, bitDepth, colorType byte, px func(x, y int) []uint16, plte, trns []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	out.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	chunk := func(typ string, data []byte) {
		binary.Write(&out, binary.BigEndian, uint32(len(data)))
		td := append([]byte(typ), data...)
		out.Write(td)
		binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(td))
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8] = bitDepth
	ihdr[9] = colorType
	ihdr[12] = 1
	chunk("IHDR", ihdr)
	if plte != nil {
		chunk("PLTE", plte)
	}
	if trns != nil {
		chunk("tRNS", trns)
	}
	var raw bytes.Buffer
	for _, p := range adam7 {
		for y := p[1]; y < h; y += p[3] {
			if p[0] >= w {
				break
			}
			raw.WriteByte(0)
			var acc uint32
			var nbits int
			for x := p[0]; x < w; x += p[2] {
				for _, v := range px(x, y) {
					if bitDepth == 16 {
						raw.Write([]byte{byte(v >> 8), byte(v)})
						continue
					}
					acc = acc<<bitDepth | uint32(v)
					nbits += int(bitDepth)
					if nbits == 8 {
						raw.WriteByte(byte(acc))
						acc, nbits = 0, 0
					}
				}
			}
			if nbits > 0 {
				raw.WriteByte(byte(acc << (8 - nbits)))
			}
		}
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(raw.Bytes())
	zw.Close()
	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}

// parseOK runs parsePNG and fails the test on an error.
func parseOK(t *testing.T, b []byte) *Imagefile {
	t.Helper()
	imgf := &Imagefile{r: newReader(b)}
	if err := imgf.parsePNG(); err != nil {
		t.Fatalf("parsePNG: %v", err)
	}
	return imgf
}

// palettedPNG encodes a w x h paletted image whose stripe x*n/w uses
// palette index x*n/w, n the number of stripes.
func palettedPNG(t *testing.T, pal color.Palette, n int) []byte {
	t.Helper()
	const w, h = 8, 3
	img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for y := range h {
		for x := range w {
			img.SetColorIndex(x, y, uint8(x*n/w))
		}
	}
	return newPNGBytes(t, img)
}

// padPalette appends opaque entries until pal has n entries, so that
// image/png writes 8-bit indices.
func padPalette(pal color.Palette, n int) color.Palette {
	for len(pal) < n {
		pal = append(pal, color.NRGBA{uint8(len(pal)), 0, 0, 255})
	}
	return pal
}

func TestParsePNG_PaletteAlpha(t *testing.T) {
	red := func(a uint8) color.NRGBA { return color.NRGBA{200, 30, 30, a} }
	for _, tc := range []struct {
		name      string
		pal       color.Palette
		depth     byte
		decoded   bool  // taken by decodePNG
		wantAlpha []int // alpha per stripe, nil for no soft mask
		wantKey   []int // color key, nil for none
	}{
		{"partial alpha 2 bit", color.Palette{red(255), red(128), red(0), red(0)}, 2, true, []int{255, 128, 0, 0}, nil},
		{"two transparent 8 bit", padPalette(color.Palette{red(255), red(0), red(0), red(255)}, 20), 8, true, []int{255, 0, 0, 255}, nil},
		{"one transparent 2 bit", color.Palette{red(255), red(255), red(0), red(255)}, 2, true, nil, []int{2}},
		{"one transparent 8 bit", padPalette(color.Palette{red(255), red(255), red(0), red(255)}, 20), 8, false, nil, []int{2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := palettedPNG(t, tc.pal, 4)
			if b[24] != tc.depth {
				t.Fatalf("test PNG has bit depth %d, want %d", b[24], tc.depth)
			}
			imgf := parseOK(t, b)
			if imgf.colorspace != "Indexed" {
				t.Errorf("colorspace=%q, want Indexed", imgf.colorspace)
			}
			if got := imgf.decodeParms == nil; got != tc.decoded {
				t.Fatalf("decoded=%v, want %v", got, tc.decoded)
			}
			if !tc.decoded {
				if len(imgf.trns) != 1 || imgf.trns[0] != tc.wantKey[0] {
					t.Errorf("trns=%v, want %v", imgf.trns, tc.wantKey)
				}
				return
			}
			if imgf.BitsPerComponent() != 8 {
				t.Errorf("BitsPerComponent()=%d, want 8", imgf.BitsPerComponent())
			}
			// Decoded indices are unfiltered, one byte each.
			idx := inflate(t, imgf.data)
			if len(idx) != 8*3 {
				t.Fatalf("index data is %d bytes, want %d", len(idx), 8*3)
			}
			for i, v := range idx {
				if want := byte(i % 8 * 4 / 8); v != want {
					t.Fatalf("index %d = %d, want %d", i, v, want)
				}
			}
			pdf := writePDF(t, imgf)
			if strings.Contains(pdf, "/DecodeParms") {
				t.Errorf("unexpected /DecodeParms in\n%s", pdf)
			}
			if tc.wantAlpha != nil {
				if !imgf.HasSoftMask() {
					t.Fatalf("no soft mask")
				}
				if len(imgf.trns) != 0 {
					t.Errorf("trns=%v next to a soft mask", imgf.trns)
				}
				for i, a := range imgf.smask {
					if want := tc.wantAlpha[i%8*4/8]; int(a) != want {
						t.Fatalf("alpha %d = %d, want %d", i, a, want)
					}
				}
				if strings.Contains(pdf, "/Mask") {
					t.Errorf("unexpected /Mask in\n%s", pdf)
				}
				return
			}
			if imgf.HasSoftMask() {
				t.Errorf("unexpected soft mask")
			}
			if !strings.Contains(pdf, "/Mask [ 2 2 ]") {
				t.Errorf("no /Mask [ 2 2 ] in\n%s", pdf)
			}
			if !strings.Contains(pdf, "/BitsPerComponent 8") {
				t.Errorf("no /BitsPerComponent 8 in\n%s", pdf)
			}
		})
	}
}

func TestParsePNG_Interlaced(t *testing.T) {
	// 11 x 9 leaves some passes with partial rows and columns.
	const w, h = 11, 9
	val := func(x, y, c int, max uint16) uint16 {
		return uint16((x*7 + y*13 + c*5) % (int(max) + 1))
	}
	be := func(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }
	for _, tc := range []struct {
		name       string
		depth, ct  byte
		channels   int
		plte, trns []byte
		colorspace string
		bits       int
		// color and alpha turn the channel values of a pixel into the
		// samples the PDF should get.
		color func(v []uint16) []byte
		alpha func(v []uint16) []byte
		key   string
	}{
		{name: "gray 8", depth: 8, ct: colGrayScale, channels: 1, colorspace: "DeviceGray", bits: 8,
			color: func(v []uint16) []byte { return []byte{byte(v[0])} }},
		{name: "gray 16", depth: 16, ct: colGrayScale, channels: 1, colorspace: "DeviceGray", bits: 16,
			color: func(v []uint16) []byte { return be(v[0]) }},
		{name: "gray 2 with key", depth: 2, ct: colGrayScale, channels: 1, trns: []byte{0, 1}, colorspace: "DeviceGray", bits: 8,
			color: func(v []uint16) []byte { return []byte{byte(v[0] * 85)} }, key: "/Mask [ 85 85 ]"},
		{name: "gray alpha 8", depth: 8, ct: colGrayScaleWithAlpha, channels: 2, colorspace: "DeviceGray", bits: 8,
			color: func(v []uint16) []byte { return []byte{byte(v[0])} },
			alpha: func(v []uint16) []byte { return []byte{byte(v[1])} }},
		{name: "rgb 8 with key", depth: 8, ct: colTrueColor, channels: 3, trns: []byte{0, 10, 0, 20, 0, 30}, colorspace: "DeviceRGB", bits: 8,
			color: func(v []uint16) []byte { return []byte{byte(v[0]), byte(v[1]), byte(v[2])} }, key: "/Mask [ 10 10 20 20 30 30 ]"},
		{name: "rgb 16", depth: 16, ct: colTrueColor, channels: 3, colorspace: "DeviceRGB", bits: 16,
			color: func(v []uint16) []byte { return append(append(be(v[0]), be(v[1])...), be(v[2])...) }},
		{name: "rgba 8", depth: 8, ct: colTrueColorWithAlpha, channels: 4, colorspace: "DeviceRGB", bits: 8,
			color: func(v []uint16) []byte { return []byte{byte(v[0]), byte(v[1]), byte(v[2])} },
			alpha: func(v []uint16) []byte { return []byte{byte(v[3])} }},
		{name: "rgba 16", depth: 16, ct: colTrueColorWithAlpha, channels: 4, colorspace: "DeviceRGB", bits: 16,
			color: func(v []uint16) []byte { return append(append(be(v[0]), be(v[1])...), be(v[2])...) },
			alpha: func(v []uint16) []byte { return be(v[3]) }},
		{name: "indexed 4", depth: 4, ct: colIndexedColor, channels: 1, plte: bytes.Repeat([]byte{1, 2, 3}, 16), colorspace: "Indexed", bits: 8,
			color: func(v []uint16) []byte { return []byte{byte(v[0])} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			max := uint16(1<<tc.depth - 1)
			px := func(x, y int) []uint16 {
				v := make([]uint16, tc.channels)
				for c := range v {
					v[c] = val(x, y, c, max)
				}
				if tc.depth == 16 {
					for c := range v {
						v[c] = v[c]*4099 + 1
					}
				}
				return v
			}
			b := buildInterlacedPNG(t, w, h, tc.depth, tc.ct, px, tc.plte, tc.trns)
			imgf := parseOK(t, b)
			if imgf.colorspace != tc.colorspace {
				t.Errorf("colorspace=%q, want %q", imgf.colorspace, tc.colorspace)
			}
			if imgf.BitsPerComponent() != tc.bits {
				t.Errorf("BitsPerComponent()=%d, want %d", imgf.BitsPerComponent(), tc.bits)
			}
			var wantColor, wantAlpha []byte
			for y := range h {
				for x := range w {
					wantColor = append(wantColor, tc.color(px(x, y))...)
					if tc.alpha != nil {
						wantAlpha = append(wantAlpha, tc.alpha(px(x, y))...)
					}
				}
			}
			if got := inflate(t, imgf.data); !bytes.Equal(got, wantColor) {
				t.Errorf("color samples\n got % x\nwant % x", got, wantColor)
			}
			if !bytes.Equal(imgf.smask, wantAlpha) {
				t.Errorf("alpha samples\n got % x\nwant % x", imgf.smask, wantAlpha)
			}
			if imgf.HasSoftMask() != (tc.alpha != nil) {
				t.Errorf("HasSoftMask()=%v, want %v", imgf.HasSoftMask(), tc.alpha != nil)
			}
			pdf := writePDF(t, imgf)
			if tc.key != "" && !strings.Contains(pdf, tc.key) {
				t.Errorf("no %s in\n%s", tc.key, pdf)
			}
			if tc.key == "" && strings.Contains(pdf, "/Mask") {
				t.Errorf("unexpected /Mask in\n%s", pdf)
			}
		})
	}
}

// TestParsePNG_InterlacedMatchesImagePNG checks the fallback against what
// image/png decodes, for an interlaced RGBA file.
func TestParsePNG_InterlacedMatchesImagePNG(t *testing.T) {
	const w, h = 13, 6
	px := func(x, y int) []uint16 {
		return []uint16{uint16(x * 19), uint16(y * 40), uint16(x * y), uint16(255 - x*y*3)}
	}
	b := buildInterlacedPNG(t, w, h, 8, colTrueColorWithAlpha, px, nil, nil)
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("image/png: %v", err)
	}
	m := img.(*image.NRGBA)
	imgf := parseOK(t, b)
	got := inflate(t, imgf.data)
	for y := range h {
		for x := range w {
			c := m.NRGBAAt(x, y)
			i := y*w + x
			if !bytes.Equal(got[3*i:3*i+3], []byte{c.R, c.G, c.B}) || imgf.smask[i] != c.A {
				t.Fatalf("pixel %d,%d = % x / %x, image/png has %v", x, y, got[3*i:3*i+3], imgf.smask[i], c)
			}
		}
	}
}
