package pdf

import (
	"bytes"
	"os"
	"regexp"
	"testing"
)

// TestFontDescriptorInGlyphSpace checks that the font descriptor's metrics
// are in glyph space, 1/1000 of the em (PDF 32000-1 §9.8.1), whatever the
// font's units per em. Unscaled, a 2048-unit font declared an ascent of 1836
// instead of 896.
func TestFontDescriptorInGlyphSpace(t *testing.T) {
	const dir = "../boxesandglue/qa/fonts/upem/fonts/"
	tests := []struct {
		file string
		want map[string]string
	}{
		{"CrimsonPro-Regular2048.ttf", map[string]string{ // TrueType, 2048 units per em
			"Ascent": "896", "Descent": "-215", "CapHeight": "573", "XHeight": "415",
			"FontBBox": "[-107 -279 1153 961]",
		}},
		{"ArugulaLAB20231005-Regular.otf", map[string]string{ // CFF, 4000 units per em
			"Ascent": "975", "Descent": "-275", "CapHeight": "700", "XHeight": "525",
			"FontBBox": "[-12 -216 1018 940]",
		}},
		{"texgyreheros-regular.otf", map[string]string{ // CFF, 1000 units per em: unchanged
			"Ascent": "1148", "Descent": "-284", "CapHeight": "729", "XHeight": "524",
			"FontBBox": "[-529 -284 1353 1148]",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			fontFile := dir + tc.file
			if _, err := os.Stat(fontFile); err != nil {
				t.Skipf("font fixture missing: %v", err)
			}
			var buf bytes.Buffer
			pw := NewPDFWriter(&buf)
			face, err := pw.LoadFace(fontFile, 0)
			if err != nil {
				t.Fatal(err)
			}
			face.RegisterChars(face.Codepoints([]rune("Hx")))
			if err := face.finish(); err != nil {
				t.Fatal(err)
			}
			for key, want := range tc.want {
				m := regexp.MustCompile(`/` + key + ` (\[[^\]]*\]|-?\d+)`).FindSubmatch(buf.Bytes())
				if m == nil {
					t.Errorf("/%s not found in the font descriptor", key)
					continue
				}
				if got := string(m[1]); got != want {
					t.Errorf("/%s = %s, want %s", key, got, want)
				}
			}
		})
	}
}
