package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newPNGBytes encodes the given image.Image as PNG and returns the bytes.
// We use standard library encoder to generate tiny, well-formed PNGs.
func newPNGBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// newReader returns a ReadSeeker over the given bytes (parsePNG needs seeking).
func newReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// makeNRGBA builds a tiny NRGBA image (RGB or RGBA depending on alpha values).
func makeNRGBA(w, h int, withAlpha bool) *image.NRGBA {
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	// Fill with a simple pattern; alpha depends on withAlpha.
	for y := range h {
		for x := range w {
			a := uint8(0xFF)
			if withAlpha {
				// vary alpha: left half opaque, right half semi-transparent
				if x >= w/2 {
					a = 0x40
				}
			}
			im.SetNRGBA(x, y, color.NRGBA{R: 0x10, G: 0x80, B: 0xF0, A: a})
		}
	}
	return im
}

// makeGray builds a tiny 8-bit grayscale image.
func makeGray(w, h int) *image.Gray {
	im := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			im.SetGray(x, y, color.Gray{Y: uint8((x + y) % 256)})
		}
	}
	return im
}

// makeIndexed builds a tiny paletted image (Indexed color).
func makeIndexed(w, h int) *image.Paletted {
	pal := color.Palette{
		color.RGBA{0x00, 0x00, 0x00, 0xFF}, // index 0
		color.RGBA{0xFF, 0x00, 0x00, 0xFF}, // index 1
		color.RGBA{0x00, 0xFF, 0x00, 0xFF}, // index 2
		color.RGBA{0x00, 0x00, 0xFF, 0xFF}, // index 3
	}
	im := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for y := range h {
		for x := range w {
			im.SetColorIndex(x, y, uint8((x+y)%len(pal)))
		}
	}
	return im
}

func TestParsePNG_RGB8_NoAlpha(t *testing.T) {
	img := makeNRGBA(2, 2, false) // fully opaque -> truecolor, no alpha
	b := newPNGBytes(t, img)

	imgf := &Imagefile{r: newReader(b)}
	if err := imgf.parsePNG(); err != nil {
		t.Fatalf("parsePNG error: %v", err)
	}

	if imgf.W != 2 || imgf.H != 2 {
		t.Fatalf("got size %dx%d, want 2x2", imgf.W, imgf.H)
	}
	if imgf.colorspace != "DeviceRGB" {
		t.Fatalf("colorspace=%q, want DeviceRGB", imgf.colorspace)
	}
	if imgf.bitsPerComponent != "8" {
		t.Fatalf("bitsPerComponent=%q, want \"8\"", imgf.bitsPerComponent)
	}
	// DecodeParms should include Predictor=15 and Columns=w; Colors=3 for RGB.
	if col, ok := imgf.decodeParms["Columns"]; !ok || col != 2 {
		t.Fatalf("decodeParms Columns=%v, want 2", col)
	}
	if colors, ok := imgf.decodeParms["Colors"]; !ok || colors != 3 {
		t.Fatalf("decodeParms Colors=%v, want 3", colors)
	}
	if len(imgf.smask) != 0 {
		t.Fatalf("unexpected smask for RGB without alpha: %d bytes", len(imgf.smask))
	}
	if len(imgf.data) == 0 {
		t.Fatalf("image data should be present")
	}
}

func TestParsePNG_RGBA8_WithAlpha(t *testing.T) {
	img := makeNRGBA(4, 2, true) // has varying alpha
	b := newPNGBytes(t, img)

	imgf := &Imagefile{r: newReader(b)}
	if err := imgf.parsePNG(); err != nil {
		t.Fatalf("parsePNG error: %v", err)
	}

	if imgf.colorspace != "DeviceRGB" {
		t.Fatalf("colorspace=%q, want DeviceRGB", imgf.colorspace)
	}
	// With alpha, parsePNG splits color and alpha: smask gets alpha, data is (re)compressed color.
	if len(imgf.smask) == 0 {
		t.Fatalf("expected non-empty smask for RGBA input")
	}
	if len(imgf.data) == 0 {
		t.Fatalf("expected non-empty compressed color data for RGBA input")
	}
	// Smask decode parms should be set for grayscale (Colors=1) with same Columns.
	if cols, ok := imgf.decodeParmsSmask["Columns"]; !ok || cols != imgf.W {
		t.Fatalf("decodeParmsSmask Columns=%v, want %d", cols, imgf.W)
	}
	if colors, ok := imgf.decodeParmsSmask["Colors"]; !ok || colors != 1 {
		t.Fatalf("decodeParmsSmask Colors=%v, want 1", colors)
	}
}

