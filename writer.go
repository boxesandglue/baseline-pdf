package pdf

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
)

var (
	// Logger is initialized to write to io.Discard and the default log level is math.MaxInt, so it should never write anything.
	Logger          *slog.Logger
	PDFNameReplacer = strings.NewReplacer("#20", " ", "/", "#2f", "#", "#23", "(", "#28", ")", "#29")
)

func init() {
	Logger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.Level(math.MaxInt)}))
}

// Objectnumber represents a PDF object number
type Objectnumber int

// Ref returns a reference to the object number
func (o Objectnumber) Ref() string {
	return fmt.Sprintf("%d 0 R", o)
}

// String returns a reference to the object number
func (o Objectnumber) String() string {
	return fmt.Sprintf("%d 0 R", o)
}

// Dict is a dictionary where each key begins with a slash (/). Each value can
// be a string, an array or another dictionary.
type Dict map[Name]any

// Get the PDF representation of a dictionary
func serializeDict(d Dict) string {
	return hashToString(d, 0)
}

// Array is a list of anything
type Array []any

// Name represents a PDF name such as Adobe Green. The String() method prepends
// a / (slash) to the name if not present.
type Name string

// sortByName implements the sorting sequence.
type sortByName []Name

func (a sortByName) Len() int      { return len(a) }
func (a sortByName) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a sortByName) Less(i, j int) bool {
	if a[i] == "Type" {
		return true
	} else if a[j] == "Type" {
		return false
	}
	return a[i] < a[j]
}

func (n Name) String() string {
	r, _ := strings.CutPrefix(string(n), "/")
	return "/" + PDFNameReplacer.Replace(r)
}

// Pages is the parent page structure
type Pages struct {
	Pages  []*Page
	objnum Objectnumber
}

// An Annotation is a PDF element that is additional to the text, such as a
// hyperlink or a note.
type Annotation struct {
	Subtype    Name
	Action     string
	Dictionary Dict
	Rect       [4]float64 // x1, y1, x2, y2
}

// Separation represents a spot color
type Separation struct {
	Obj        Objectnumber
	ID         string
	Name       string
	ICCProfile Objectnumber
	C          float64
	M          float64
	Y          float64
	K          float64
}

// Page contains information about a single page.
type Page struct {
	Objnum        Objectnumber // The "/Page" object
	Annotations   []Annotation
	Faces         []*Face
	Images        []*Imagefile
	Width         float64
	Height        float64
	OffsetX       float64
	OffsetY       float64
	Dict          Dict // Additional dictionary entries such as "/Trimbox"
	contentStream *Object
}

// Outline represents PDF bookmarks. To create outlines, you need to assign
// previously created Dest items to the outline. When Open is true, the PDF
// viewer shows the child outlines.
type Outline struct {
	Children     []*Outline
	Title        string
	Open         bool
	Dest         string
	objectNumber Objectnumber
}

// PDF is the central point of writing a PDF file.
type PDF struct {
	Catalog           Dict
	InfoDict          Dict
	DefaultOffsetX    float64
	DefaultOffsetY    float64
	DefaultPageWidth  float64
	DefaultPageHeight float64
	Colorspaces       []*Separation
	NameDestinations  map[String]*NameDest
	Outlines          []*Outline
	Major             uint
	Minor             uint
	NoPages           int // set when PDF is finished
	lastEOL           int64
	names             Dict
	nextobject        Objectnumber
	objectlocations   map[Objectnumber]int64
	outfile           io.Writer
	pages             *Pages
	pos               int64

	// having a zlib writer here and using reset removes lots
	// of allocations that would happen with
	// a new zlib writer for each stream
	zlibWriter *zlib.Writer
}

// NewPDFWriter creates a PDF file for writing to file
func NewPDFWriter(file io.Writer) *PDF {
	pw := PDF{
		Major:            1,
		Minor:            7,
		NameDestinations: make(map[String]*NameDest),
		objectlocations:  make(map[Objectnumber]int64),
		zlibWriter:       zlib.NewWriter(io.Discard),
		names:            make(Dict),
	}
	pw.outfile = file
	pw.nextobject = 1
	pw.objectlocations[0] = 0
	pw.pages = &Pages{}
	return &pw
}

