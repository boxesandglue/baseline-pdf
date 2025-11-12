package pdf

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
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
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
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
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
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

			// Sanity: decodeParms must advertise PNG predictor & columns.
			if v, ok := imgf.decodeParms["Predictor"]; !ok || v != 15 {
				t.Errorf("decodeParms.Predictor=%v, want 15", v)
			}
			if v, ok := imgf.decodeParms["Columns"]; !ok || v != imgf.W {
				t.Errorf("decodeParms.Columns=%v, want %d", v, imgf.W)
			}
			// For RGB images, Colors=3 should be set.
			if imgf.colorspace == "DeviceRGB" {
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
