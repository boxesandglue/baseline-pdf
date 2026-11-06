package pdf

import (
	"strconv"
	"strings"
)

// ExtGState describes a graphics state parameter dictionary (ISO 32000-2
// section 8.4.5). Only the entries needed so far are modeled; a nil pointer
// or the empty string means "entry absent".
type ExtGState struct {
	FillAlpha   *float64 // /ca, constant alpha for nonstroking (fill) operations
	StrokeAlpha *float64 // /CA, constant alpha for stroking operations
	BlendMode   string   // /BM blend mode name such as "Multiply"; empty omits the entry

	StrokeOverprint *bool // /OP, overprint for stroking operations
	FillOverprint   *bool // /op, overprint for nonstroking (fill) operations
	OverprintMode   *int  // /OPM, 1 leaves the plates of zero CMYK components alone
}

// Graphics states to switch overprinting on and off. With mode 1 a CMYK color
// prints only on the plates of its nonzero components: a 100% black prints on
// the black plate and keeps the cyan, magenta and yellow below it. Separation
// colors print on their own plate in either mode.
var (
	OverprintOn  = overprintState(true, 1)
	OverprintOff = overprintState(false, 0)
)

func overprintState(on bool, mode int) ExtGState {
	return ExtGState{StrokeOverprint: &on, FillOverprint: &on, OverprintMode: &mode}
}

// fmtGSComponent renders a float for use inside a PDF name. Dots are not
// syntax errors in names, but replacing them keeps the names easy to read
// in a decompressed content stream ("GSca0_4" for fill alpha 0.4).
func fmtGSComponent(f float64) string {
	return strings.ReplaceAll(strconv.FormatFloat(f, 'f', -1, 64), ".", "_")
}

// ResourceName derives a stable resource name from the contents, so equal
// parameter sets share one name (and, via WriteExtGState's cache, one
// indirect object) document-wide. Content streams can therefore reference
// the state by name ("/<name> gs") before the object is materialised in the
// page's /Resources/ExtGState dictionary.
func (gs ExtGState) ResourceName() Name {
	var sb strings.Builder
	sb.WriteString("GS")
	if gs.FillAlpha != nil {
		sb.WriteString("ca")
		sb.WriteString(fmtGSComponent(*gs.FillAlpha))
	}
	if gs.StrokeAlpha != nil {
		sb.WriteString("CA")
		sb.WriteString(fmtGSComponent(*gs.StrokeAlpha))
	}
	if gs.BlendMode != "" {
		sb.WriteString("BM")
		sb.WriteString(gs.BlendMode)
	}
	if gs.StrokeOverprint != nil {
		sb.WriteString("OP")
		sb.WriteString(strconv.FormatBool(*gs.StrokeOverprint))
	}
	if gs.FillOverprint != nil {
		sb.WriteString("op")
		sb.WriteString(strconv.FormatBool(*gs.FillOverprint))
	}
	if gs.OverprintMode != nil {
		sb.WriteString("OPM")
		sb.WriteString(strconv.Itoa(*gs.OverprintMode))
	}
	return Name(sb.String())
}

// WriteExtGState serialises gs as an indirect object and returns it, so
// callers can reference it from a Page's /Resources/ExtGState entry. Equal
// parameter sets are written only once per PDF: the object is cached under
// its ResourceName and shared between pages.
func (pw *PDF) WriteExtGState(gs ExtGState) (*Object, error) {
	name := gs.ResourceName()
	if obj, ok := pw.extGStates[name]; ok {
		return obj, nil
	}
	d := Dict{"Type": "/ExtGState"}
	if gs.FillAlpha != nil {
		d["ca"] = fmtPDFFloat(*gs.FillAlpha)
	}
	if gs.StrokeAlpha != nil {
		d["CA"] = fmtPDFFloat(*gs.StrokeAlpha)
	}
	if gs.BlendMode != "" {
		d["BM"] = "/" + gs.BlendMode
	}
	if gs.StrokeOverprint != nil {
		d["OP"] = strconv.FormatBool(*gs.StrokeOverprint)
	}
	if gs.FillOverprint != nil {
		d["op"] = strconv.FormatBool(*gs.FillOverprint)
	}
	if gs.OverprintMode != nil {
		d["OPM"] = strconv.Itoa(*gs.OverprintMode)
	}
	obj := pw.NewObject()
	obj.Dictionary = d
	if err := obj.Save(); err != nil {
		return nil, err
	}
	if pw.extGStates == nil {
		pw.extGStates = make(map[Name]*Object)
	}
	pw.extGStates[name] = obj
	return obj, nil
}
