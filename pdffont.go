package pdf

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/boxesandglue/textshape/ot"
	"github.com/boxesandglue/textshape/subset"
)

var integerSequence int64

// nextID generates a new unique face ID.
func nextID() int {
	return int(atomic.AddInt64(&integerSequence, 1))
}

// Face represents a font structure with no specific size. To get the dimensions
// of a font, you need to create a Font object with a given size.
type Face struct {
	FaceID            int
	Shaper            *ot.Shaper
	UnitsPerEM        int32
	Filename          string
	PostscriptName    string
	usedChar          map[int]bool
	fontobject        *Object
	pw                *PDF
	Scale             float64
	face              *ot.Face
	glyphMap          map[ot.GlyphID]ot.GlyphID // old GID -> new GID (set after subsetting)
	VariationSettings map[string]float64        // axis tag -> value for variable fonts
}

// RegisterChars marks the codepoints as used on the page. For font subsetting.
//
// Deprecated: use RegisterCodepoints instead.
func (face *Face) RegisterChars(codepoints []int) {
	face.RegisterCodepoints(codepoints)
}

// RegisterChar marks the codepoint as used on the page. For font subsetting.
//
// Deprecated: use RegisterCodepoint instead.
func (face *Face) RegisterChar(codepoint int) {
	face.RegisterCodepoint(codepoint)
}

// RegisterCodepoints marks the codepoints as used on the page. For font subsetting.
func (face *Face) RegisterCodepoints(codepoints []int) {
	face.usedChar[0] = true
	for _, v := range codepoints {
		face.usedChar[v] = true
	}
}

// RegisterCodepoint marks the codepoint as used on the page. For font subsetting.
func (face *Face) RegisterCodepoint(codepoint int) {
	face.usedChar[0] = true
	face.usedChar[codepoint] = true
}

func fillFaceObject(otFace *ot.Face) (*Face, error) {
	shaper, err := ot.NewShaper(otFace.Font)
	if err != nil {
		return nil, err
	}

	face := Face{
		FaceID:         nextID(),
		UnitsPerEM:     int32(otFace.Upem()),
		Shaper:         shaper,
		PostscriptName: otFace.PostscriptName(),
		usedChar:       make(map[int]bool),
		Scale:          1.0,
		face:           otFace,
	}

	return &face, nil
}

// NewFaceFromData returns a Face object which is a representation of a font file.
func (pw *PDF) NewFaceFromData(data []byte, idx int) (*Face, error) {
	font, err := ot.ParseFont(data, idx)
	if err != nil {
		return nil, err
	}
	otFace, err := ot.NewFace(font)
	if err != nil {
		return nil, err
	}
	f, err := fillFaceObject(otFace)
	if err != nil {
		return nil, err
	}
	f.Filename = "(embedded)"
	f.pw = pw
	f.fontobject = pw.NewObject()
	return f, nil
}