func TestParsePNG_Gray8(t *testing.T) {
	img := makeGray(3, 1)
	b := newPNGBytes(t, img)

	imgf := &Imagefile{r: newReader(b)}
	if err := imgf.parsePNG(); err != nil {
		t.Fatalf("parsePNG error: %v", err)
	}
	if imgf.colorspace != "DeviceGray" {
		t.Fatalf("colorspace=%q, want DeviceGray", imgf.colorspace)
	}
	// For DeviceGray, no Colors in decodeParms (only Predictor/Columns [+ BitsPerComponent if !=8]).
	if _, ok := imgf.decodeParms["Colors"]; ok {
		t.Fatalf("decodeParms Colors should be absent for DeviceGray")
	}
}

func TestParsePNG_Indexed8_WithPalette(t *testing.T) {
	img := makeIndexed(2, 2)
	b := newPNGBytes(t, img)

	imgf := &Imagefile{r: newReader(b)}
	if err := imgf.parsePNG(); err != nil {
		t.Fatalf("parsePNG error: %v", err)
	}
	if imgf.colorspace != "Indexed" {
		t.Fatalf("colorspace=%q, want Indexed", imgf.colorspace)
	}
	if len(imgf.pal) == 0 {
		t.Fatalf("expected non-empty PLTE palette for indexed PNG")
	}
	// No RGBA split for indexed without tRNS; smask should be empty.
	if len(imgf.smask) != 0 {
		t.Fatalf("unexpected smask for indexed PNG without transparency")
	}
}

// Expect describes optional per-file expectations loaded from a sidecar JSON.
// Put a file named like "<image>.json" next to "<image>.png" to enforce expectations.
type Expect struct {
	Width            *int   `json:"width,omitempty"`            // expected width
	Height           *int   `json:"height,omitempty"`           // expected height
	ColorSpace       string `json:"colorspace,omitempty"`       // "DeviceRGB", "DeviceGray", "Indexed"
	BitsPerComponent string `json:"bitsPerComponent,omitempty"` // usually "8"
	HasTRNS          *bool  `json:"hasTRNS,omitempty"`          // true if tRNS chunk expected
	HasPalette       *bool  `json:"hasPalette,omitempty"`       // true for Indexed, false otherwise
	ExpectError      string `json:"expectError,omitempty"`      // substring that must appear in the error
}

// loadExpect tries to load "<png>.json". If absent, returns zero Expect and false.
func loadExpect(pngPath string) (Expect, bool, error) {
	jsonPath := pngPath[:len(pngPath)-len(filepath.Ext(pngPath))] + ".json"
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Expect{}, false, nil
		}
		return Expect{}, false, err
	}
	var e Expect
	if err := json.Unmarshal(b, &e); err != nil {
		return Expect{}, false, err
	}
	return e, true, nil
}

// newImagefileFromPath opens a PNG and returns an Imagefile wired to it.
func newImagefileFromPath(t *testing.T, path string) *Imagefile {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	// Let the test close the file when done by attaching it to Imagefile if needed.
	// parsePNG uses imgf.r (io.ReadSeeker); *os.File implements that.
	return &Imagefile{r: f}
}

