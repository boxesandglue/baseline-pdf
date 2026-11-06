package pdf

import (
	"maps"
	"strings"
)

// Resources holds what a content stream refers to by name. A page and a form
// each have their own set.
type Resources struct {
	Faces  []*Face
	Images []*Imagefile
	// Forms maps a resource name (without the leading slash) to a form from
	// PDF.AddForm. The name is the host's choice, so that a renderer can
	// write "/<name> Do" before the form exists. Entries land in
	// /Resources/XObject.
	Forms map[Name]*Form
	// Patterns maps a per-page-unique resource name (without the leading
	// slash) to the indirect Pattern object returned by
	// PDF.WriteShadingPattern. Entries land in /Resources/Pattern; the
	// renderer (e.g. svgreader) refers to them as "/<name> scn" inside the
	// content stream.
	Patterns map[Name]*Object
	// ExtGStates maps a resource name (without the leading slash) to the
	// indirect graphics state parameter dictionary returned by
	// PDF.WriteExtGState. Entries land in /Resources/ExtGState; content
	// streams activate them with "/<name> gs".
	ExtGStates map[Name]*Object
}

// resourceDict builds the /Resources dictionary for r. The color spaces of
// the document are part of every dictionary, since a form does not see the
// ones of its page.
func (pw *PDF) resourceDict(r *Resources) Dict {
	res := Dict{}
	if len(r.Faces) > 0 {
		fnts := Dict{}
		for _, face := range r.Faces {
			fnts[Name(face.InternalName())] = face.fontobject.ObjectNumber.Ref()
		}
		res["Font"] = fnts
	}
	if len(pw.Colorspaces) > 0 || pw.DefaultRGB != 0 {
		colorspace := Dict{}

		for _, cs := range pw.Colorspaces {
			colorspace[Name(cs.ID)] = cs.Obj.Ref()
		}
		if pw.DefaultRGB != 0 {
			colorspace["DefaultRGB"] = pw.DefaultRGB.Ref()
		}
		res["ColorSpace"] = colorspace
	}
	if len(r.Images) > 0 || len(r.Forms) > 0 {
		xo := Dict{}
		for _, img := range r.Images {
			xo[Name(img.InternalName())] = img.imageobject.ObjectNumber.Ref()
		}
		for name, form := range r.Forms {
			xo[name] = form.content.ObjectNumber.Ref()
		}
		res["XObject"] = xo
	}
	// Shading patterns produced by WriteShadingPattern. Pattern names
	// are passed through verbatim; the SVG renderer reuses the same
	// names in the page content stream.
	if len(r.Patterns) > 0 {
		pat := Dict{}
		for name, obj := range r.Patterns {
			pat[name] = obj.ObjectNumber.Ref()
		}
		res["Pattern"] = pat
	}
	// Graphics state parameter dictionaries written by WriteExtGState.
	// Names are passed through verbatim; content streams activate them
	// with "/<name> gs".
	if len(r.ExtGStates) > 0 {
		gsDict := Dict{}
		for name, obj := range r.ExtGStates {
			gsDict[name] = obj.ObjectNumber.Ref()
		}
		res["ExtGState"] = gsDict
	}
	return res
}

// TransparencyGroup describes a /Group dictionary of subtype Transparency
// (ISO 32000-2 section 11.6.6). On a form it makes the content of the form one
// object for the compositing of the surrounding content, so a constant alpha
// applies to the result and not to each drawing operator.
type TransparencyGroup struct {
	// ColorSpace is the blending color space as it appears in the PDF, such
	// as "/DeviceCMYK" or a reference to an ICC based color space. An empty
	// string omits the entry.
	ColorSpace string
	Isolated   bool // /I, the group does not see its backdrop
	Knockout   bool // /K, later objects replace earlier ones in the group
}

func (g *TransparencyGroup) dict() Dict {
	d := Dict{"Type": "/Group", "S": "/Transparency"}
	if g.ColorSpace != "" {
		d["CS"] = g.ColorSpace
	}
	if g.Isolated {
		d["I"] = "true"
	}
	if g.Knockout {
		d["K"] = "true"
	}
	return d
}

// A Form is a Form XObject: a content stream with its own resources that a
// page, another form or a soft mask paints as one object. There is no /Matrix
// by default, so coordinates in the form are those of the content stream that
// paints it.
type Form struct {
	Resources
	Dict    Dict // Additional dictionary entries such as "/StructParents"
	BBox    [4]float64
	Group   *TransparencyGroup // nil for a form without a transparency group
	content *Object
	pw      *PDF
}

// AddForm makes a Form XObject from the content stream. Unlike AddPage it needs
// the clipping rectangle of the form. The form is written when the PDF is
// finished, so its resources can be set until then. Paint it by adding it to
// the Forms of the page and writing "/<name> Do".
func (pw *PDF) AddForm(content *Object, bbox [4]float64) *Form {
	content.ForceStream = true
	form := &Form{BBox: bbox, content: content, pw: pw}
	pw.forms = append(pw.forms, form)
	return form
}

// ObjectNumber returns the object number of the form. A structure element needs
// it for marked content in the form.
func (f *Form) ObjectNumber() Objectnumber {
	return f.content.ObjectNumber
}

func (f *Form) save() error {
	d := Dict{
		"Type":      "/XObject",
		"Subtype":   "/Form",
		"FormType":  "1",
		"BBox":      numberArray(f.BBox[:]),
		"Resources": f.pw.resourceDict(&f.Resources),
	}
	if f.Group != nil {
		d["Group"] = f.Group.dict()
	}
	maps.Copy(d, f.Dict)
	f.content.Dict(d)
	return f.content.Save()
}

// numberArray writes values as a PDF array.
func numberArray(values []float64) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmtPDFFloat(v)
	}
	return "[" + strings.Join(parts, " ") + "]"
}