// LoadFace loads a font from the disc. The index specifies the sub font to be loaded.
func (pw *PDF) LoadFace(filename string, idx int) (*Face, error) {
	r, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	Logger.Info("Load font", "filename", filename)
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	font, err := ot.ParseFont(data, idx)
	if err != nil {
		return nil, err
	}
	otFace, err := ot.NewFace(font)
	if err != nil {
		return nil, err
	}
	f, err := fillFaceObject(otFace)
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
func (face *Face) Codepoint(r rune) int {
	cmap := face.face.Cmap()
	if cmap == nil {
		return 0
	}
	if gid, ok := cmap.Lookup(ot.Codepoint(r)); ok {
		return int(gid)
	}
	return 0
}

// Codepoints returns the internal code points for the runes.
func (face *Face) Codepoints(runes []rune) []int {
	cmap := face.face.Cmap()
	if cmap == nil {
		return nil
	}
	ret := make([]int, 0, len(runes))
	for _, r := range runes {
		if gid, ok := cmap.Lookup(ot.Codepoint(r)); ok {
			ret = append(ret, int(gid))
		}
	}
	return ret
}

// OTFace returns the underlying ot.Face for direct access.
func (face *Face) OTFace() *ot.Face {
	return face.face
}

// MapGlyph maps an old glyph ID to the new glyph ID after compact subsetting.
// Only useful after calling CompactSubset().
// If compact subsetting is not used, returns the original ID unchanged.
func (face *Face) MapGlyph(oldGID int) int {
	if face.glyphMap == nil {
		return oldGID
	}
	if newGID, ok := face.glyphMap[ot.GlyphID(oldGID)]; ok {
		return int(newGID)
	}
	return oldGID
}

// CompactSubset enables compact glyph mapping for smaller font files.
// When called, glyph IDs are renumbered (0, 1, 2, ...) instead of keeping
// the original positions. Use MapGlyph() to convert old GIDs to new GIDs
// when writing content streams.
// Must be called after RegisterCodepoints() and before creating content streams.
func (face *Face) CompactSubset() error {
	if face.glyphMap != nil {
		return nil // already prepared
	}

	// Collect glyphs to subset (old GIDs)
	oldGlyphs := make([]ot.GlyphID, 0, len(face.usedChar))
	for g := range face.usedChar {
		oldGlyphs = append(oldGlyphs, ot.GlyphID(g))
	}

	// Create subset input
	input := subset.NewInput()
	input.Flags = subset.FlagDropLayoutTables
	for _, gid := range oldGlyphs {
		input.AddGlyph(gid)
	}

	// Create plan and get glyph map
	plan, err := subset.CreatePlan(face.face.Font, input)
	if err != nil {
		return err
	}

	face.glyphMap = plan.GlyphMap()
	return nil
}

// --- PDF formatting helpers ---

// subsetTag generates a 6-character subset tag.
// It hashes the glyph IDs and variation settings to ensure unique tags
// for different subsets of the same font with different variations.
func subsetTag(glyphs []ot.GlyphID, variations map[string]float64) string {
	sorted := make([]ot.GlyphID, len(glyphs))
	copy(sorted, glyphs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	data := make([]byte, len(sorted)*2)
	for i, g := range sorted {
		data[i*2] = byte((g >> 8) & 0xff)
		data[i*2+1] = byte(g & 0xff)
	}

	// Include variation settings in the hash for unique tags per variation
	if len(variations) > 0 {
		keys := make([]string, 0, len(variations))
		for k := range variations {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			data = append(data, []byte(fmt.Sprintf("%s:%.2f", k, variations[k]))...)
		}
	}

	sum := md5.Sum(data)
	ret := make([]rune, 6)
	for i := 0; i < 6; i++ {
		ret[i] = rune(sum[2*i]+sum[2*i+1])%26 + 'A'
	}
	return string(ret)
}

// fontNamePDF returns the PDF font name with subset tag.
func fontNamePDF(f *ot.Face, tag string) string {
	psName := f.PostscriptName()
	if psName == "" {
		psName = "Unknown"
	}
	if tag != "" {
		return fmt.Sprintf("/%s+%s", tag, psName)
	}
	return "/" + psName
}

// bboxPDF returns the font bounding box as PDF string.
func bboxPDF(f *ot.Face) string {
	xMin, yMin, xMax, yMax := f.BBox()
	return fmt.Sprintf("[%d %d %d %d]", xMin, yMin, xMax, yMax)
}

// flagsPDF returns the PDF font flags.
func flagsPDF(f *ot.Face) int {
	flags := 0
	if f.IsFixedPitch() {
		flags |= 1 // FixedPitch
	}
	if f.IsItalic() {
		flags |= 64 // Italic
	}
	if flags == 0 {
		flags = 4 // Non-symbolic
	}
	return flags
}

// stemVPDF estimates StemV based on weight.
func stemVPDF(f *ot.Face) int {
	weight := f.WeightClass()
	if weight >= 700 {
		return 140
	} else if weight >= 500 {
		return 100
	}
	return 80
}

// widthsPDF returns PDF width array for glyphs.
// newGlyphs contains the new (post-subset) glyph IDs.
// reverseMap maps new GID -> old GID for looking up widths in the original font.
func widthsPDF(f *ot.Face, newGlyphs []ot.GlyphID, reverseMap map[ot.GlyphID]ot.GlyphID) string {
	if len(newGlyphs) == 0 {
		return "[]"
	}

	// Scale: for CFF fonts the units are typically 1000, for TrueType we scale
	scale := 1.0
	if !f.IsCFF() {
		scale = float64(f.Upem()) / 1000.0
	}

	getWd := func(newGID ot.GlyphID) string {
		oldGID := reverseMap[newGID]
		advance := f.HorizontalAdvance(oldGID)
		return strconv.FormatFloat(float64(advance)/scale, 'f', -1, 64)
	}

	// Sort glyphs for consistent output
	sorted := make([]ot.GlyphID, len(newGlyphs))
	copy(sorted, newGlyphs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var b strings.Builder
	b.WriteString("[")
	c := 0
	for c < len(sorted) {
		cp := sorted[c]
		fmt.Fprintf(&b, "%d[%s", cp, getWd(cp))
		c++
		for c < len(sorted) && sorted[c] == cp+1 {
			cp++
			fmt.Fprintf(&b, " %s", getWd(cp))
			c++
		}
		b.WriteString("]")
	}
	b.WriteString("]")
	return b.String()
}

// cmapPDF returns PDF ToUnicode CMap for glyphs.
// newGlyphs contains the new (post-subset) glyph IDs.
// reverseMap maps new GID -> old GID for looking up Unicode in the original font.
func cmapPDF(f *ot.Face, newGlyphs []ot.GlyphID, reverseMap map[ot.GlyphID]ot.GlyphID) string {
	if len(newGlyphs) == 0 {
		return ""
	}

	// Build old glyph to unicode mapping from original font
	var glyphToUnicode map[ot.GlyphID]rune
	if cmap := f.Cmap(); cmap != nil {
		glyphToUnicode = cmap.CollectReverseMapping()
	} else {
		glyphToUnicode = make(map[ot.GlyphID]rune)
	}

	// Find max new glyph ID
	maxGlyph := ot.GlyphID(0)
	for _, gid := range newGlyphs {
		if gid > maxGlyph {
			maxGlyph = gid
		}
	}

	var b strings.Builder
	b.WriteString(`/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe)/Ordering (UCS)/Supplement 0>> def
/CMapName /Adobe-Identity-UCS def /CMapType 2 def
1 begincodespacerange
`)
	fmt.Fprintf(&b, "<0001><%04X>\n", maxGlyph+1)
	b.WriteString("endcodespacerange\n")
	fmt.Fprintf(&b, "%d beginbfchar\n", len(newGlyphs))
	for _, newGID := range newGlyphs {
		oldGID := reverseMap[newGID]
		r := glyphToUnicode[oldGID]
		if r == 0 {
			r = 0xFFFD
		}
		fmt.Fprintf(&b, "<%04X><%04X>\n", newGID, r)
	}
	b.WriteString(`endbfchar
endcmap CMapName currentdict /CMap defineresource pop end end`)
	return b.String()
}

// finish writes the font file to the PDF.
func (face *Face) finish() error {
	var err error
	pdfwriter := face.pw
	Logger.Info("Write font to PDF", "filename", face.Filename, "psname", face.PostscriptName)

	// Collect glyphs to subset (old GIDs)
	oldGlyphs := make([]ot.GlyphID, 0, len(face.usedChar))
	for g := range face.usedChar {
		oldGlyphs = append(oldGlyphs, ot.GlyphID(g))
	}

	// Check if CompactSubset was called
	// If not, use FlagRetainGIDs so old GIDs in content streams still work
	useCompactMapping := face.glyphMap != nil

	// Create subset input
	input := subset.NewInput()
	input.Flags = subset.FlagDropLayoutTables
	if !useCompactMapping {
		input.Flags |= subset.FlagRetainGIDs
	}
	for _, gid := range oldGlyphs {
		input.AddGlyph(gid)
	}

	// Apply variation settings for instancing (creates static font from variable)
	if len(face.VariationSettings) > 0 {
		Logger.Debug("Applying variation settings for instancing", "variations", face.VariationSettings)
	}
	for tag, value := range face.VariationSettings {
		input.PinAxisLocation(ot.MakeTag(tag[0], tag[1], tag[2], tag[3]), float32(value))
	}

	// Subset the font
	plan, err := subset.CreatePlan(face.face.Font, input)
	if err != nil {
		return err
	}
	subsetData, err := plan.Execute()
	if err != nil {
		return err
	}

	// Build glyph list and reverse map for PDF tables
	var newGlyphs []ot.GlyphID
	var reverseMap map[ot.GlyphID]ot.GlyphID

	if useCompactMapping {
		// Use the pre-computed glyph map from CompactSubset
		newGlyphs = make([]ot.GlyphID, 0, len(face.glyphMap))
		reverseMap = make(map[ot.GlyphID]ot.GlyphID)
		for oldGID, newGID := range face.glyphMap {
			newGlyphs = append(newGlyphs, newGID)
			reverseMap[newGID] = oldGID
		}
	} else {
		// With FlagRetainGIDs, old GID == new GID (identity mapping)
		newGlyphs = oldGlyphs
		reverseMap = make(map[ot.GlyphID]ot.GlyphID)
		for _, gid := range oldGlyphs {
			reverseMap[gid] = gid
		}
	}

	// Generate subset tag using old GIDs and variations for consistency
	tag := subsetTag(oldGlyphs, face.VariationSettings)

	fontstream := pdfwriter.NewObject()

	isCFF := face.face.IsCFF()

	if isCFF {
		// For CFF fonts, PDF needs only the raw CFF table data,
		// not the full SFNT/OTF file
		subsetFont, err := ot.ParseFont(subsetData, 0)
		if err != nil {
			return fmt.Errorf("failed to parse subset font: %w", err)
		}
		cffData, err := subsetFont.TableData(ot.TagCFF)
		if err != nil {
			return fmt.Errorf("failed to get CFF table from subset: %w", err)
		}
		fontstream.Data.Write(cffData)
	} else {
		// For TrueType fonts, PDF needs the full SFNT file
		fontstream.Data.Write(subsetData)
	}
	fontstream.SetCompression(9)

	fontstream.Dictionary = Dict{}
	if isCFF {
		fontstream.Dictionary["/Subtype"] = "/CIDFontType0C"
	}
	if err = fontstream.Save(); err != nil {
		return err
	}

	// Font descriptor using raw metrics from ot.Face
	f := face.face
	fontDescriptor := Dict{
		"Type":        "/FontDescriptor",
		"FontName":    fontNamePDF(f, tag),
		"FontBBox":    bboxPDF(f),
		"Ascent":      fmt.Sprintf("%d", f.Ascender()),
		"Descent":     fmt.Sprintf("%d", f.Descender()),
		"CapHeight":   fmt.Sprintf("%d", f.CapHeight()),
		"Flags":       fmt.Sprintf("%d", flagsPDF(f)),
		"ItalicAngle": fmt.Sprintf("%d", f.ItalicAngle()>>16),
		"StemV":       fmt.Sprintf("%d", stemVPDF(f)),
		"XHeight":     fmt.Sprintf("%d", f.XHeight()),
	}
	if isCFF {
		fontDescriptor["FontFile3"] = fontstream.ObjectNumber.Ref()
	} else {
		fontDescriptor["FontFile2"] = fontstream.ObjectNumber.Ref()
	}

	fontDescriptorObj := face.pw.NewObject()
	fdd := fontDescriptorObj.Dict(fontDescriptor)
	fdd.Save()

	cmapStr := cmapPDF(f, newGlyphs, reverseMap)
	cmapObj := pdfwriter.NewObject()
	cmapObj.Data.WriteString(cmapStr)
	if err = cmapObj.Save(); err != nil {
		return err
	}

	cidFontType2 := Dict{
		"BaseFont":       fontNamePDF(f, tag),
		"CIDSystemInfo":  `<< /Ordering (Identity) /Registry (Adobe) /Supplement 0 >>`,
		"FontDescriptor": fontDescriptorObj.ObjectNumber.Ref(),
		"Type":           "/Font",
		"W":              widthsPDF(f, newGlyphs, reverseMap),
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
		"BaseFont":        fontNamePDF(f, tag),
		"DescendantFonts": fmt.Sprintf("[%s]", cidFontType2Obj.ObjectNumber.Ref()),
		"Encoding":        "/Identity-H",
		"Subtype":         "/Type0",
		"ToUnicode":       cmapObj.ObjectNumber.Ref(),
		"Type":            "/Font",
	})
	fontObj.Save()
	return nil
}
