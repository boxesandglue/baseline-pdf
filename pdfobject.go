package pdf

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// unicodeToPDFDocEncoding maps Unicode codepoints in the 0x80–0x9F range
// of PDFDocEncoding to their byte values. Codepoints U+00A0–U+00FF map
// directly to bytes 0xA0–0xFF and need no table entry.
// Reference: PDF 32000-1:2008, Table D.2.
var unicodeToPDFDocEncoding = map[rune]byte{
	0x2022: 0x80, // bullet
	0x2020: 0x81, // dagger
	0x2021: 0x82, // double dagger
	0x2026: 0x83, // horizontal ellipsis
	0x2014: 0x84, // em dash
	0x2013: 0x85, // en dash
	0x0192: 0x86, // latin small f with hook
	0x2044: 0x87, // fraction slash
	0x2039: 0x88, // single left-pointing angle quotation mark
	0x203A: 0x89, // single right-pointing angle quotation mark
	0x2212: 0x8A, // minus sign
	0x2030: 0x8B, // per mille sign
	0x201E: 0x8C, // double low-9 quotation mark „
	0x201C: 0x8D, // left double quotation mark "
	0x201D: 0x8E, // right double quotation mark "
	0x2018: 0x8F, // left single quotation mark '
	0x2019: 0x90, // right single quotation mark '
	0x201A: 0x91, // single low-9 quotation mark ‚
	0x2122: 0x92, // trade mark sign
	0xFB01: 0x93, // fi ligature
	0xFB02: 0x94, // fl ligature
	0x0141: 0x95, // latin capital L with stroke
	0x0152: 0x96, // latin capital ligature OE
	0x0160: 0x97, // latin capital S with caron
	0x0178: 0x98, // latin capital Y with diaeresis
	0x017D: 0x99, // latin capital Z with caron
	0x0131: 0x9A, // latin small dotless i
	0x0142: 0x9B, // latin small l with stroke
	0x0153: 0x9C, // latin small ligature oe
	0x0161: 0x9D, // latin small s with caron
	0x017E: 0x9E, // latin small z with caron
	0x20AC: 0xA0, // euro sign (mapped to 0xA0 in PDFDocEncoding)
}

// NameDest represents a named PDF destination. The origin of X and Y are in the
// top left corner and expressed in DTP points.
type NameDest struct {
	Name             String
	PageObjectnumber Objectnumber
	X                float64
	Y                float64
	objectnumber     Objectnumber
}

// NameTreeData is a map of strings to object numbers which is sorted by key and
// converted to an array when written to the PDF. It is suitable for use in a
// name tree object.
type NameTreeData map[String]Objectnumber

// String is a string that gets automatically converted to (...) or
// hexadecimal form when placed in the PDF.
type String string

// stringToPDF returns an escaped string suitable to be used as a PDF object.
// It uses PDFDocEncoding (parenthesized literal) when all characters are
// representable, and falls back to UTF-16BE hex encoding otherwise.
func stringToPDF(str string) string {
	// Check if the string can be encoded in PDFDocEncoding.
	canEncode := true
	for _, r := range str {
		if r <= 0x7F {
			continue
		}
		if r >= 0x00A1 && r <= 0x00FF {
			// Latin-1 supplement (excluding U+00A0 which is used for €)
			continue
		}
		if _, ok := unicodeToPDFDocEncoding[r]; ok {
			continue
		}
		canEncode = false
		break
	}

	var out strings.Builder
	if canEncode {
		out.WriteRune('(')
		for _, r := range str {
			switch {
			case r == '(' || r == ')' || r == '\\':
				out.WriteRune('\\')
				out.WriteRune(r)
			case r == '\n':
				out.WriteString(`\n`)
			case r == '\r':
				out.WriteString(`\r`)
			case r == '\t':
				out.WriteString(`\t`)
			case r == '\b':
				out.WriteString(`\b`)
			case r <= 0x7F:
				out.WriteRune(r)
			case r >= 0x00A1 && r <= 0x00FF:
				out.WriteByte(byte(r))
			default:
				out.WriteByte(unicodeToPDFDocEncoding[r])
			}
		}
		out.WriteRune(')')
		return out.String()
	}
	out.WriteString("<feff")
	for _, i := range utf16.Encode([]rune(str)) {
		writeHex4(&out, i)
	}
	out.WriteRune('>')
	return out.String()
}

// Serialize returns a string representation of the item as it may appear in the
// PDF file. Arrays are written with square brackets, Dicts with double angle
// brackets, Strings (PDF strings) with parentheses or single angle brackets,
// depending on the contents and all other objects with their respective
// String() method.
func Serialize(item any) string {
	return serializeLevel(item, 0)
}

