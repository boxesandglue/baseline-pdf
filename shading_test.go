package pdf

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestFmtPDFFloat(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		// IEEE-754 negative zero must be normalised to "0" by fmtPDFFloat.
		// Go's literal -0.0 collapses to +0.0; math.Copysign is the only way
		// to obtain a true negative zero here.
		{math.Copysign(0, -1), "0"},
		{1.5, "1.5"},
		{-1.5, "-1.5"},
		{0.5, "0.5"},
		{-0.5, "-0.5"},
		{1.234567, "1.234567"},
		{100, "100"},
		{123456.789, "123456.789"},
		// Values smaller than 0.5e-6 round to "0" at %.6f; "-0" is normalised.
		{0.0000001, "0"},
		{-0.0000001, "0"},
	}
	for _, tt := range tests {
		got := fmtPDFFloat(tt.in)
		if got != tt.want {
			t.Errorf("fmtPDFFloat(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// newTestPDF returns a fresh PDF writer that captures output in a buffer.
// NewPDFWriter resets the package-global font/image id counter, so tests in
// this file don't influence one another's object numbering.
func newTestPDF() (*PDF, *bytes.Buffer) {
	var buf bytes.Buffer
	pw := NewPDFWriter(&buf)
	return pw, &buf
}

func TestWriteFunctionType2(t *testing.T) {
	pw, buf := newTestPDF()
	f := Function{
		FunctionType: 2,
		Domain:       [2]float64{0, 1},
		C0:           [3]float64{1, 0, 0},
		C1:           [3]float64{0, 0, 1},
		N:            1,
	}
	obj, err := pw.writeFunction(f)
	if err != nil {
		t.Fatalf("writeFunction Type 2: %v", err)
	}
	if obj == nil {
		t.Fatal("writeFunction returned nil object")
	}
	out := buf.String()
	for _, want := range []string{
		"/FunctionType 2",
		"/Domain [0 1]",
		"/C0 [1 0 0]",
		"/C1 [0 0 1]",
		"/N 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Type 2 output missing %q\nfull output:\n%s", want, out)
		}
	}
	if got, want := strings.Count(out, "endobj"), 1; got != want {
		t.Errorf("expected %d endobj, got %d", want, got)
	}
}

func TestWriteFunctionType3Stitching(t *testing.T) {
	pw, buf := newTestPDF()
	f := Function{
		FunctionType: 3,
		Domain:       [2]float64{0, 1},
		SubFunctions: []Function{
			{FunctionType: 2, Domain: [2]float64{0, 1}, C0: [3]float64{1, 0, 0}, C1: [3]float64{0, 1, 0}, N: 1},
			{FunctionType: 2, Domain: [2]float64{0, 1}, C0: [3]float64{0, 1, 0}, C1: [3]float64{0, 0, 1}, N: 1},
		},
		Bounds: []float64{0.5},
		Encode: [][2]float64{{0, 1}, {0, 1}},
	}
	obj, err := pw.writeFunction(f)
	if err != nil {
		t.Fatalf("writeFunction Type 3: %v", err)
	}
	if obj == nil {
		t.Fatal("writeFunction returned nil object")
	}
	out := buf.String()
	for _, want := range []string{
		"/FunctionType 3",
		"/Functions [1 0 R 2 0 R]",
		"/Bounds [0.5]",
		"/Encode [0 1 0 1]",
		"/Domain [0 1]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Type 3 output missing %q\nfull output:\n%s", want, out)
		}
	}
	// Two Type 2 sub-functions plus the stitching parent must produce three indirect objects.
	if got, want := strings.Count(out, "endobj"), 3; got != want {
		t.Errorf("expected %d endobj markers, got %d\nfull output:\n%s", want, got, out)
	}
	// Stitching parent must be the third (last) object written so callers
	// reference the right one.
	if obj.ObjectNumber != 3 {
		t.Errorf("stitching object number = %d, want 3", obj.ObjectNumber)
	}
}

func TestWriteFunctionErrors(t *testing.T) {
	tests := []struct {
		name string
		f    Function
		want string
	}{
		{
			"unsupported type",
			Function{FunctionType: 4},
			"unsupported FunctionType",
		},
		{
			"stitching no sub-functions",
			Function{FunctionType: 3},
			"needs at least one sub-function",
		},
		{
			"stitching bounds mismatch",
			Function{
				FunctionType: 3,
				SubFunctions: []Function{
					{FunctionType: 2, N: 1},
					{FunctionType: 2, N: 1},
				},
				Bounds: nil, // expected 1
				Encode: [][2]float64{{0, 1}, {0, 1}},
			},
			"bounds",
		},
		{
			"stitching encode mismatch",
			Function{
				FunctionType: 3,
				SubFunctions: []Function{
					{FunctionType: 2, N: 1},
					{FunctionType: 2, N: 1},
				},
				Bounds: []float64{0.5},
				Encode: nil,
			},
			"encode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pw, _ := newTestPDF()
			_, err := pw.writeFunction(tt.f)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestWriteShadingPattern2Stop(t *testing.T) {
	pw, buf := newTestPDF()
	p := ShadingPattern{
		Shading: Shading{
			ShadingType: 2,
			ColorSpace:  "/DeviceRGB",
			Coords:      [4]float64{0, 0, 100, 0},
			Function: Function{
				FunctionType: 2,
				Domain:       [2]float64{0, 1},
				C0:           [3]float64{1, 0, 0},
				C1:           [3]float64{0, 0, 1},
				N:            1,
			},
		},
		Matrix: [6]float64{1, 0, 0, 1, 0, 0},
	}
	pat, err := pw.WriteShadingPattern(p)
	if err != nil {
		t.Fatalf("WriteShadingPattern: %v", err)
	}
	if pat == nil {
		t.Fatal("WriteShadingPattern returned nil")
	}
	out := buf.String()
	for _, want := range []string{
		// Function object
		"/FunctionType 2",
		"/C0 [1 0 0]",
		"/C1 [0 0 1]",
		// Shading dict
		"/ShadingType 2",
		"/ColorSpace /DeviceRGB",
		"/Coords [0 0 100 0]",
		"/Extend [false false]",
		// Pattern dict — /Type comes first by hashToString's special sort
		"/Type /Pattern",
		"/PatternType 2",
		"/Matrix [1 0 0 1 0 0]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
	// Three indirect objects: Function (#1), Shading (#2), Pattern (#3).
	if got, want := strings.Count(out, "endobj"), 3; got != want {
		t.Errorf("expected %d endobj markers, got %d\nfull output:\n%s", want, got, out)
	}
	if !strings.Contains(out, "/Function 1 0 R") {
		t.Errorf("Shading dict should reference Function via 1 0 R\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "/Shading 2 0 R") {
		t.Errorf("Pattern dict should reference Shading via 2 0 R\nfull output:\n%s", out)
	}
	if pat.ObjectNumber != 3 {
		t.Errorf("returned Pattern object number = %d, want 3", pat.ObjectNumber)
	}
}
