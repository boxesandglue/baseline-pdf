package pdf

import (
	"bytes"
	"fmt"
	"os"

	"github.com/speedata/textlayout/fonts"
	"github.com/speedata/textlayout/fonts/truetype"
	"github.com/speedata/textlayout/harfbuzz"
)

var (
	ids chan int
)

func genIntegerSequence(ids chan int) {
	i := int(0)
	for {
		ids <- i
		i++
	}
}

func init() {
	ids = make(chan int)
	go genIntegerSequence(ids)
}

// newInternalFontName returns a font name for the PDF such as /F1.
func newInternalFontName() string {
	return fmt.Sprintf("/F%d", <-ids)
}

// Face represents a font structure with no specific size. To get the dimensions
// of a font, you need to create a Font object with a given size.
type Face struct {
	FaceID         int
	HarfbuzzFont   *harfbuzz.Font
	UnitsPerEM     int32
	Cmap           fonts.Cmap
	Filename       string
	PostscriptName string
	toRune         map[fonts.GID]rune
	toGlyphIndex   map[rune]fonts.GID
	usedChar       map[int]bool
	fontobject     *Object
	pw             *PDF
}

// SortByFaceID is used to sort the order of the written font faces in the PDF
// file to create reproducible builds.
type SortByFaceID []*Face

func (a SortByFaceID) Len() int           { return len(a) }
func (a SortByFaceID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a SortByFaceID) Less(i, j int) bool { return a[i].FaceID < a[j].FaceID }

// RegisterChars marks the codepoints as used on the page. For font subsetting.
func (face *Face) RegisterChars(codepoints []int) {
	// RegisterChars tells the PDF file which fonts are used on a page and which
	// characters are included. The slice must include every used char in this
	// font in any order at least once.
	face.usedChar[0] = true
	for _, v := range codepoints {
		face.usedChar[v] = true
	}
}

// RegisterChar marks the codepoint as used on the page. For font subsetting.
func (face *Face) RegisterChar(codepoint int) {
	face.usedChar[0] = true
	face.usedChar[codepoint] = true
}

func fillFaceObject(hbFace harfbuzz.Face) (*Face, error) {
	cm, _ := hbFace.Cmap()
	face := Face{
		FaceID:         <-ids,
		UnitsPerEM:     int32(hbFace.Upem()),
		HarfbuzzFont:   harfbuzz.NewFont(hbFace),
		PostscriptName: hbFace.PostscriptName(),
		toRune:         make(map[fonts.GID]rune),
		toGlyphIndex:   make(map[rune]fonts.GID),
		usedChar:       make(map[int]bool),
		Cmap:           cm,
	}

	return &face, nil
}

// NewFaceFromData returns a Face object which is a representation of a font file.
// The first parameter (id) should be the file name of the font, but can be any string.
// This is to prevent duplicate font loading.
func NewFaceFromData(pw *PDF, data []byte, idx int) (*Face, error) {
	r := bytes.NewReader(data)
	fnt, err := truetype.Load(r)
	if err != nil {
		return nil, err
	}
	requestedFace := fnt[idx]
	f, err := fillFaceObject(requestedFace)
	if err != nil {
		return nil, err
	}
	f.pw = pw
	f.fontobject = pw.NewObject()
	return f, nil

}

// LoadFace loads a font from the disc. The index specifies the sub font to be
// loaded.
func LoadFace(pw *PDF, filename string, idx int) (*Face, error) {
	r, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	Logger.Info("Load font", "filename", filename)
	fnt, err := truetype.Load(r)
	if err != nil {
		return nil, err
	}
	firstface := fnt[0]

	f, err := fillFaceObject(firstface)
	if err != nil {
		return nil, err
	}
	f.pw = pw
	f.fontobject = pw.NewObject()
	f.Filename = filename
	return f, nil
}

// InternalName returns a PDF usable name such as /F1
func (face *Face) InternalName() string {
	return fmt.Sprintf("/F%d", face.FaceID)
}

// Codepoint tries to find the code point for r. If none found, 0 is returned.
func (face *Face) Codepoint(r rune) fonts.GID {
	if gid, ok := face.Cmap.Lookup(r); ok {
		return gid
	}
	return 0
}

