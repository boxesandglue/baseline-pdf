package pdf

import (
	"bytes"
	"strings"
	"testing"
)

func floatPtr(f float64) *float64 { return &f }

// TestExtGStateResourceName verifies the content-derived names: stable for
// equal contents, distinct for distinct contents, and free of dots (they
// are replaced to keep decompressed content streams readable).
func TestExtGStateResourceName(t *testing.T) {
	testdata := []struct {
		gs   ExtGState
		want Name
	}{
		{ExtGState{FillAlpha: floatPtr(0.4)}, "GSca0_4"},
		{ExtGState{StrokeAlpha: floatPtr(1)}, "GSCA1"},
		{ExtGState{FillAlpha: floatPtr(0.4), StrokeAlpha: floatPtr(0.75), BlendMode: "Multiply"}, "GSca0_4CA0_75BMMultiply"},
		{ExtGState{}, "GS"},
	}
	for _, td := range testdata {
		if got := td.gs.ResourceName(); got != td.want {
			t.Errorf("ResourceName() = %q, want %q", got, td.want)
		}
	}
}

// TestWriteExtGStateCaching verifies that equal parameter sets share one
// indirect object per document while different sets get their own.
func TestWriteExtGStateCaching(t *testing.T) {
	pw := NewPDFWriter(&bytes.Buffer{})
	a1, err := pw.WriteExtGState(ExtGState{FillAlpha: floatPtr(0.4)})
	if err != nil {
		t.Fatalf("WriteExtGState: %v", err)
	}
	a2, err := pw.WriteExtGState(ExtGState{FillAlpha: floatPtr(0.4)})
	if err != nil {
		t.Fatalf("WriteExtGState: %v", err)
	}
	if a1 != a2 {
		t.Error("equal ExtGStates should share one indirect object")
	}
	b, err := pw.WriteExtGState(ExtGState{FillAlpha: floatPtr(0.5)})
	if err != nil {
		t.Fatalf("WriteExtGState: %v", err)
	}
	if a1 == b {
		t.Error("different ExtGStates must not share an object")
	}
}

// TestExtGStateSerialization renders a one-page document with a registered
// ExtGState and checks the serialized file for the /ExtGState resource
// entry, the resource name and the parameter dictionary.
func TestExtGStateSerialization(t *testing.T) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	gs := ExtGState{FillAlpha: floatPtr(0.4), BlendMode: "Multiply"}
	obj, err := pw.WriteExtGState(gs)
	if err != nil {
		t.Fatalf("WriteExtGState: %v", err)
	}
	stream := pw.NewObject()
	stream.Data.WriteString("/GSca0_4BMMultiply gs 0 0 10 10 re f")
	if err = stream.Save(); err != nil {
		t.Fatalf("save content stream: %v", err)
	}
	page := pw.AddPage(stream, pw.NextObject())
	page.Width = 100
	page.Height = 100
	page.ExtGStates = map[Name]*Object{gs.ResourceName(): obj}
	if err = pw.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"/ExtGState", "/GSca0_4BMMultiply", "/ca 0.4", "/BM /Multiply"} {
		if !strings.Contains(out, want) {
			t.Errorf("serialized PDF misses %q", want)
		}
	}
}

// TestExtGStateOverprint checks that switching overprint off is written out
// and not mistaken for an absent entry.
func TestExtGStateOverprint(t *testing.T) {
	if OverprintOn.ResourceName() == OverprintOff.ResourceName() {
		t.Fatal("on and off need different names")
	}
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	on, err := pw.WriteExtGState(OverprintOn)
	if err != nil {
		t.Fatalf("WriteExtGState: %v", err)
	}
	off, err := pw.WriteExtGState(OverprintOff)
	if err != nil {
		t.Fatalf("WriteExtGState: %v", err)
	}
	for _, tc := range []struct {
		obj  *Object
		want Dict
	}{
		{on, Dict{"OP": "true", "op": "true", "OPM": "1"}},
		{off, Dict{"OP": "false", "op": "false", "OPM": "0"}},
	} {
		for k, v := range tc.want {
			if got := tc.obj.Dictionary[k]; got != v {
				t.Errorf("/%s = %v, want %v", k, got, v)
			}
		}
	}
}

func softMaskForm(pw *PDF, group bool) *Form {
	content := pw.NewObject()
	content.Data.WriteString("0.5 g 0 0 1 1 re f")
	form := pw.AddForm(content, [4]float64{0, 0, 1, 1})
	if group {
		form.Group = &TransparencyGroup{ColorSpace: "/DeviceGray"}
	}
	return form
}

// TestExtGStateSoftMask checks the dictionary and that the name tells masks
// apart, as equal names share one object.
func TestExtGStateSoftMask(t *testing.T) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	form := softMaskForm(pw, true)
	lum := ExtGState{SoftMask: &SoftMask{Group: form}}
	alpha := ExtGState{SoftMask: &SoftMask{Group: form, Alpha: true}}
	bc := ExtGState{SoftMask: &SoftMask{Group: form, Backdrop: []float64{0.5}}}
	none := ExtGState{SoftMask: &SoftMask{}}
	names := map[Name]bool{}
	for _, gs := range []ExtGState{lum, alpha, bc, none, {}} {
		names[gs.ResourceName()] = true
		if _, err := pw.WriteExtGState(gs); err != nil {
			t.Fatalf("WriteExtGState: %v", err)
		}
	}
	if len(names) != 5 {
		t.Errorf("resource names are not distinct: %v", names)
	}
	newTestPage(t, pw, "q Q")
	out := finishToString(t, pw, &buf)
	wantAll(t, out,
		"/Type /Mask",
		"/S /Luminosity",
		"/S /Alpha",
		"/G "+form.ObjectNumber().Ref(),
		"/BC [0.5]",
		"/SMask /None",
		"/CS /DeviceGray",
	)
}

// TestExtGStateSoftMaskNeedsGroup: a soft mask without a transparency group is
// not valid PDF, so it must not reach the file.
func TestExtGStateSoftMaskNeedsGroup(t *testing.T) {
	pw := NewPDFWriter(&bytes.Buffer{})
	form := softMaskForm(pw, false)
	if _, err := pw.WriteExtGState(ExtGState{SoftMask: &SoftMask{Group: form}}); err == nil {
		t.Error("expected an error for a form without a transparency group")
	}
}