func TestParsePNG_TestdataDirectory(t *testing.T) {
	dir := filepath.Join("testdata", "png")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no %s directory: %v", dir, err)
	}

	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".png" {
			continue
		}
		pngPath := filepath.Join(dir, e.Name())
		expect, haveExpect, err := loadExpect(pngPath)
		if err != nil {
			t.Fatalf("read %s sidecar: %v", pngPath, err)
		}

		t.Run(e.Name(), func(t *testing.T) {
			imgf := newImagefileFromPath(t, pngPath)
			defer func() {
				// Close file if r is *os.File; ignore otherwise.
				if f, ok := imgf.r.(*os.File); ok {
					_ = f.Close()
				}
			}()

			err := imgf.parsePNG()

			// If an error is expected, assert and return early.
			if haveExpect && expect.ExpectError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", expect.ExpectError)
				}
				if !containsIgnoreCase(err.Error(), expect.ExpectError) {
					t.Fatalf("error %q does not contain %q", err.Error(), expect.ExpectError)
				}
				return
			}

			// Otherwise, any error is a failure.
			if err != nil {
				t.Fatalf("parsePNG(%s): %v", e.Name(), err)
			}

			// Basic invariants that should hold for all valid files.
			if imgf.W <= 0 || imgf.H <= 0 {
				t.Fatalf("invalid dimensions: %dx%d", imgf.W, imgf.H)
			}
			if imgf.bitsPerComponent == "" {
				t.Fatalf("missing bitsPerComponent")
			}
			if imgf.colorspace == "" {
				t.Fatalf("missing colorspace")
			}

			// Apply expectations when present.
			if haveExpect {
				if expect.Width != nil && imgf.W != *expect.Width {
					t.Errorf("width=%d, want %d", imgf.W, *expect.Width)
				}
				if expect.Height != nil && imgf.H != *expect.Height {
					t.Errorf("height=%d, want %d", imgf.H, *expect.Height)
				}
				if expect.ColorSpace != "" && imgf.colorspace != expect.ColorSpace {
					t.Errorf("colorspace=%q, want %q", imgf.colorspace, expect.ColorSpace)
				}
				if expect.BitsPerComponent != "" && imgf.bitsPerComponent != expect.BitsPerComponent {
					t.Errorf("bitsPerComponent=%q, want %q", imgf.bitsPerComponent, expect.BitsPerComponent)
				}
				if expect.HasPalette != nil {
					got := len(imgf.pal) > 0
					if got != *expect.HasPalette {
						t.Errorf("palette present=%v, want %v", got, *expect.HasPalette)
					}
				}
				if expect.HasTRNS != nil {
					got := len(imgf.trns) > 0
					if got != *expect.HasTRNS {
						t.Errorf("tRNS present=%v, want %v", got, *expect.HasTRNS)
					}
				}
			}

			// Sanity: passed through, decodeParms must advertise PNG
			// predictor & columns. Decoded images (decodePNG) have none.
			if imgf.decodeParms != nil {
				if v, ok := imgf.decodeParms["Predictor"]; !ok || v != 15 {
					t.Errorf("decodeParms.Predictor=%v, want 15", v)
				}
				if v, ok := imgf.decodeParms["Columns"]; !ok || v != imgf.W {
					t.Errorf("decodeParms.Columns=%v, want %d", v, imgf.W)
				}
			}
			// For RGB images passed through, Colors=3 should be set.
			if imgf.colorspace == "DeviceRGB" && imgf.decodeParms != nil {
				if v, ok := imgf.decodeParms["Colors"]; !ok || v != 3 {
					t.Errorf("decodeParms.Colors=%v, want 3 (DeviceRGB)", v)
				}
			}
			// If the file had alpha (ct >= 4), parsePNG splits alpha into smask.
			// We can't know that for sure without expectations, but we can at least ensure
			// that data exists; smask may be empty for non-alpha images.
			if len(imgf.data) == 0 {
				t.Errorf("image data is empty")
			}
		})
	}
}