// Codepoints returns the internal code points for the runes.
func (face *Face) Codepoints(runes []rune) []int {
	ret := []int{}
	for _, r := range runes {
		if gid, ok := face.Cmap.Lookup(r); ok {
			ret = append(ret, int(gid))
		}
	}
	return ret
}

// finish writes the font file to the PDF. The font should be subsetted,
// therefore we know the requirements only the end of the PDF file.
func (face *Face) finish() error {
	var err error
	pdfwriter := face.pw
	Logger.Info("Write font to PDF", "filename", face.Filename, "psname", face.PostscriptName)
	fnt := face.HarfbuzzFont.Face()
	subset := make([]fonts.GID, len(face.usedChar))
	i := 0
	for g := range face.usedChar {
		subset[i] = fonts.GID(g)
		i++
	}

	if err = fnt.Subset(subset); err != nil {
		return err
	}

	fontstream := pdfwriter.NewObject()

	if err = fnt.WriteSubset(fontstream.Data); err != nil {
		return err
	}
	fontstream.SetCompression(9)

	var isCFF bool
	if otf, ok := fnt.(*truetype.Font); ok {
		isCFF = otf.Type == truetype.TypeOpenType
	}
	fontstream.Dictionary = Dict{}

	if isCFF {
		fontstream.Dictionary["/Subtype"] = "/CIDFontType0C"
	}
	if err = fontstream.Save(); err != nil {
		return err
	}
	fontDescriptor := Dict{
		"Type":        "/FontDescriptor",
		"FontName":    fnt.NamePDF(),
		"FontBBox":    fnt.BoundingBoxPDF(),
		"Ascent":      fmt.Sprintf("%d", fnt.AscenderPDF()),
		"Descent":     fmt.Sprintf("%d", fnt.DescenderPDF()),
		"CapHeight":   fmt.Sprintf("%d", fnt.CapHeightPDF()),
		"Flags":       fmt.Sprintf("%d", fnt.FlagsPDF()),
		"ItalicAngle": fmt.Sprintf("%d", fnt.ItalicAnglePDF()),
		"StemV":       fmt.Sprintf("%d", fnt.StemVPDF()),
		"XHeight":     fmt.Sprintf("%d", fnt.XHeightPDF()),
	}
	if isCFF {
		fontDescriptor["FontFile3"] = fontstream.ObjectNumber.Ref()
	} else {
		fontDescriptor["FontFile2"] = fontstream.ObjectNumber.Ref()
	}

	fontDescriptorObj := face.pw.NewObject()
	fdd := fontDescriptorObj.Dict(fontDescriptor)
	fdd.Save()

	cmap := fnt.CMapPDF()
	cmapObj := pdfwriter.NewObject()
	cmapObj.Data.WriteString(cmap)
	if err = cmapObj.Save(); err != nil {
		return err
	}

	cidFontType2 := Dict{
		"BaseFont":       fnt.NamePDF(),
		"CIDSystemInfo":  `<< /Ordering (Identity) /Registry (Adobe) /Supplement 0 >>`,
		"FontDescriptor": fontDescriptorObj.ObjectNumber.Ref(),
		"Subtype":        "/CIDFontType2",
		"Type":           "/Font",
		"W":              fnt.WidthsPDF(),
	}

	if isCFF {
		cidFontType2["Subtype"] = "/CIDFontType0"
	} else {
		cidFontType2["Subtype"] = "/CIDFontType2"
		cidFontType2["CIDToGIDMap"] = "/Identity"
	}
	cidFontType2Obj := face.pw.NewObject()
	d := cidFontType2Obj.Dict(cidFontType2)
	d.Save()

	fontObj := face.fontobject
	fontObj.Dict(Dict{
		"BaseFont":        fnt.NamePDF(),
		"DescendantFonts": fmt.Sprintf("[%s]", cidFontType2Obj.ObjectNumber.Ref()),
		"Encoding":        "/Identity-H",
		"Subtype":         "/Type0",
		"ToUnicode":       cmapObj.ObjectNumber.Ref(),
		"Type":            "/Font",
	})
	fontObj.Save()
	return nil
}
