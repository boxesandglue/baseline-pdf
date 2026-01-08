package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"

	// Packages image/jpeg and image/png are not used explicitly in the code below,
	// but are imported for their initialization side-effect, which allows
	// image.Decode to understand JPEG formatted images.
	_ "image/jpeg"
	_ "image/png"

	"github.com/boxesandglue/gofpdi"
)

// Imagefile represents a physical image file. Images to be place in the PDF
// must be derived from the image.
type Imagefile struct {
	Format           string
	NumberOfPages    int
	PageSizes        map[int]map[string]map[string]float64
	Filename         string
	ScaleX           float64
	ScaleY           float64
	W                int
	H                int
	Box              string
	PageNumber       int // The requested page number for PDF images (1-based)
	r                io.ReadSeeker
	pdfimporter      *gofpdi.Importer
	pw               *PDF
	imageobject      *Object
	id               int
	colorspace       string
	bitsPerComponent string
	trns             []byte
	smask            []byte
	pal              []byte
	decodeParms      Dict
	decodeParmsSmask Dict
	data             []byte
}

// LoadImageFileWithBox loads an image from the disc with the given box and page
// number. If box is empty, it defaults to /MediaBox.
func (pw *PDF) LoadImageFileWithBox(filename string, box string, pagenumber int) (*Imagefile, error) {
	Logger.Info("Load image", "filename", filename)
	r, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	imgCfg, format, err := image.DecodeConfig(r)
	if errors.Is(err, image.ErrFormat) {
		return tryParsePDFWithBox(pw, r, filename, box, pagenumber)
	}
	if err != nil {
		return nil, err
	}

	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	imgf := &Imagefile{
		Filename:      filename,
		Format:        format,
		id:            nextID(),
		pw:            pw,
		r:             r,
		ScaleX:        1,
		ScaleY:        1,
		NumberOfPages: 1,
	}

	switch format {
	case "jpeg":
		imgf.parseJPG(imgCfg)
	case "png":
		imgf.parsePNG()
	}

	return imgf, nil
}