func containsIgnoreCase(s, sub string) bool {
	// cheap, dependency-free case-insensitive contains
	ls, lsub := []rune(s), []rune(sub)
	for i := range ls {
		if i+len(lsub) > len(ls) {
			break
		}
		match := true
		for j := range lsub {
			a := ls[i+j]
			b := lsub[j]
			// ASCII fold only; enough for test error messages
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// buildPNG writes a PNG by hand, for what image/png cannot write: gray with
// alpha, tRNS for gray and RGB, and bit depths it does not allow for a color
// type. samples holds the unfiltered rows. Row y uses filter type y%5, so the
// rows exercise every filter.
func buildPNG(t *testing.T, w, h int, bitDepth, colorType byte, samples [][]byte, trns []byte) []byte {
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
	chunk("IHDR", ihdr)
	if trns != nil {
		chunk("tRNS", trns)
	}
	bpp := max(1, int(bitDepth)*pngChannels(colorType)/8)
	var raw bytes.Buffer
	var prev []byte
	for y, row := range samples {
		ft := byte(y % 5)
		raw.WriteByte(ft)
		raw.Write(pngFilter(ft, row, prev, bpp))
		prev = row
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(raw.Bytes())
	zw.Close()
	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}

func pngChannels(colorType byte) int {
	switch colorType {
	case colTrueColor:
		return 3
	case colGrayScaleWithAlpha:
		return 2
	case colTrueColorWithAlpha:
		return 4
	default:
		return 1
	}
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// pngFilter applies PNG filter type ft to row; prev is the previous
// unfiltered row, nil for the first.
func pngFilter(ft byte, row, prev []byte, bpp int) []byte {
	out := make([]byte, len(row))
	for i := range row {
		var a, b, c byte
		if i >= bpp {
			a = row[i-bpp]
		}
		if prev != nil {
			b = prev[i]
			if i >= bpp {
				c = prev[i-bpp]
			}
		}
		switch ft {
		case 0:
			out[i] = row[i]
		case 1:
			out[i] = row[i] - a
		case 2:
			out[i] = row[i] - b
		case 3:
			out[i] = row[i] - byte((int(a)+int(b))/2)
		case 4:
			out[i] = row[i] - paeth(a, b, c)
		}
	}
	return out
}

// pngUnfilter reverses the PNG filters of rows that start with a filter type
// byte, as a PDF reader does for /Predictor 15, and returns the samples.
func pngUnfilter(t *testing.T, data []byte, rowBytes, h, bpp int) [][]byte {
	t.Helper()
	if len(data) != (1+rowBytes)*h {
		t.Fatalf("filtered data is %d bytes, want %d", len(data), (1+rowBytes)*h)
	}
	rows := make([][]byte, h)
	prev := make([]byte, rowBytes)
	for y := range h {
		ft := data[y*(1+rowBytes)]
		in := data[y*(1+rowBytes)+1 : (y+1)*(1+rowBytes)]
		row := make([]byte, rowBytes)
		for i := range in {
			var a, c byte
			if i >= bpp {
				a = row[i-bpp]
				c = prev[i-bpp]
			}
			b := prev[i]
			switch ft {
			case 0:
				row[i] = in[i]
			case 1:
				row[i] = in[i] + a
			case 2:
				row[i] = in[i] + b
			case 3:
				row[i] = in[i] + byte((int(a)+int(b))/2)
			case 4:
				row[i] = in[i] + paeth(a, b, c)
			default:
				t.Fatalf("row %d: filter type %d", y, ft)
			}
		}
		rows[y] = row
		prev = row
	}
	return rows
}

func inflate(t *testing.T, data []byte) []byte {
	t.Helper()
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("zlib: %v", err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("zlib: %v", err)
	}
	return b
}

// gradient16 returns a 16-bit sample that changes in steps finer than 8 bits
// can hold, so a test sees whether the low byte survives.
func gradient16(x, y, channel int) uint16 {
	return uint16(x*4099 + y*257 + channel*13001)
}

// writePDF runs the image through finishBitmap and returns the PDF output.
func writePDF(t *testing.T, imgf *Imagefile) string {
	t.Helper()
	var out bytes.Buffer
	imgf.pw = NewPDFWriter(&out)
	imgf.Format = "png"
	imgf.imageobject = imgf.pw.NewObject()
	if err := finishBitmap(imgf); err != nil {
		t.Fatalf("finishBitmap: %v", err)
	}
	return out.String()
}

func TestParsePNG_16Bit(t *testing.T) {
	const w, h = 5, 7
	// want[y] holds the color samples and wantAlpha[y] the alpha samples
	// the PDF reader should end up with, big-endian 16 bit each.
	type testcase struct {
		name       string
		png        []byte
		colorspace string
		colors     int
		want       [][]byte
		wantAlpha  [][]byte
	}
	be := func(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }

	gray := image.NewGray16(image.Rect(0, 0, w, h))
	rgb := image.NewRGBA64(image.Rect(0, 0, w, h))
	rgba := image.NewNRGBA64(image.Rect(0, 0, w, h))
	var grayWant, rgbWant, rgbaWant, rgbaAlpha, gaRows, gaWant, gaAlpha [][]byte
	for y := range h {
		var g, c, ca, a, garow, gac, gaa []byte
		for x := range w {
			gv := gradient16(x, y, 0)
			gray.SetGray16(x, y, color.Gray16{Y: gv})
			g = append(g, be(gv)...)

			r, gg, b := gradient16(x, y, 0), gradient16(x, y, 1), gradient16(x, y, 2)
			rgb.SetRGBA64(x, y, color.RGBA64{R: r, G: gg, B: b, A: 0xffff})
			c = append(c, be(r)...)
			c = append(c, be(gg)...)
			c = append(c, be(b)...)

			av := uint16(0x0101 + x*12345 + y*321)
			rgba.SetNRGBA64(x, y, color.NRGBA64{R: r, G: gg, B: b, A: av})
			ca = append(ca, be(r)...)
			ca = append(ca, be(gg)...)
			ca = append(ca, be(b)...)
			a = append(a, be(av)...)

			garow = append(garow, be(gv)...)
			garow = append(garow, be(av)...)
			gac = append(gac, be(gv)...)
			gaa = append(gaa, be(av)...)
		}
		grayWant = append(grayWant, g)
		rgbWant = append(rgbWant, c)
		rgbaWant = append(rgbaWant, ca)
		rgbaAlpha = append(rgbaAlpha, a)
		gaRows = append(gaRows, garow)
		gaWant = append(gaWant, gac)
		gaAlpha = append(gaAlpha, gaa)
	}

	for _, tc := range []testcase{
		{name: "gray", png: newPNGBytes(t, gray), colorspace: "DeviceGray", colors: 1, want: grayWant},
		{name: "gray+alpha", png: buildPNG(t, w, h, 16, colGrayScaleWithAlpha, gaRows, nil), colorspace: "DeviceGray", colors: 1, want: gaWant, wantAlpha: gaAlpha},
		{name: "rgb", png: newPNGBytes(t, rgb), colorspace: "DeviceRGB", colors: 3, want: rgbWant},
		{name: "rgba", png: newPNGBytes(t, rgba), colorspace: "DeviceRGB", colors: 3, want: rgbaWant, wantAlpha: rgbaAlpha},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.png[24] != 16 {
				t.Fatalf("test PNG has bit depth %d, want 16", tc.png[24])
			}
			imgf := &Imagefile{r: newReader(tc.png)}
			if err := imgf.parsePNG(); err != nil {
				t.Fatalf("parsePNG: %v", err)
			}
			if imgf.colorspace != tc.colorspace {
				t.Errorf("colorspace=%q, want %q", imgf.colorspace, tc.colorspace)
			}
			if imgf.bitsPerComponent != "16" {
				t.Errorf("bitsPerComponent=%q, want \"16\"", imgf.bitsPerComponent)
			}
			if v := imgf.decodeParms["BitsPerComponent"]; v != "16" {
				t.Errorf("decodeParms.BitsPerComponent=%v, want 16", v)
			}
			if v := imgf.decodeParms["Columns"]; v != w {
				t.Errorf("decodeParms.Columns=%v, want %d", v, w)
			}
			if tc.colors == 3 {
				if v := imgf.decodeParms["Colors"]; v != 3 {
					t.Errorf("decodeParms.Colors=%v, want 3", v)
				}
			} else if _, ok := imgf.decodeParms["Colors"]; ok {
				t.Errorf("decodeParms.Colors set for DeviceGray")
			}

			got := pngUnfilter(t, inflate(t, imgf.data), tc.colors*2*w, h, tc.colors*2)
			for y := range h {
				if !bytes.Equal(got[y], tc.want[y]) {
					t.Fatalf("color row %d = % x, want % x", y, got[y], tc.want[y])
				}
			}

			pdf := writePDF(t, imgf)
			if !strings.Contains(pdf, "/BitsPerComponent 16") {
				t.Errorf("no /BitsPerComponent 16 in\n%s", pdf)
			}

			if tc.wantAlpha == nil {
				if haveSMask(imgf) {
					t.Fatalf("unexpected smask")
				}
				if strings.Contains(pdf, "/SMask") {
					t.Errorf("unexpected /SMask in\n%s", pdf)
				}
				return
			}
			if v := imgf.decodeParmsSmask["BitsPerComponent"]; v != "16" {
				t.Errorf("decodeParmsSmask.BitsPerComponent=%v, want 16", v)
			}
			if v := imgf.decodeParmsSmask["Colors"]; v != 1 {
				t.Errorf("decodeParmsSmask.Colors=%v, want 1", v)
			}
			if v := imgf.decodeParmsSmask["Columns"]; v != w {
				t.Errorf("decodeParmsSmask.Columns=%v, want %d", v, w)
			}
			gotAlpha := pngUnfilter(t, imgf.smask, 2*w, h, 2)
			for y := range h {
				if !bytes.Equal(gotAlpha[y], tc.wantAlpha[y]) {
					t.Fatalf("alpha row %d = % x, want % x", y, gotAlpha[y], tc.wantAlpha[y])
				}
			}
			if !strings.Contains(pdf, "/SMask ") {
				t.Errorf("no /SMask in\n%s", pdf)
			}
			// Both the image and its soft mask are 16 bit.
			if n := strings.Count(pdf, "/BitsPerComponent 16"); n < 4 {
				t.Errorf("/BitsPerComponent 16 appears %d times, want 4 (two dictionaries, two DecodeParms)\n%s", n, pdf)
			}
		})
	}
}

// TestParsePNG_8BitAlphaSplit guards the 8 bit split, which shares its code
// with the 16 bit one.
func TestParsePNG_8BitAlphaSplit(t *testing.T) {
	const w, h = 6, 5
	var gaRows, gaWant, gaAlpha [][]byte
	rgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	var rgbaWant, rgbaAlpha [][]byte
	for y := range h {
		var row, c, a, rc, ra []byte
		for x := range w {
			g, av := byte(x*40+y*3), byte(255-x*30-y)
			row = append(row, g, av)
			c = append(c, g)
			a = append(a, av)
			r, gg, b := byte(x*41), byte(y*50), byte(x*y*7)
			rgba.SetNRGBA(x, y, color.NRGBA{R: r, G: gg, B: b, A: av})
			rc = append(rc, r, gg, b)
			ra = append(ra, av)
		}
		gaRows = append(gaRows, row)
		gaWant = append(gaWant, c)
		gaAlpha = append(gaAlpha, a)
		rgbaWant = append(rgbaWant, rc)
		rgbaAlpha = append(rgbaAlpha, ra)
	}
	for _, tc := range []struct {
		name            string
		png             []byte
		colors          int
		want, wantAlpha [][]byte
	}{
		{"gray+alpha", buildPNG(t, w, h, 8, colGrayScaleWithAlpha, gaRows, nil), 1, gaWant, gaAlpha},
		{"rgba", newPNGBytes(t, rgba), 3, rgbaWant, rgbaAlpha},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imgf := &Imagefile{r: newReader(tc.png)}
			if err := imgf.parsePNG(); err != nil {
				t.Fatalf("parsePNG: %v", err)
			}
			if _, ok := imgf.decodeParmsSmask["BitsPerComponent"]; ok {
				t.Errorf("decodeParmsSmask.BitsPerComponent set for 8 bit")
			}
			got := pngUnfilter(t, inflate(t, imgf.data), tc.colors*w, h, tc.colors)
			gotAlpha := pngUnfilter(t, imgf.smask, w, h, 1)
			for y := range h {
				if !bytes.Equal(got[y], tc.want[y]) {
					t.Fatalf("color row %d = % x, want % x", y, got[y], tc.want[y])
				}
				if !bytes.Equal(gotAlpha[y], tc.wantAlpha[y]) {
					t.Fatalf("alpha row %d = % x, want % x", y, gotAlpha[y], tc.wantAlpha[y])
				}
			}
		})
	}
}

func TestParsePNG_TRNS(t *testing.T) {
	const w, h = 3, 2
	rows := func(bytesPerPixel int) [][]byte {
		r := make([][]byte, h)
		for y := range r {
			r[y] = make([]byte, w*bytesPerPixel)
		}
		return r
	}
	for _, tc := range []struct {
		name      string
		bitDepth  byte
		colorType byte
		trns      []byte
		want      []int
		wantMask  string
	}{
		{"gray 8", 8, colGrayScale, []byte{0x00, 0x80}, []int{0x80}, "/Mask [ 128 128 ]"},
		{"gray 16", 16, colGrayScale, []byte{0x12, 0x34}, []int{0x1234}, "/Mask [ 4660 4660 ]"},
		{"rgb 8", 8, colTrueColor, []byte{0x00, 0x10, 0x00, 0x20, 0x00, 0xff}, []int{0x10, 0x20, 0xff}, "/Mask [ 16 16 32 32 255 255 ]"},
		{"rgb 16", 16, colTrueColor, []byte{0xff, 0xfe, 0x01, 0x00, 0x00, 0x01}, []int{0xfffe, 0x0100, 0x0001}, "/Mask [ 65534 65534 256 256 1 1 ]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := buildPNG(t, w, h, tc.bitDepth, tc.colorType, rows(int(tc.bitDepth)/8*pngChannels(tc.colorType)), tc.trns)
			imgf := &Imagefile{r: newReader(b)}
			if err := imgf.parsePNG(); err != nil {
				t.Fatalf("parsePNG: %v", err)
			}
			if len(imgf.trns) != len(tc.want) {
				t.Fatalf("trns=%v, want %v", imgf.trns, tc.want)
			}
			for i := range tc.want {
				if imgf.trns[i] != tc.want[i] {
					t.Fatalf("trns=%v, want %v", imgf.trns, tc.want)
				}
			}
			pdf := writePDF(t, imgf)
			if !strings.Contains(pdf, tc.wantMask) {
				t.Errorf("no %q in\n%s", tc.wantMask, pdf)
			}
			if !strings.Contains(pdf, fmt.Sprintf("/BitsPerComponent %d", tc.bitDepth)) {
				t.Errorf("no /BitsPerComponent %d in\n%s", tc.bitDepth, pdf)
			}
		})
	}
}

