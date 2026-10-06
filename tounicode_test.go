package pdf

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"regexp"
	"testing"
)

// toUnicodeEntries writes a PDF with face, registering the glyphs of runes,
// and returns the bfchar entries of its ToUnicode CMap, CID to UTF-16BE hex.
func toUnicodeEntries(t *testing.T, fontFile string, runes []rune) map[string]string {
	t.Helper()
	if _, err := os.Stat(fontFile); err != nil {
		t.Skipf("font fixture missing: %v", err)
	}
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	face, err := pw.LoadFace(fontFile, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, gid := range face.Codepoints(runes) {
		face.RegisterGlyph(gid, string(runes[i]))
	}
	if err := face.CompactSubset(); err != nil {
		t.Fatal(err)
	}
	stream := pw.NewObject()
	if err := stream.Save(); err != nil {
		t.Fatal(err)
	}
	page := pw.AddPage(stream, pw.NextObject())
	page.Faces = []*Face{face}
	if err := pw.Finish(); err != nil {
		t.Fatal(err)
	}
	// The CMap stream may be compressed; inflate every stream and keep the
	// one with the bfchar list.
	out := buf.Bytes()
	var cmap []byte
	for _, m := range regexp.MustCompile(`(?s)stream\r?\n(.*?)endstream`).FindAllSubmatch(out, -1) {
		data := m[1]
		if r, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
			if inflated, err := io.ReadAll(r); err == nil {
				data = inflated
			}
		}
		if bytes.Contains(data, []byte("beginbfchar")) {
			cmap = data
		}
	}
	if cmap == nil {
		t.Fatal("no ToUnicode CMap in the output")
	}
	entries := map[string]string{}
	for _, m := range regexp.MustCompile(`<([0-9A-F]{4})><([0-9A-F]+)>`).FindAllSubmatch(cmap, -1) {
		entries[string(m[1])] = string(m[2])
	}
	return entries
}

// A glyph that is in the subset only as a component of a composite glyph
// never appears in a content stream and gets no ToUnicode entry: Crimson Pro
// builds "ä" from "a" and U+0308.
func TestToUnicodeSkipsCompositeComponents(t *testing.T) {
	const crimson = "../boxesandglue/qa/fonts/upem/fonts/CrimsonPro-Regular.ttf"
	values := func(m map[string]string) map[string]bool {
		ret := map[string]bool{}
		for _, v := range m {
			ret[v] = true
		}
		return ret
	}

	t.Run("only the composite is used", func(t *testing.T) {
		got := toUnicodeEntries(t, crimson, []rune{'ä'})
		if len(got) != 2 || got["0000"] != "FFFD" || !values(got)["00E4"] {
			t.Errorf("entries %v, want .notdef and U+00E4 only", got)
		}
	})
	t.Run("a component used on its own keeps its entry", func(t *testing.T) {
		got := toUnicodeEntries(t, crimson, []rune{'ä', 'a'})
		v := values(got)
		if len(got) != 3 || !v["00E4"] || !v["0061"] || v["0308"] {
			t.Errorf("entries %v, want .notdef, U+00E4 and U+0061", got)
		}
	})
}
