package pdf

import (
	"fmt"
	"strings"
)

// Function describes a PDF Function (Section 7.10 of ISO 32000-1).
//
// Two FunctionTypes are supported here:
//
//   - FunctionType 2 (exponential interpolation) — used for a two-stop linear
//     gradient. C0/C1 are the RGB endpoints in 0..1 space; N=1 yields linear
//     interpolation, the only mode used for SVG.
//
//   - FunctionType 3 (stitching) — chains multiple Type 2 sub-functions for
//     gradients with three or more stops. Bounds partition the [0,1] domain
//     and Encode remaps each sub-domain onto its sub-function's [0,1].
type Function struct {
	FunctionType int        // 2 or 3
	Domain       [2]float64 // typically [0 1]
	// FunctionType 2 fields
	C0 [3]float64 // start RGB color (0..1)
	C1 [3]float64 // end RGB color (0..1)
	N  float64    // exponent; 1 for linear interpolation
	// FunctionType 3 fields
	SubFunctions []Function   // each must be FunctionType 2
	Bounds       []float64    // len = len(SubFunctions)-1
	Encode       [][2]float64 // len = len(SubFunctions)
}

// Shading describes a PDF Shading Dictionary (Section 8.7.4.5).
// Only Type 2 (axial) is implemented; Type 3 (radial) is intentionally left
// out until there is a need.
type Shading struct {
	ShadingType int        // 2 = axial (linear)
	ColorSpace  Name       // "/DeviceRGB"
	Coords      [4]float64 // x1 y1 x2 y2 in pattern space
	Function    Function
	Extend      [2]bool // [extendStart extendEnd]
}

// ShadingPattern describes a PDF Pattern Dictionary of type 2 (Shading
// Pattern, Section 8.7.3.3). The matrix maps pattern space to default
// coordinate space, mirroring SVG's gradientTransform.
type ShadingPattern struct {
	Shading Shading
	Matrix  [6]float64 // PDF order: a b c d e f
}

// WriteShadingPattern serialises a ShadingPattern as three chained indirect
// PDF objects (Function, Shading, Pattern) and returns the Pattern object so
// callers can reference it from a Page's /Resources/Pattern entry.
//
// All sub-functions of a stitching function become individual indirect
// objects too, since /Functions in a Type 3 Function dictionary is an array
// of indirect references.
func (pw *PDF) WriteShadingPattern(p ShadingPattern) (*Object, error) {
	funcObj, err := pw.writeFunction(p.Shading.Function)
	if err != nil {
		return nil, err
	}

	cs := p.Shading.ColorSpace
	if cs == "" {
		cs = "/DeviceRGB"
	}
	extendStart := "false"
	if p.Shading.Extend[0] {
		extendStart = "true"
	}
	extendEnd := "false"
	if p.Shading.Extend[1] {
		extendEnd = "true"
	}
	shadingObj := pw.NewObject()
	shadingObj.Dictionary = Dict{
		"ShadingType": fmt.Sprintf("%d", p.Shading.ShadingType),
		"ColorSpace":  string(cs),
		"Coords": fmt.Sprintf("[%s %s %s %s]",
			fmtPDFFloat(p.Shading.Coords[0]),
			fmtPDFFloat(p.Shading.Coords[1]),
			fmtPDFFloat(p.Shading.Coords[2]),
			fmtPDFFloat(p.Shading.Coords[3])),
		"Function": funcObj.ObjectNumber.Ref(),
		"Extend":   fmt.Sprintf("[%s %s]", extendStart, extendEnd),
	}
	if err := shadingObj.Save(); err != nil {
		return nil, err
	}

	patternObj := pw.NewObject()
	patternObj.Dictionary = Dict{
		"Type":        "/Pattern",
		"PatternType": "2",
		"Shading":     shadingObj.ObjectNumber.Ref(),
		"Matrix": fmt.Sprintf("[%s %s %s %s %s %s]",
			fmtPDFFloat(p.Matrix[0]), fmtPDFFloat(p.Matrix[1]),
			fmtPDFFloat(p.Matrix[2]), fmtPDFFloat(p.Matrix[3]),
			fmtPDFFloat(p.Matrix[4]), fmtPDFFloat(p.Matrix[5])),
	}
	if err := patternObj.Save(); err != nil {
		return nil, err
	}
	return patternObj, nil
}