func TestParsePNG_TRNSTooShort(t *testing.T) {
	b := buildPNG(t, 1, 1, 16, colTrueColor, [][]byte{make([]byte, 6)}, []byte{0, 1, 0, 2})
	imgf := &Imagefile{r: newReader(b)}
	if err := imgf.parsePNG(); err == nil {
		t.Fatalf("parsePNG accepted a tRNS chunk of 4 bytes for RGB")
	}
}

func TestParsePNG_BitDepthPerColorType(t *testing.T) {
	for _, tc := range []struct {
		colorType byte
		bitDepth  byte
		ok        bool
	}{
		{colGrayScale, 1, true},
		{colGrayScale, 16, true},
		{colGrayScale, 3, false},
		{colTrueColor, 4, false},
		{colTrueColor, 16, true},
		{colIndexedColor, 8, true},
		{colIndexedColor, 16, false},
		{colGrayScaleWithAlpha, 4, false},
		{colTrueColorWithAlpha, 32, false},
	} {
		t.Run(fmt.Sprintf("type %d depth %d", tc.colorType, tc.bitDepth), func(t *testing.T) {
			rowBytes := max(1, int(tc.bitDepth)*pngChannels(tc.colorType)/8)
			var trns []byte
			b := buildPNG(t, 1, 1, tc.bitDepth, tc.colorType, [][]byte{make([]byte, rowBytes)}, trns)
			if tc.colorType == colIndexedColor {
				b = insertPLTE(t, b)
			}
			imgf := &Imagefile{r: newReader(b)}
			err := imgf.parsePNG()
			if tc.ok && err != nil {
				t.Fatalf("parsePNG: %v", err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), "bit depth")) {
				t.Fatalf("parsePNG error = %v, want a bit depth error", err)
			}
		})
	}
}

// insertPLTE adds a one-entry palette after the IHDR chunk.
func insertPLTE(t *testing.T, b []byte) []byte {
	t.Helper()
	const afterIHDR = 8 + 4 + 4 + 13 + 4
	plte := []byte{0, 0, 0, 3, 'P', 'L', 'T', 'E', 0xff, 0, 0}
	plte = binary.BigEndian.AppendUint32(plte, crc32.ChecksumIEEE(plte[4:]))
	out := append([]byte{}, b[:afterIHDR]...)
	out = append(out, plte...)
	return append(out, b[afterIHDR:]...)
}