// Return the Dict for the specified name. If it does not exist, it is created.
func (pd *PDF) GetCatalogNameTreeDict(dict Name) Dict {
	if pd.names[dict] == nil {
		pd.names[dict] = make(Dict)
	}
	return pd.names[dict].(Dict)
}

func (pw *PDF) writePDFHead() error {
	s := fmt.Sprintf("%%PDF-%d.%d\n%%\x80\x80\x80\x80", pw.Major, pw.Minor)
	n, err := fmt.Fprint(pw.outfile, s)
	pw.pos += int64(n)
	return err
}

// Print writes the string to the PDF file
func (pw *PDF) Print(s string) error {
	var err error
	if pw.pos == 0 {
		if err = pw.writePDFHead(); err != nil {
			return err
		}
	}
	n, err := fmt.Fprint(pw.outfile, s)
	pw.pos += int64(n)
	return err
}

// Println writes the string to the PDF file and adds a newline.
func (pw *PDF) Println(s string) error {
	var err error
	if pw.pos == 0 {
		if err = pw.writePDFHead(); err != nil {
			return err
		}
	}
	n, err := fmt.Fprintln(pw.outfile, s)
	pw.pos += int64(n)
	return err
}

// Printf writes the formatted string to the PDF file.
func (pw *PDF) Printf(format string, a ...any) error {
	var err error
	if pw.pos == 0 {
		if err = pw.writePDFHead(); err != nil {
			return err
		}
	}
	n, err := fmt.Fprintf(pw.outfile, format, a...)
	pw.pos += int64(n)
	return err
}

// AddPage adds a page to the PDF file. The content stream must a stream object
// (i.e. an object with data). Pass 0 for the page object number if you don't
// pre-allocate an object number for the page.
func (pw *PDF) AddPage(content *Object, page Objectnumber) *Page {
	pg := &Page{
		Width:   pw.DefaultPageWidth,
		Height:  pw.DefaultPageHeight,
		OffsetX: pw.DefaultOffsetX,
		OffsetY: pw.DefaultOffsetY,
	}
	if page == 0 {
		page = pw.NextObject()
	}
	pg.contentStream = content
	content.ForceStream = true
	pg.Objnum = page
	pw.pages.Pages = append(pw.pages.Pages, pg)
	return pg
}

// NextObject returns the next free object number
func (pw *PDF) NextObject() Objectnumber {
	pw.nextobject++
	return pw.nextobject - 1
}

func (pw *PDF) writeInfoDict() (*Object, error) {
	if pw.Major < 2 {
		info := pw.NewObject()
		info.Dictionary = pw.InfoDict
		info.Save()
		return info, nil
	}
	return nil, nil
}

