package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Helpers ----------------------------------------------------------------

// --- Minimal PDF generator for tests ----------------------------------------

// makeMinimalPDF builds a valid one-page PDF with given MediaBox and CropBox.
func makeMinimalPDF(mb [4]float64, cb [4]float64) []byte {
	var buf bytes.Buffer
	write := func(s string) { buf.WriteString(s) }

	// Header
	write("%PDF-1.4\n")

	// We'll accumulate object contents and track byte offsets.
	type obj struct {
		n   int
		raw string
	}
	objs := []obj{
		{
			1,
			"<< /Type /Catalog /Pages 2 0 R >>",
		},
		{
			2,
			"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		},
		{
			3,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /Resources <<>> "+
				"/MediaBox [%g %g %g %g] /CropBox [%g %g %g %g] /Contents 4 0 R >>",
				mb[0], mb[1], mb[2], mb[3],
				cb[0], cb[1], cb[2], cb[3],
			),
		},
		{
			4,
			"<< /Length 0 >>\nstream\n\nendstream",
		},
	}

	offsets := make([]int, len(objs)+1) // index by object number; 0th is the free object
	// Write objects and record their starting offsets.
	for _, o := range objs {
		offsets[o.n] = buf.Len()
		write(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", o.n, o.raw))
	}

	// xref table
	xrefPos := buf.Len()
	write("xref\n")
	write(fmt.Sprintf("0 %d\n", len(objs)+1))
	// Free object 0
	write("0000000000 65535 f \n")
	// Each real object
	for i := 1; i <= len(objs); i++ {
		write(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}

	// trailer, startxref, EOF
	write(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\n", len(objs)+1))
	write(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefPos))

	return buf.Bytes()
}

// writeTempJPEG creates a temporary JPEG image of the given size.
func writeTempJPEG(t *testing.T, dir string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 0, 255})
		}
	}
	fn := filepath.Join(dir, "test.jpg")
	f, err := os.Create(fn)
	if err != nil {
		t.Fatalf("create %s: %v", fn, err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return fn
}

// writeTempPNG creates a temporary PNG image (optionally with alpha).
func writeTempPNG(t *testing.T, dir string, w, h int, withAlpha bool) string {
	t.Helper()
	var img image.Image
	if withAlpha {
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				a := uint8((x + y) % 256)
				rgba.Set(x, y, color.NRGBA{R: 10, G: 20, B: 200, A: a})
			}
		}
		img = rgba
	} else {
		rgb := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				rgb.Set(x, y, color.NRGBA{R: 200, G: 30, B: 10, A: 255})
			}
		}
		img = rgb
	}

	fn := filepath.Join(dir, "test.png")
	f, err := os.Create(fn)
	if err != nil {
		t.Fatalf("create %s: %v", fn, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return fn
}

// --- Tests for image loading ------------------------------------------------

func TestLoadImageFileWithBox_JPEG(t *testing.T) {
	td := t.TempDir()
	fn := writeTempJPEG(t, td, 37, 19)
	pdfw := &PDF{}

	img, err := pdfw.LoadImageFileWithBox(fn, "/MediaBox", 1)
	if err != nil {
		t.Fatalf("LoadImageFileWithBox jpeg: %v", err)
	}

	if img.Format != "jpeg" {
		t.Fatalf("expected format=jpeg, got %q", img.Format)
	}
	if img.W != 37 || img.H != 19 {
		t.Fatalf("expected size 37x19, got %dx%d", img.W, img.H)
	}
	if img.bitsPerComponent != "8" {
		t.Fatalf("expected bpc=8, got %q", img.bitsPerComponent)
	}
	if img.NumberOfPages != 1 {
		t.Fatalf("expected NumberOfPages=1, got %d", img.NumberOfPages)
	}
	if img.ScaleX != 1 || img.ScaleY != 1 {
		t.Fatalf("expected ScaleX/ScaleY=1, got %v/%v", img.ScaleX, img.ScaleY)
	}
}

func TestLoadImageFileWithBox_PNG(t *testing.T) {
	td := t.TempDir()
	fn := writeTempPNG(t, td, 16, 8, true)
	pdfw := &PDF{}

	img, err := pdfw.LoadImageFileWithBox(fn, "/MediaBox", 1)
	if err != nil {
		t.Fatalf("LoadImageFileWithBox png: %v", err)
	}

	if img.Format != "png" {
		t.Fatalf("expected format=png, got %q", img.Format)
	}
	if img.W != 16 || img.H != 8 {
		t.Fatalf("expected size 16x8, got %dx%d", img.W, img.H)
	}
	if img.colorspace == "" {
		t.Fatalf("expected non-empty colorspace")
	}
	if img.bitsPerComponent == "" {
		t.Fatalf("expected non-empty bitsPerComponent")
	}
}

func TestLoadImageFileWithBox_NotImageAndNotPDF_YieldsErrFormat(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "notimg.bin")
	if err := os.WriteFile(fn, []byte("not-an-image"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	pdfw := &PDF{}
	_, err := pdfw.LoadImageFileWithBox(fn, "/MediaBox", 1)
	if !errors.Is(err, image.ErrFormat) {
		t.Fatalf("expected image.ErrFormat, got %v", err)
	}
}

// --- Tests for box handling -------------------------------------------------

func TestGetPDFBoxDimensions_IntersectAndFallback(t *testing.T) {
	imgf := &Imagefile{
		NumberOfPages: 1,
		PageSizes: map[int]map[string]map[string]float64{
			1: {
				"/MediaBox": {"llx": 0, "lly": 0, "urx": 200, "ury": 100},
				"/CropBox":  {"llx": -10, "lly": 5, "urx": 210, "ury": 120},
			},
		},
	}

	mb, err := imgf.GetPDFBoxDimensions(1, "/MediaBox")
	if err != nil {
		t.Fatalf("MediaBox err: %v", err)
	}
	if mb["w"] != 200 || mb["h"] != 100 {
		t.Fatalf("MediaBox w/h expected 200/100, got %v/%v", mb["w"], mb["h"])
	}

	cb, err := imgf.GetPDFBoxDimensions(1, "/CropBox")
	if err != nil {
		t.Fatalf("CropBox err: %v", err)
	}
	if cb["llx"] != 0 || cb["lly"] != 5 || cb["urx"] != 200 || cb["ury"] != 100 {
		t.Fatalf("CropBox intersect wrong: %+v", cb)
	}
	if cb["w"] != 200 || cb["h"] != 95 {
		t.Fatalf("CropBox w/h expected 200/95, got %v/%v", cb["w"], cb["h"])
	}

	tb, err := imgf.GetPDFBoxDimensions(1, "/TrimBox")
	if err != nil {
		t.Fatalf("TrimBox err: %v", err)
	}
	if tb["w"] != 200 || tb["h"] != 95 {
		t.Fatalf("TrimBox fallback wrong size: %v/%v", tb["w"], tb["h"])
	}
}

func TestGetPDFBoxDimensions_PageOutOfRange(t *testing.T) {
	imgf := &Imagefile{NumberOfPages: 2}
	_, err := imgf.GetPDFBoxDimensions(3, "/MediaBox")
	if err == nil {
		t.Fatalf("expected error for invalid page number")
	}
}

// --- Tests for small helpers ------------------------------------------------

func TestInternalName_AndHaveSMask(t *testing.T) {
	img := &Imagefile{id: 42}
	if got := img.InternalName(); got != "/ImgBag42" {
		t.Fatalf("InternalName mismatch: got %q", got)
	}

	if haveSMask(img) {
		t.Fatalf("expected haveSMask=false for nil smask")
	}
	img.smask = []byte{0x00, 0x10}
	if !haveSMask(img) {
		t.Fatalf("expected haveSMask=true when smask present")
	}
}

func TestIntersectBox(t *testing.T) {
	mb := map[string]float64{"llx": 0, "lly": 0, "urx": 200, "ury": 100}
	bx := map[string]float64{"llx": -5, "lly": 10, "urx": 250, "ury": 90}
	got := intersectBox(bx, mb)
	if got["llx"] != 0 || got["lly"] != 10 || got["urx"] != 200 || got["ury"] != 90 {
		t.Fatalf("intersect mismatch: %+v", got)
	}
	if got["x"] != got["llx"] || got["y"] != got["lly"] || got["w"] != 200 || got["h"] != 80 {
		t.Fatalf("x/y/w/h not set correctly: %+v", got)
	}
}

func writeBytes(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	fn := filepath.Join(dir, name)
	if err := os.WriteFile(fn, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", fn, err)
	}
	return fn
}

// This test ensures that a non-PDF header is properly rejected after DecodeConfig fails.
func TestNotPDFHeaderPath(t *testing.T) {
	td := t.TempDir()
	fn := writeBytes(t, td, "raw.bin", []byte{0x25, 0x50, 0x44, 0x00}) // "%PD\000"
	pdfw := &PDF{}
	_, err := pdfw.LoadImageFileWithBox(fn, "/MediaBox", 1)
	if !errors.Is(err, image.ErrFormat) {
		t.Fatalf("expected image.ErrFormat, got %v", err)
	}
}

func TestInternalName_UniquenessPattern(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 3; i++ {
		img := &Imagefile{id: i + 1}
		buf.WriteString(img.InternalName())
	}
	want := "/ImgBag1/ImgBag2/ImgBag3"
	if buf.String() != want {
		t.Fatalf("unexpected concatenation: %q", buf.String())
	}
}

func TestTryParsePDFWithBox_Basic(t *testing.T) {
	mb := [4]float64{0, 0, 200, 100}
	cb := [4]float64{10, 5, 180, 90}
	pdfBytes := makeMinimalPDF(mb, cb)

	pw := &PDF{}
	r := bytes.NewReader(pdfBytes)

	imgf, err := tryParsePDFWithBox(pw, r, "mem.pdf", "/CropBox", 1)
	if err != nil {
		t.Fatalf("tryParsePDFWithBox: %v", err)
	}

	// Basic metadata
	if imgf.Format != "pdf" {
		t.Fatalf("expected format=pdf, got %q", imgf.Format)
	}
	if imgf.NumberOfPages != 1 {
		t.Fatalf("expected NumberOfPages=1, got %d", imgf.NumberOfPages)
	}

	// Page sizes presence
	ps := imgf.PageSizes
	if ps == nil || ps[1] == nil {
		t.Fatalf("expected PageSizes for page 1")
	}
	mbox := ps[1]["/MediaBox"]
	cbox := ps[1]["/CropBox"]
	if len(mbox) == 0 || len(cbox) == 0 {
		t.Fatalf("expected both MediaBox and CropBox")
	}

	// Box numbers should match what we encoded
	if mbox["llx"] != mb[0] || mbox["lly"] != mb[1] || mbox["urx"] != mb[2] || mbox["ury"] != mb[3] {
		t.Fatalf("MediaBox mismatch: %+v", mbox)
	}
	if cbox["llx"] != cb[0] || cbox["lly"] != cb[1] || cbox["urx"] != cb[2] || cbox["ury"] != cb[3] {
		t.Fatalf("CropBox mismatch: %+v", cbox)
	}

	// GetPDFBoxDimensions + ScaleX/ScaleY derived during tryParsePDFWithBox
	got, err := imgf.GetPDFBoxDimensions(1, "/CropBox")
	if err != nil {
		t.Fatalf("GetPDFBoxDimensions: %v", err)
	}
	// intersectBox should clamp to MediaBox; here CropBox is inside, so w/h are (180-10)=170, (90-5)=85
	if got["w"] != 170 || got["h"] != 85 {
		t.Fatalf("expected w/h 170/85, got %v/%v", got["w"], got["h"])
	}
	if imgf.ScaleX != 170 || imgf.ScaleY != 85 {
		t.Fatalf("expected ScaleX/ScaleY 170/85, got %v/%v", imgf.ScaleX, imgf.ScaleY)
	}
}

func TestTryParsePDFWithBox_InvalidPageErrors(t *testing.T) {
	mb := [4]float64{0, 0, 50, 50}
	cb := [4]float64{0, 0, 50, 50}
	pdfBytes := makeMinimalPDF(mb, cb)

	pw := &PDF{}
	r := bytes.NewReader(pdfBytes)
	imgf, err := tryParsePDFWithBox(pw, r, "mem.pdf", "/MediaBox", 1)
	if err != nil {
		t.Fatalf("tryParsePDFWithBox: %v", err)
	}

	// Asking for a non-existing page should error.
	if _, err := imgf.GetPDFBoxDimensions(2, "/MediaBox"); err == nil {
		t.Fatalf("expected error for page 2")
	}
}

func TestLoadImageFile_ChoosesPDFPathWhenImageDecodeFails(t *testing.T) {
	// This verifies the full LoadImageFileWithBox branch:
	// DecodeConfig fails with image.ErrFormat -> tryParsePDFWithBox is used.
	mb := [4]float64{0, 0, 123, 45}
	cb := [4]float64{5, 0, 120, 40}
	pdfBytes := makeMinimalPDF(mb, cb)

	td := t.TempDir()
	fn := filepath.Join(td, "tiny.pdf")
	if err := os.WriteFile(fn, pdfBytes, 0o644); err != nil {
		t.Fatalf("write tiny.pdf: %v", err)
	}

	pw := &PDF{}
	imgf, err := pw.LoadImageFileWithBox(fn, "/CropBox", 1)
	if err != nil {
		t.Fatalf("LoadImageFileWithBox(pdf): %v", err)
	}
	if imgf.Format != "pdf" {
		t.Fatalf("expected format=pdf, got %q", imgf.Format)
	}
}

// --- Tests for finishPDF and finishBitmap -----------------------------------

func TestFinishPDF_ImportsObjectsAndWritesBytes(t *testing.T) {
	// Build a tiny, valid one-page PDF to import.
	mb := [4]float64{0, 0, 200, 100}
	cb := [4]float64{10, 5, 180, 90}
	src := makeMinimalPDF(mb, cb)

	// Use an in-memory writer for the new PDF.
	var out bytes.Buffer
	pw := NewPDFWriter(&out)

	// Parse the source as an "image" (PDF import mode).
	r := bytes.NewReader(src)
	imgf, err := tryParsePDFWithBox(pw, r, "mem.pdf", "/CropBox", 1)
	if err != nil {
		t.Fatalf("tryParsePDFWithBox: %v", err)
	}
	pw.AddPage(pw.NewObject(), 0)
	// Act: import the page objects into the writer.
	if err := finishPDF(imgf); err != nil {
		t.Fatalf("finishPDF: %v", err)
	}
	pw.FinishAndClose()

	// We haven't finished the whole PDF yet, but importing already writes raw objects.
	got := out.String()
	if !strings.Contains(got, "%PDF-") {
		// The object writer will emit the header on first write; require it.
		t.Fatalf("expected PDF header to be written")
	}
	if !strings.Contains(got, "obj") {
		t.Fatalf("expected at least one object after finishPDF")
	}

	// Optionally complete the file (xref + trailer) to assert it finalizes cleanly.
	if err := pw.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	final := out.String()
	if !strings.Contains(final, "xref") {
		t.Fatalf("expected xref section after Finish")
	}
}

func TestFinishBitmap_JPEG_WritesImageXObject(t *testing.T) {
	// Prepare a tiny JPEG in memory.
	img := image.NewGray(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x + y)})
		}
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: 70}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}

	var out bytes.Buffer
	pw := NewPDFWriter(&out)

	// Build the Imagefile as if it had been loaded via LoadImageFileWithBox.
	imgf := &Imagefile{
		Format:           "jpeg",
		Filename:         "mem.jpg",
		ScaleX:           1,
		ScaleY:           1,
		W:                3,
		H:                2,
		pw:               pw,
		colorspace:       "DeviceRGB", // the JPEG encoder above will be decoded as YCbCr, mapped to RGB
		bitsPerComponent: "8",
		r:                bytes.NewReader(jpg.Bytes()),
		imageobject:      pw.NewObject(),
	}

	// Act: write the XObject.
	if err := finishBitmap(imgf); err != nil {
		t.Fatalf("finishBitmap(jpeg): %v", err)
	}

	// Assert: the PDF output should include an Image XObject with DCTDecode.
	pdf := out.String()
	if !strings.Contains(pdf, "/Subtype /Image") {
		t.Fatalf("expected /Subtype /Image in output")
	}
	if !strings.Contains(pdf, "/Filter /DCTDecode") {
		t.Fatalf("expected /Filter /DCTDecode for JPEG")
	}
	if !strings.Contains(pdf, "/ColorSpace /DeviceRGB") {
		t.Fatalf("expected /ColorSpace /DeviceRGB")
	}
	if !strings.Contains(pdf, "/Width 3") || !strings.Contains(pdf, "/Height 2") {
		t.Fatalf("expected correct Width/Height")
	}
}