func serializeLevel(item any, level int) string {
	switch t := item.(type) {
	case string:
		return t
	case Array:
		return arrayToString(t)
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case Dict:
		return hashToString(t, level+1)
	case String:
		return stringToPDF(string(t))
	case NameTreeData:
		// sort by key
		var keys []string
		for k := range t {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		var out strings.Builder
		out.WriteString("[ ")
		for _, k := range keys {
			out.WriteString(stringToPDF(k))
			out.WriteByte(' ')
			out.WriteString(t[String(k)].Ref())
			out.WriteByte(' ')
		}
		out.WriteString("]")
		return out.String()
	case Objectnumber:
		return t.Ref()
	default:
		return fmt.Sprintf("%v", t)
	}
}

// arrayToString converts the objects in ary to a string including the opening
// and closing bracket.
func arrayToString(ary []any) string {
	ret := []string{"["}
	for _, elt := range ary {
		ret = append(ret, Serialize(elt))
	}
	ret = append(ret, "]")
	return strings.Join(ret, " ")
}

// FloatToPoint returns a string suitable as a PDF size value.
func FloatToPoint(in float64) string {
	const precisionFactor = 100.0
	rounded := math.Round(precisionFactor*in) / precisionFactor
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}

// Object has information about a specific PDF object
type Object struct {
	Data         *bytes.Buffer
	Dictionary   Dict
	pdfwriter    *PDF
	comment      string
	Array        []any
	ObjectNumber Objectnumber
	Raw          bool // Data holds everything between object number and endobj
	ForceStream  bool // Write stream even if Data is empty
	compress     bool // for streams
	saved        bool // set to true when object is written to the PDF file
}

// NewObjectWithNumber create a new PDF object and reserves an object
// number for it.
// The object is not written to the PDF until Save() is called.
func (pw *PDF) NewObjectWithNumber(objnum Objectnumber) *Object {
	obj := &Object{
		Data: &bytes.Buffer{},
	}
	obj.ObjectNumber = objnum
	obj.pdfwriter = pw
	return obj
}

// NewObject create a new PDF object and reserves an object
// number for it.
// The object is not written to the PDF until Save() is called.
func (pw *PDF) NewObject() *Object {
	obj := &Object{
		Data: &bytes.Buffer{},
	}
	obj.ObjectNumber = pw.NextObject()
	obj.pdfwriter = pw
	return obj
}

// SetCompression turns on stream compression if compresslevel > 0
func (obj *Object) SetCompression(compresslevel uint) {
	obj.compress = compresslevel > 0
}

// Save adds the PDF object to the main PDF file.
func (obj *Object) Save() error {
	// guard against multiple Save()
	if obj.saved {
		return nil
	}
	obj.saved = true
	if obj.comment != "" {
		if err := obj.pdfwriter.Print("\n% " + obj.comment); err != nil {
			return err
		}
	}

	if obj.Raw {
		err := obj.pdfwriter.startObject(obj.ObjectNumber)
		if err != nil {
			return err
		}
		n, err := obj.Data.WriteTo(obj.pdfwriter.outfile)
		if err != nil {
			return err
		}
		obj.pdfwriter.pos += n
		obj.pdfwriter.endObject()
		return nil
	}
	hasData := obj.Data.Len() > 0 || obj.ForceStream
	if hasData {
		if obj.Dictionary == nil {
			obj.Dictionary = Dict{}
		}
		obj.Dictionary["Length"] = strconv.Itoa(obj.Data.Len())

		if obj.compress {
			obj.Dictionary["Filter"] = "/FlateDecode"
			var b bytes.Buffer
			obj.pdfwriter.zlibWriter.Reset(&b)
			if _, err := obj.pdfwriter.zlibWriter.Write(obj.Data.Bytes()); err != nil {
				return err
			}
			obj.pdfwriter.zlibWriter.Close()
			obj.Dictionary["Length"] = strconv.Itoa(b.Len())
			obj.Dictionary["Length1"] = strconv.Itoa(obj.Data.Len())
			obj.Data = &b
		} else {
			obj.Dictionary["Length"] = strconv.Itoa(obj.Data.Len())
		}
	}

	obj.pdfwriter.startObject(obj.ObjectNumber)
	if len(obj.Dictionary) > 0 {
		n, err := fmt.Fprint(obj.pdfwriter.outfile, hashToString(obj.Dictionary, 0))
		if err != nil {
			return err
		}
		obj.pdfwriter.pos += int64(n)
	} else if len(obj.Array) > 0 {
		n, err := fmt.Fprint(obj.pdfwriter.outfile, arrayToString(obj.Array))
		if err != nil {
			return err
		}
		obj.pdfwriter.pos += int64(n)
	}
	if obj.Data.Len() > 0 {
		n, err := fmt.Fprintln(obj.pdfwriter.outfile, "\nstream")
		if err != nil {
			return err
		}
		obj.pdfwriter.pos += int64(n)
		m, err := obj.Data.WriteTo(obj.pdfwriter.outfile)
		if err != nil {
			return err
		}
		obj.pdfwriter.pos += m
		n, err = fmt.Fprint(obj.pdfwriter.outfile, "\nendstream")
		if err != nil {
			return err
		}
		obj.pdfwriter.pos += int64(n)
	}
	obj.pdfwriter.endObject()
	return nil
}

// Dict writes the dict d to a PDF object
func (obj *Object) Dict(d Dict) *Object {
	obj.Dictionary = d
	return obj
}