func (pw *PDF) writeDocumentCatalogAndPages() (Objectnumber, error) {
	var err error
	usedFaces := make(map[*Face]bool)
	usedImages := make(map[*Imagefile]bool)
	// Write all page streams:
	for _, page := range pw.pages.Pages {
		for _, img := range page.Images {
			usedImages[img] = true
		}
		if err = page.contentStream.Save(); err != nil {
			return 0, err
		}
	}

	// Page streams are finished. Now the /Page dictionaries with
	// references to the streams and the parent
	// Pages objects have to be placed in the file

	//  We need to know in advance where the parent object is written (/Pages)
	pagesObj := pw.NewObject()

	// write out all images to the PDF
	// In order to create reproducible PDFs, let's write the image in a certain order.
	sortedImages := make([]*Imagefile, 0, len(usedImages))
	for k := range usedImages {
		sortedImages = append(sortedImages, k)
	}
	sort.Sort(sortImagefile(sortedImages))
	for _, img := range sortedImages {
		img.finish()
	}

	if len(pw.pages.Pages) == 0 {
		return 0, fmt.Errorf("no pages in document")
	}

	for _, page := range pw.pages.Pages {
		obj := pw.NewObjectWithNumber(page.Objnum)
		fnts := Dict{}
		if len(page.Faces) > 0 {
			for _, face := range page.Faces {
				fnts[Name(face.InternalName())] = face.fontobject.ObjectNumber.Ref()
			}
		}

		resHash := Dict{}
		if len(page.Faces) > 0 {
			for _, face := range page.Faces {
				usedFaces[face] = true
			}
			resHash["Font"] = fnts
		}
		if len(pw.Colorspaces) > 0 {
			colorspace := Dict{}

			for _, cs := range pw.Colorspaces {
				colorspace[Name(cs.ID)] = cs.Obj.String()
			}
			resHash["ColorSpace"] = colorspace
		}
		if len(page.Images) > 0 {
			var sb strings.Builder
			sb.WriteString("<<")
			for _, img := range page.Images {
				sb.WriteRune(' ')
				sb.WriteString(img.InternalName())
				sb.WriteRune(' ')
				sb.WriteString(img.imageobject.ObjectNumber.Ref())
			}
			sb.WriteString(">>")
			resHash["XObject"] = sb.String()
		}

		pageHash := Dict{
			"Type":     "/Page",
			"Contents": page.contentStream.ObjectNumber.Ref(),
			"Parent":   pagesObj.ObjectNumber.Ref(),
		}
		if page.OffsetX != pw.DefaultOffsetX || page.OffsetY != pw.DefaultOffsetY || page.Width != pw.DefaultPageWidth || page.Height != pw.DefaultPageHeight {
			pageHash["MediaBox"] = fmt.Sprintf("[%s %s %s %s]", FloatToPoint(page.OffsetX), FloatToPoint(page.OffsetY), FloatToPoint(page.Width), FloatToPoint(page.Height))
		}

		if len(resHash) > 0 {
			pageHash["Resources"] = resHash
		}

		annotationObjectNumbers := make([]string, len(page.Annotations))
		for i, annot := range page.Annotations {
			annotObj := pw.NewObject()
			annotDict := Dict{
				"Type":    "/Annot",
				"Subtype": annot.Subtype.String(),
				"A":       annot.Action,
				"Rect":    fmt.Sprintf("[%s %s %s %s]", FloatToPoint(annot.Rect[0]), FloatToPoint(annot.Rect[1]), FloatToPoint(annot.Rect[2]), FloatToPoint(annot.Rect[3])),
			}
			for k, v := range annot.Dictionary {
				annotDict[k] = v
			}

			annotObj.Dict(annotDict)
			if err := annotObj.Save(); err != nil {
				return 0, err
			}
			annotationObjectNumbers[i] = annotObj.ObjectNumber.Ref()
		}
		if len(annotationObjectNumbers) > 0 {
			pageHash["Annots"] = "[" + strings.Join(annotationObjectNumbers, " ") + "]"
		}
		for k, v := range page.Dict {
			pageHash[k] = v
		}
		obj.Dict(pageHash)
		obj.Save()
	}

	// The pages object
	kids := make([]string, len(pw.pages.Pages))
	for i, v := range pw.pages.Pages {
		kids[i] = v.Objnum.Ref()
	}

	pw.pages.objnum = pagesObj.ObjectNumber
	pagesObj.Dict(Dict{
		"Type":     "/Pages",
		"Kids":     "[ " + strings.Join(kids, " ") + " ]",
		"Count":    fmt.Sprint(len(pw.pages.Pages)),
		"MediaBox": fmt.Sprintf("[%s %s %s %s]", FloatToPoint(pw.DefaultOffsetX), FloatToPoint(pw.DefaultOffsetY), FloatToPoint(pw.DefaultPageWidth), FloatToPoint(pw.DefaultPageHeight)),
	})
	if err = pagesObj.Save(); err != nil {
		return 0, err
	}

	// outlines
	var outlinesOjbNum Objectnumber

	if pw.Outlines != nil {
		outlinesOjb := pw.NewObject()
		first, last, count, err := pw.writeOutline(outlinesOjb, pw.Outlines)
		if err != nil {
			return 0, err
		}

		outlinesOjb.Dictionary = Dict{
			"Type":  "/Outlines",
			"First": first.Ref(),
			"Last":  last.Ref(),
			"Count": fmt.Sprintf("%d", count),
		}
		outlinesOjbNum = outlinesOjb.ObjectNumber

		if err = outlinesOjb.Save(); err != nil {
			return 0, err
		}
	}

	catalog := pw.NewObject()
	dictCatalog := Dict{
		"Type":  "/Catalog",
		"Pages": pw.pages.objnum.Ref(),
	}
	if pw.Outlines != nil {
		dictCatalog["/Outlines"] = outlinesOjbNum.Ref()
	}

	if len(pw.NameDestinations) != 0 {
		type name struct {
			onum Objectnumber
			name String
		}
		destnames := make([]name, 0, len(pw.NameDestinations))

		sortedNames := make([]String, 0, len(pw.NameDestinations))
		for destname := range pw.NameDestinations {
			sortedNames = append(sortedNames, destname)
		}
		slices.Sort(sortedNames)
		for _, n := range sortedNames {
			nd := pw.NameDestinations[n]
			nd.objectnumber, err = pw.writeDestObj(nd.PageObjectnumber, nd.X, nd.Y)
			if err != nil {
				return 0, err
			}
			destnames = append(destnames, name{name: nd.Name, onum: nd.objectnumber})
		}

		var limitsAry, namesAry Array
		limitsAry = append(limitsAry, destnames[0].name)
		limitsAry = append(limitsAry, destnames[len(destnames)-1].name)
		for _, n := range destnames {
			namesAry = append(namesAry, String(n.name))
			namesAry = append(namesAry, n.onum.Ref())
		}

		destNameTree := Dict{
			"Limits": Serialize(limitsAry),
			"Names":  Serialize(namesAry),
		}

		pw.names["Dests"] = destNameTree
	}

	if len(pw.names) > 0 {
		dictCatalog["Names"] = pw.names
	}
	for k, v := range pw.Catalog {
		dictCatalog[k] = v
	}
	catalog.Dict(dictCatalog)
	if err = catalog.Save(); err != nil {
		return 0, err
	}

	// write out all font descriptors and files into the PDF
	sortedFaces := make([]*Face, 0, len(usedFaces))
	for k := range usedFaces {
		sortedFaces = append(sortedFaces, k)
	}
	sort.Sort(sortByFaceID(sortedFaces))
	for _, f := range sortedFaces {
		if err = f.finish(); err != nil {
			return 0, err
		}
	}

	return catalog.ObjectNumber, nil
}