func TestFinishBitmap_PNG_WritesFlateImageXObject(t *testing.T) {
	// Create a simple PNG (with alpha just to exercise SMask-paths if your parsePNG sets them).
	rgba := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			a := uint8(255)
			if (x+y)%2 == 0 {
				a = 200
			}
			rgba.SetRGBA(x, y, color.RGBA{R: 30, G: 60, B: 90, A: a})
		}
	}
	var pngbuf bytes.Buffer
	if err := png.Encode(&pngbuf, rgba); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	// Wire it as if it was loaded by your PNG parser:
	var out bytes.Buffer
	pw := NewPDFWriter(&out)

	imgf := &Imagefile{
		Format: "png",
		pw:     pw,
		r:      bytes.NewReader(pngbuf.Bytes()),
	}

	// Let the real PNG parser populate fields (colorspace, W/H, data, palettes, masks, etc.).
	// Ensure the reader is at the start.
	if _, err := imgf.r.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	imgf.parsePNG() // same package; allowed in tests

	// Prepare the target object and write.
	imgf.imageobject = pw.NewObject()
	if err := finishBitmap(imgf); err != nil {
		t.Fatalf("finishBitmap(png): %v", err)
	}

	// Assert: should be an Image XObject using FlateDecode.
	pdf := out.String()
	if !strings.Contains(pdf, "/Subtype /Image") {
		t.Fatalf("expected /Subtype /Image in output")
	}
	if !strings.Contains(pdf, "/Filter /FlateDecode") {
		t.Fatalf("expected /Filter /FlateDecode for PNG path")
	}
	if !strings.Contains(pdf, "/Width ") || !strings.Contains(pdf, "/Height ") {
		t.Fatalf("expected Width/Height in dictionary")
	}

	// If parsePNG produced an SMask, the dictionary should reference it.
	// This assertion is soft (optional), since not all PNGs yield SMask.
	if haveSMask(imgf) && !strings.Contains(pdf, "/SMask ") {
		t.Fatalf("SMask bytes exist but dictionary lacks /SMask reference")
	}
}
