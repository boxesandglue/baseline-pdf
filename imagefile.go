package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"image"
	"image/color"

	// Packages image/jpeg and image/png are not used explicitly in the code below,
	// but are imported for their initialization side-effect, which allows
	// image.Decode to understand JPEG formatted images.
	_ "image/jpeg"
	_ "image/png"

	"github.com/speedata/gofpdi"
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
	r                io.ReadSeeker
	pdfimporter      *gofpdi.Importer
	pw               *PDF
	imageobject      *Object
	id               int
	colorspace       string
	bitsPerComponent string
	filter           string
	trns             []byte
	smask            []byte
	pal              []byte
	decodeParms      string
	data             []byte
}

// LoadImageFile loads an image from the disc.
func LoadImageFile(pw *PDF, filename string) (*Imagefile, error) {
	if l := pw.Logger; l != nil {
		l.Infof("Load image %s", filename)
	}
	r, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	imgCfg, format, err := image.DecodeConfig(r)
	if errors.Is(err, image.ErrFormat) {
		// let's try PDF
		return tryParsePDF(pw, r, filename)
	}
	if err != nil {
		return nil, err
	}

	imgf := &Imagefile{
		Filename:      filename,
		Format:        format,
		id:            <-ids,
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
	imgf.filter = "DCTDecode"
	imgf.W = imgCfg.Width
	imgf.H = imgCfg.Height
	return nil
}

func tryParsePDF(pw *PDF, r io.ReadSeeker, filename string) (*Imagefile, error) {
	r.Seek(0, io.SeekStart)
	b, err := readBytes(r, 4)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal([]byte("%PDF"), b) {
		return nil, fmt.Errorf("%w: %s", image.ErrFormat, filename)
	}
	imgf := &Imagefile{
		Filename: filename,
		Format:   "pdf",
		id:       <-ids,
		pw:       pw,
		r:        r,
	}

	imgf.pdfimporter = gofpdi.NewImporter()

	f := func() int {
		if imgf.imageobject == nil {
			imgf.imageobject = pw.NewObject()
			return int(imgf.imageobject.ObjectNumber)
		}
		return int(pw.NewObject().ObjectNumber)
	}
	imgf.pdfimporter.SetObjIdGetter(f)
	imgf.pdfimporter.SetSourceStream(r)
	ps, err := imgf.pdfimporter.GetPageSizes()
	if err != nil {
		return nil, err
	}
	imgf.PageSizes = ps
	box := ps[1]["/MediaBox"]
	imgf.ScaleX = float64(box["w"])
	imgf.ScaleY = float64(box["h"])
	if imgf.NumberOfPages, err = imgf.pdfimporter.GetNumPages(); err != nil {
		return nil, err
	}
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

// GetPDFBoxDimensions returns the dimensions for the given box. Box must be one
// of "/MediaBox", "/CropBox", "/BleedBox", "/TrimBox", "/ArtBox".
func (imgf *Imagefile) GetPDFBoxDimensions(p int, boxname string) (map[string]float64, error) {
	if p > imgf.NumberOfPages {
		return nil, fmt.Errorf("cannot get the page number %d of the PDF, the PDF has only %d page(s)", p, imgf.NumberOfPages)
	}
	bx := imgf.PageSizes[p][boxname]
	if len(bx) == 0 {
		if boxname == "/CropBox" {
			return imgf.PageSizes[p]["/MediaBox"], nil
		}
		switch boxname {
		case "/ArtBox", "/BleedBox", "/TrimBox":
			return imgf.GetPDFBoxDimensions(p, "/CropBox")
		default:
			// unknown box dimensions
			return nil, fmt.Errorf("could not find the box dimensions for the image (box %s)", boxname)
		}
	}
	if boxname == "/MediaBox" {
		return bx, nil
	}
	return intersectBox(bx, imgf.PageSizes[p]["/MediaBox"]), nil
}

// InternalName returns a PDF usable name such as /F1
func (imgf *Imagefile) InternalName() string {
	return fmt.Sprintf("/ImgBag%d", imgf.id)
}

func (imgf *Imagefile) finish() error {
	pw := imgf.pw
	if l := pw.Logger; l != nil {
		l.Infof("Write image %s to PDF", imgf.Filename)
	}
	imgo := imgf.imageobject

	if imgf.Format == "pdf" {
		_, err := imgf.pdfimporter.ImportPage(1, "/MediaBox")
		if err != nil {
			return err
		}

		_, err = imgf.pdfimporter.PutFormXobjects()
		if err != nil {
			return err
		}

		imported := imgf.pdfimporter.GetImportedObjects()
		for i, v := range imported {
			o := pw.NewObjectWithNumber(Objectnumber(i))
			o.Raw = true
			o.Data = bytes.NewBuffer(v)
			o.Save()
		}
		return nil
	}
	d := Dict{
		"Type":             "/XObject",
		"Subtype":          "/Image",
		"BitsPerComponent": imgf.bitsPerComponent,
		"ColorSpace":       "/" + imgf.colorspace,
		"Width":            fmt.Sprintf("%d", imgf.W),
		"Height":           fmt.Sprintf("%d", imgf.H),
		"Filter":           "/" + imgf.filter,
	}

	if imgf.colorspace == "Indexed" {
		size := len(imgf.pal)/3 - 1
		palObj := pw.NewObject()
		palObj.Data.Write(imgf.pal)
		palObj.SetCompression(9)
		if err := palObj.Save(); err != nil {
			return err
		}
		d["/ColorSpace"] = fmt.Sprintf("[/Indexed /DeviceRGB %d %s]", size, palObj.ObjectNumber.Ref())
		if imgf.decodeParms != "" {
			d["/DecodeParms"] = fmt.Sprintf("<<%s>>", imgf.decodeParms)
		}
	}
	imgo.Dict(d)
	switch imgf.Format {
	case "png":
		imgo.Data = bytes.NewBuffer(imgf.data)
	case "jpeg":
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