func (pw *PDF) writeDestObj(page Objectnumber, x, y float64) (Objectnumber, error) {
	obj := pw.NewObject()
	dest := fmt.Sprintf("[%s /XYZ %0.5g %0.5g null]", page.Ref(), x, y)
	obj.Dict(Dict{
		"D": dest,
	})

	if err := obj.Save(); err != nil {
		return 0, err
	}
	return obj.ObjectNumber, nil

}

func (pw *PDF) writeOutline(parentObj *Object, outlines []*Outline) (first Objectnumber, last Objectnumber, c int, err error) {
	for _, outline := range outlines {
		outline.objectNumber = pw.NextObject()
	}

	c = 0
	for i, outline := range outlines {
		c++
		outlineObj := pw.NewObjectWithNumber(outline.objectNumber)
		outlineDict := Dict{}
		outlineDict["Parent"] = parentObj.ObjectNumber.Ref()
		outlineDict["Title"] = stringToPDF(outline.Title)
		outlineDict["Dest"] = Serialize(outline.Dest)

		if i < len(outlines)-1 {
			outlineDict["Next"] = outlines[i+1].objectNumber.Ref()
		} else {
			last = outline.objectNumber
		}
		if i > 0 {
			outlineDict["Prev"] = outlines[i-1].objectNumber.Ref()
		} else {
			first = outline.objectNumber
		}

		if len(outline.Children) > 0 {
			var cldFirst, cldLast Objectnumber
			var count int
			cldFirst, cldLast, count, err = pw.writeOutline(outlineObj, outline.Children)
			if err != nil {
				return
			}
			outlineDict["First"] = cldFirst.Ref()
			outlineDict["Last"] = cldLast.Ref()
			if outline.Open {
				outlineDict["Count"] = fmt.Sprintf("%d", count)
			} else {
				outlineDict["Count"] = "-1"
			}
			c += count
		}
		outlineObj.Dictionary = outlineDict
		outlineObj.Save()
	}
	return
}