// Close closes the underlying file handle.
func (imgf *Imagefile) Close() error {
	if c, ok := imgf.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// LoadImageFile loads an image from the disc. For PDF files it defaults to page
// 1 and the /MediaBox.
func (pw *PDF) LoadImageFile(filename string) (*Imagefile, error) {
	return pw.LoadImageFileWithBox(filename, "/MediaBox", 1)
}

func (imgf *Imagefile) parseJPG(imgCfg image.Config) error {
	switch imgCfg.ColorModel {
	case color.YCbCrModel:
		imgf.colorspace = "DeviceRGB"
	case color.GrayModel:
		imgf.colorspace = "DeviceGray"
	case color.CMYKModel:
		imgf.colorspace = "DeviceCMYK"
	default:
		return fmt.Errorf("color model not supported")
	}

	imgf.bitsPerComponent = "8"
	imgf.W = imgCfg.Width
	imgf.H = imgCfg.Height
	return nil
}

func (imgf *Imagefile) createSMaskObject() Objectnumber {
	d := Dict{
		"Type":             "/XObject",
		"Subtype":          "/Image",
		"BitsPerComponent": imgf.bitsPerComponent,
		"ColorSpace":       "/DeviceGray",
		"Width":            fmt.Sprintf("%d", imgf.W),
		"Height":           fmt.Sprintf("%d", imgf.H),
	}
	if imgf.decodeParmsSmask != nil {
		d["DecodeParms"] = imgf.decodeParmsSmask
	}

	sm := imgf.pw.NewObject()
	sm.Dict(d)
	sm.SetCompression(9)
	// imgf.smask is non-compressed data
	sm.Data.Write(imgf.smask)
	sm.Save()
	return sm.ObjectNumber
}

// if box is empty, defaults to /MediaBox
func tryParsePDFWithBox(pw *PDF, r io.ReadSeeker, filename string, box string, pagenumber int) (*Imagefile, error) {
	if box == "" {
		box = "/MediaBox"
	}
	r.Seek(0, io.SeekStart)
	b, err := readBytes(r, 4)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal([]byte("%PDF"), b) {
		return nil, fmt.Errorf("%w: %s", image.ErrFormat, filename)
	}
	imgf := &Imagefile{
		Filename:   filename,
		Format:     "pdf",
		Box:        box,
		PageNumber: pagenumber,
		id:         nextID(),
		pw:         pw,
		r:          r,
	}

	imgf.pdfimporter = gofpdi.NewImporter()

	f := func() int {
		if imgf.imageobject == nil {
			imgf.imageobject = pw.NewObject()
			return int(imgf.imageobject.ObjectNumber)
		}
		return int(pw.NewObject().ObjectNumber)
	}
	imgf.pdfimporter.SetObjIDGetter(f)
	if err = imgf.pdfimporter.SetSourceStream(r); err != nil {
		return nil, fmt.Errorf("could not set source stream for PDF importer: %w", err)
	}
	if imgf.NumberOfPages, err = imgf.pdfimporter.GetNumPages(); err != nil {
		return nil, err
	}

	ps, err := imgf.pdfimporter.GetPageSizes()
	if err != nil {
		return nil, err
	}
	imgf.PageSizes = ps
	pbox, err := imgf.GetPDFBoxDimensions(pagenumber, box)
	if err != nil {
		return nil, err
	}

	imgf.ScaleX = float64(pbox["w"])
	imgf.ScaleY = float64(pbox["h"])

	return imgf, nil
}

// PDF boxes (crop, trim,...) should not be larger than the mediabox.
func intersectBox(bx map[string]float64, mediabox map[string]float64) map[string]float64 {
	newbox := make(map[string]float64)
	for k, v := range bx {
		newbox[k] = v
	}

	if bx["lly"] < mediabox["lly"] {
		newbox["lly"] = mediabox["lly"]
	}
	if bx["llx"] < mediabox["llx"] {
		newbox["llx"] = mediabox["llx"]
	}
	if bx["ury"] > mediabox["ury"] {
		newbox["ury"] = mediabox["ury"]
	}
	if bx["urx"] > mediabox["urx"] {
		newbox["urx"] = mediabox["urx"]
	}
	newbox["x"] = newbox["llx"]
	newbox["y"] = newbox["lly"]
	newbox["w"] = newbox["urx"] - newbox["llx"]
	newbox["h"] = newbox["ury"] - newbox["lly"]
	return newbox
}

// GetPDFBoxDimensions returns normalized box dimensions for the given page and box name.
// It always computes x, y, w, h and clamps non-Media boxes to the MediaBox.
// Supported names: "/MediaBox", "/CropBox", "/BleedBox", "/TrimBox", "/ArtBox".
// Fallbacks:
//   - missing /CropBox -> /MediaBox
//   - missing /ArtBox|/BleedBox|/TrimBox -> /CropBox if present, else /MediaBox
func (imgf *Imagefile) GetPDFBoxDimensions(p int, boxName string) (map[string]float64, error) {
	// sanity: page present?
	pg, ok := imgf.PageSizes[p]
	if !ok || pg == nil {
		return nil, fmt.Errorf("page %d not found", p)
	}

	// media box is required as clamp reference
	mb := pg["/MediaBox"]
	if len(mb) == 0 {
		return nil, fmt.Errorf("page %d has no /MediaBox", p)
	}

	// helper to pick a source box with fallbacks
	pick := func(name string) map[string]float64 {
		if b := pg[name]; len(b) != 0 {
			return b
		}
		if name == "/CropBox" {
			// /CropBox falls back to /MediaBox
			return mb
		}
		switch name {
		case "/ArtBox", "/BleedBox", "/TrimBox":
			if cb := pg["/CropBox"]; len(cb) != 0 {
				return cb
			}
			return mb
		default:
			return nil
		}
	}

	src := pick(boxName)
	if len(src) == 0 {
		return nil, fmt.Errorf("unknown box %q on page %d", boxName, p)
	}

	// Normalize:
	// - For /MediaBox: compute x,y,w,h (intersect with itself just to fill fields)
	// - For others: intersect against /MediaBox to clamp within page
	if boxName == "/MediaBox" {
		out := intersectBox(src, src) // computes x,y,w,h
		return out, nil
	}

	out := intersectBox(src, mb)
	return out, nil
}

// InternalName returns a PDF usable name such as /F1
func (imgf *Imagefile) InternalName() string {
	return fmt.Sprintf("/ImgBag%d", imgf.id)
}

func finishPDF(imgf *Imagefile) error {
	_, err := imgf.pdfimporter.ImportPage(imgf.PageNumber, imgf.Box)
	if err != nil {
		return err
	}

	_, err = imgf.pdfimporter.PutFormXobjects()
	if err != nil {
		return err
	}

	imported := imgf.pdfimporter.GetImportedObjects()
	for i, v := range imported {
		o := imgf.pw.NewObjectWithNumber(Objectnumber(i))
		o.Raw = true
		o.Data = bytes.NewBuffer(v)
		o.Save()
	}
	return nil
}

func haveSMask(imginfo *Imagefile) bool {
	return len(imginfo.smask) > 0
}

func finishBitmap(imgf *Imagefile) error {
	d := Dict{
		"Type":             "/XObject",
		"Subtype":          "/Image",
		"BitsPerComponent": imgf.bitsPerComponent,
		"ColorSpace":       "/" + imgf.colorspace,
		"Width":            fmt.Sprintf("%d", imgf.W),
		"Height":           fmt.Sprintf("%d", imgf.H),
	}

	if len(imgf.trns) > 0 {
		j := 0
		content := []byte{}
		max := len(imgf.trns)
		for j < max {
			content = append(content, imgf.trns[j])
			content = append(content, imgf.trns[j])
			j++
		}
		d["Mask"] = content
	}
	if haveSMask(imgf) {
		objnum := imgf.createSMaskObject()
		d["SMask"] = objnum.Ref()
	}

	if imgf.colorspace == "Indexed" {
		size := len(imgf.pal)/3 - 1
		palObj := imgf.pw.NewObject()
		palObj.Data.Write(imgf.pal)
		if err := palObj.Save(); err != nil {
			return err
		}
		d["ColorSpace"] = fmt.Sprintf("[/Indexed /DeviceRGB %d %s]", size, palObj.ObjectNumber.Ref())
	}
	if imgf.decodeParms != nil {
		d["DecodeParms"] = imgf.decodeParms
	}
	imgo := imgf.imageobject

	imgo.Dict(d)
	switch imgf.Format {
	case "png":
		// imgf.data is /FlateDecoded compressed, so we need to add the Filter entry:
		imgo.Dictionary["Filter"] = "/FlateDecode"
		imgo.Data = bytes.NewBuffer(imgf.data)
	case "jpeg":
		imgo.Dictionary["Filter"] = "/DCTDecode"
		imgf.r.Seek(0, io.SeekStart)
		data, err := io.ReadAll(imgf.r)
		if err != nil {
			return err
		}
		imgo.Data = bytes.NewBuffer(data)
	}
	imgo.Save()

	return nil
}

func (imgf *Imagefile) finish() error {
	Logger.Info("Write image to PDF", "filename", imgf.Filename)
	if imgf.Format == "pdf" {
		return finishPDF(imgf)
	}
	if imgf.imageobject == nil {
		imgf.imageobject = imgf.pw.NewObject()
	}
	return finishBitmap(imgf)
}
