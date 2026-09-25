package pdf

import (
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/boxesandglue/textshape/ot"
)

// TestWidthsCFFNot1000Upem checks that /W is in 1/1000 of text space for a
// CFF font whose units per em are not 1000. The fixture has 4000 units per
// em; unscaled widths made every glyph advance four times too far.
func TestWidthsCFFNot1000Upem(t *testing.T) {
	const fontFile = "../boxesandglue/qa/fonts/upem/fonts/ArugulaLAB20231005-Regular.otf"
	if _, err := os.Stat(fontFile); err != nil {
		t.Skipf("font fixture missing: %v", err)
	}
	pw := NewPDFWriter(io.Discard)
	face, err := pw.LoadFace(fontFile, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := face.OTFace()
	if !f.IsCFF() || f.Upem() == 1000 {
		t.Fatalf("fixture must be CFF with upem != 1000, got CFF=%v upem=%d", f.IsCFF(), f.Upem())
	}
	gid := face.Codepoints([]rune{'A'})[0]
	w := widthsPDF(f, []ot.GlyphID{1}, map[ot.GlyphID]ot.GlyphID{1: ot.GlyphID(gid)})
	adv := float64(f.HorizontalAdvance(ot.GlyphID(gid))) * 1000 / float64(f.Upem())
	want := "[1[" + strconv.FormatFloat(adv, 'f', -1, 64) + "]]"
	if w != want {
		t.Errorf("widthsPDF = %s, want %s", w, want)
	}
}