// Finish writes the trailer and xref section but does not close the file.
func (pw *PDF) Finish() error {
	dc, err := pw.writeDocumentCatalogAndPages()
	if err != nil {
		return err
	}

	infodict, err := pw.writeInfoDict()
	if err != nil {
		return err
	}

	// XRef section
	type chunk struct {
		startOnum Objectnumber
		positions []int64
	}
	objectChunks := []chunk{}
	var curchunk *chunk
	for i := Objectnumber(0); i <= pw.nextobject; i++ {
		if loc, ok := pw.objectlocations[i]; ok {
			if curchunk == nil {
				curchunk = &chunk{
					startOnum: i,
				}
			}
			curchunk.positions = append(curchunk.positions, loc)
		} else {
			if curchunk == nil {
				// the PDF might be corrupt
			} else {
				objectChunks = append(objectChunks, *curchunk)
				curchunk = nil
			}
		}
	}
	var str strings.Builder

	for _, chunk := range objectChunks {
		startOnum := chunk.startOnum
		fmt.Fprintf(&str, "%d %d\n", chunk.startOnum, len(chunk.positions))
		for i, pos := range chunk.positions {
			if int(startOnum)+i == 0 {
				fmt.Fprintf(&str, "%010d 65535 f \n", pos)
			} else {
				fmt.Fprintf(&str, "%010d 00000 n \n", pos)
			}
		}
	}

	xrefpos := pw.pos
	pw.Println("xref")
	pw.Print(str.String())
	sum := fmt.Sprintf("%X", md5.Sum([]byte(str.String())))

	trailer := Dict{
		"Size": fmt.Sprint(int(pw.nextobject)),
		"Root": dc.Ref(),
		"ID":   fmt.Sprintf("[<%s> <%s>]", sum, sum),
	}
	if infodict != nil {
		trailer["Info"] = infodict.ObjectNumber.Ref()
	}

	if err = pw.Println("trailer"); err != nil {
		return err
	}

	pw.outHash(trailer)

	if err = pw.Printf("\nstartxref\n%d\n%%%%EOF\n", xrefpos); err != nil {
		return err
	}
	pw.NoPages = len(pw.pages.Pages)
	return nil
}

// Size returns the current size of the PDF file.
func (pw *PDF) Size() int64 {
	return pw.pos
}

// hashToString converts a PDF dictionary to a string including the paired angle
// brackets (<< ... >>).
func hashToString(h Dict, level int) string {
	var b bytes.Buffer
	b.WriteString("<<\n")
	keys := make([]Name, 0, len(h))
	for v := range h {
		keys = append(keys, v)
	}
	sort.Sort(sortByName(keys))
	for _, key := range keys {
		b.WriteString(fmt.Sprintf("%s%s %v\n", strings.Repeat(" ", level+1), key, serializeLevel(h[key], level+1)))
	}
	b.WriteString(strings.Repeat(" ", level))
	b.WriteString(">>")
	return b.String()
}

func (pw *PDF) outHash(h Dict) {
	pw.Printf(hashToString(h, 0))
}

// Write an end of line (EOL) marker to the file if it is not on a EOL already.
func (pw *PDF) eol() {
	if pw.pos == 0 {
		pw.writePDFHead()
	}
	if pw.pos != pw.lastEOL {
		pw.Println("")
		pw.lastEOL = pw.pos
	}
}

// Write a start object marker with the next free object.
func (pw *PDF) startObject(onum Objectnumber) error {
	var position int64
	if pw.pos == 0 {
		var err error
		if err = pw.writePDFHead(); err != nil {
			return err
		}
	}
	position = pw.pos + 1
	pw.objectlocations[onum] = position
	pw.Printf("\n%d 0 obj\n", onum)
	return nil
}

// Write a simple "endobj" to the PDF file. Return the object number.
func (pw *PDF) endObject() Objectnumber {
	onum := pw.nextobject
	pw.eol()
	pw.Println("endobj")
	return onum
}