// writeFunction emits a single Function object and recurses into sub-
// functions for the stitching case. Returns the indirect object so the
// caller can stash its reference.
func (pw *PDF) writeFunction(f Function) (*Object, error) {
	switch f.FunctionType {
	case 2:
		obj := pw.NewObject()
		obj.Dictionary = Dict{
			"FunctionType": "2",
			"Domain": fmt.Sprintf("[%s %s]",
				fmtPDFFloat(f.Domain[0]), fmtPDFFloat(f.Domain[1])),
			"C0": fmt.Sprintf("[%s %s %s]",
				fmtPDFFloat(f.C0[0]), fmtPDFFloat(f.C0[1]), fmtPDFFloat(f.C0[2])),
			"C1": fmt.Sprintf("[%s %s %s]",
				fmtPDFFloat(f.C1[0]), fmtPDFFloat(f.C1[1]), fmtPDFFloat(f.C1[2])),
			"N": fmtPDFFloat(f.N),
		}
		if err := obj.Save(); err != nil {
			return nil, err
		}
		return obj, nil
	case 3:
		if len(f.SubFunctions) == 0 {
			return nil, fmt.Errorf("pdf: stitching function needs at least one sub-function")
		}
		if len(f.Bounds) != len(f.SubFunctions)-1 {
			return nil, fmt.Errorf("pdf: stitching function bounds (%d) must be sub-functions-1 (%d)",
				len(f.Bounds), len(f.SubFunctions)-1)
		}
		if len(f.Encode) != len(f.SubFunctions) {
			return nil, fmt.Errorf("pdf: stitching function encode (%d) must match sub-functions (%d)",
				len(f.Encode), len(f.SubFunctions))
		}
		subRefs := make([]string, len(f.SubFunctions))
		for i, sub := range f.SubFunctions {
			subObj, err := pw.writeFunction(sub)
			if err != nil {
				return nil, err
			}
			subRefs[i] = subObj.ObjectNumber.Ref()
		}
		boundsStrs := make([]string, len(f.Bounds))
		for i, b := range f.Bounds {
			boundsStrs[i] = fmtPDFFloat(b)
		}
		encodeStrs := make([]string, 0, len(f.Encode)*2)
		for _, e := range f.Encode {
			encodeStrs = append(encodeStrs, fmtPDFFloat(e[0]), fmtPDFFloat(e[1]))
		}
		obj := pw.NewObject()
		obj.Dictionary = Dict{
			"FunctionType": "3",
			"Domain": fmt.Sprintf("[%s %s]",
				fmtPDFFloat(f.Domain[0]), fmtPDFFloat(f.Domain[1])),
			"Functions": "[" + strings.Join(subRefs, " ") + "]",
			"Bounds":    "[" + strings.Join(boundsStrs, " ") + "]",
			"Encode":    "[" + strings.Join(encodeStrs, " ") + "]",
		}
		if err := obj.Save(); err != nil {
			return nil, err
		}
		return obj, nil
	default:
		return nil, fmt.Errorf("pdf: unsupported FunctionType %d", f.FunctionType)
	}
}

// fmtPDFFloat formats a float64 for PDF: max 6 decimals, trailing zeros
// trimmed, "-0" normalised. Keeps PDF byte output stable when small floats
// pile up in a Shading dict.
func fmtPDFFloat(f float64) string {
	s := fmt.Sprintf("%.6f", f)
	if strings.ContainsRune(s, '.') {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}
