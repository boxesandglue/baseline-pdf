package pdf

import (
	"bytes"
	"strings"
	"testing"
)

func newTestPage(t *testing.T, pw *PDF, content string) *Page {
	t.Helper()
	obj := pw.NewObject()
	obj.Data.WriteString(content)
	return pw.AddPage(obj, 0)
}

func finishToString(t *testing.T, pw *PDF, buf *bytes.Buffer) string {
	t.Helper()
	pw.DefaultPageWidth = 100
	pw.DefaultPageHeight = 100
	if err := pw.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return buf.String()
}

func wantAll(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output does not contain %q", w)
		}
	}
}

// TestFormOnPage writes a form with a transparency group and paints it.
func TestFormOnPage(t *testing.T) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	content := pw.NewObject()
	content.Data.WriteString("1 0 0 rg 0 0 10 10 re f")
	form := pw.AddForm(content, [4]float64{0, 0, 10.5, 20})
	form.Group = &TransparencyGroup{ColorSpace: "/DeviceCMYK", Isolated: true}
	form.Dict = Dict{"StructParents": "3"}

	page := newTestPage(t, pw, form.InternalName()+" Do")
	page.Forms = append(page.Forms, form)
	out := finishToString(t, pw, &buf)

	wantAll(t, out,
		"/Subtype /Form",
		"/FormType 1",
		"/BBox [0 0 10.5 20]",
		"/S /Transparency",
		"/CS /DeviceCMYK",
		"/I true",
		"/StructParents 3",
		"/XObject <<",
		form.InternalName()+" "+form.ObjectNumber().Ref(),
		"1 0 0 rg 0 0 10 10 re f",
	)
	if strings.Contains(out, "/K true") {
		t.Error("the group is not a knockout group")
	}
}

// TestFormWithoutGroup leaves out /Group and still writes /Resources, which a
// form must not inherit from its page.
func TestFormWithoutGroup(t *testing.T) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	content := pw.NewObject()
	content.Data.WriteString("q Q")
	form := pw.AddForm(content, [4]float64{0, 0, 1, 1})
	page := newTestPage(t, pw, form.InternalName()+" Do")
	page.Forms = append(page.Forms, form)
	out := finishToString(t, pw, &buf)

	if strings.Contains(out, "/Group") {
		t.Error("a form without a group must not have /Group")
	}
	wantAll(t, out, "/Resources <<")
}

// TestFormSeesDocumentColorspaces: a color space of the document has to be in
// the resources of the form, it does not see the ones of its page.
func TestFormSeesDocumentColorspaces(t *testing.T) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	pw.Colorspaces = []*Separation{{ID: "CS1", Obj: pw.NextObject()}}
	pw.DefaultRGB = pw.NextObject()
	spot := pw.Colorspaces[0].Obj.Ref()
	def := pw.DefaultRGB.Ref()
	content := pw.NewObject()
	content.Data.WriteString("q Q")
	pw.AddForm(content, [4]float64{0, 0, 1, 1})
	newTestPage(t, pw, "q Q")
	out := finishToString(t, pw, &buf)

	if got := strings.Count(out, "/CS1 "+spot); got != 2 {
		t.Errorf("spot color in %d resource dictionaries, want 2", got)
	}
	if got := strings.Count(out, "/DefaultRGB "+def); got != 2 {
		t.Errorf("DefaultRGB in %d resource dictionaries, want 2", got)
	}
}

// TestEmptyFormIsAStream: an element without content can give an empty form,
// which still has to be a stream.
func TestEmptyFormIsAStream(t *testing.T) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	form := pw.AddForm(pw.NewObject(), [4]float64{0, 0, 1, 1})
	page := newTestPage(t, pw, "q Q")
	page.Forms = append(page.Forms, form)
	out := finishToString(t, pw, &buf)

	wantAll(t, out, "/Length 0", "stream\n\nendstream")
}

// TestFontOnlyInForm embeds a font that no page uses directly.
func TestFontOnlyInForm(t *testing.T) {
	const fontFile = "../boxesandglue/qa/fonts/upem/fonts/CrimsonPro-Regular.ttf"
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	face, err := pw.LoadFace(fontFile, 0)
	if err != nil {
		t.Skipf("font not available: %v", err)
	}
	face.RegisterCodepoint(face.Codepoint('A'))
	content := pw.NewObject()
	content.Data.WriteString("BT " + face.InternalName() + " 10 Tf (A) Tj ET")
	form := pw.AddForm(content, [4]float64{0, 0, 100, 100})
	form.Faces = []*Face{face}
	page := newTestPage(t, pw, form.InternalName()+" Do")
	page.Forms = append(page.Forms, form)
	out := finishToString(t, pw, &buf)

	wantAll(t, out, "/FontFile2", "/Font <<", face.InternalName()+" "+face.fontobject.ObjectNumber.Ref())
	// Only the form refers to the font, so a missing face would leave a
	// dangling reference: the font object has to be in the file.
	wantAll(t, out, face.fontobject.ObjectNumber.String()+" 0 obj")
}

// TestPageGroup writes the transparency group of a page.
func TestPageGroup(t *testing.T) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	page := newTestPage(t, pw, "q Q")
	page.Group = &TransparencyGroup{ColorSpace: "/DeviceCMYK"}
	out := finishToString(t, pw, &buf)

	wantAll(t, out, "/Group <<", "/S /Transparency", "/CS /DeviceCMYK")
	if strings.Contains(out, "/I true") {
		t.Error("the group is not isolated")
	}
}

// TestNoFormsNoChange: forms must not move any object of a document without
// them.
func TestNoFormsNoChange(t *testing.T) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	newTestPage(t, pw, "q Q")
	out := finishToString(t, pw, &buf)
	if strings.Contains(out, "/Group") || strings.Contains(out, "/XObject") {
		t.Errorf("unexpected entries in\n%s", out)
	}
}
