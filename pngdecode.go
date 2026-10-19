package pdf

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
)

// paletteNeedsDecoding reports whether an indexed PNG with the tRNS alpha
// values alpha can't be passed through. The pass-through path can express
// exactly one fully transparent palette entry, as a color key. Partial
// alpha and several transparent entries need a soft mask, and below 8 bits
// macOS Quartz ignores a color key on an indexed image, so the indices are
// unpacked to 8 bits.
func paletteNeedsDecoding(bpc byte, alpha []byte) bool {
	zeros := 0
	for _, a := range alpha {
		switch a {
		case 0:
			zeros++
		case 255:
		default:
			return true
		}
	}
	return zeros > 1 || zeros == 1 && bpc < 8
}

// decodePNG is the fallback for PNG files the pass-through path can't
// write: interlaced images and indexed images with alpha (see
// paletteNeedsDecoding). It decodes the image with image/png and writes the
// samples without PNG filters, at 8 or 16 bits per component. Alpha becomes
// a soft mask. A color key from tRNS stays a color key, because PDF/X-3
// allows a color key but no soft mask. ct and bpc are the color type and
// bit depth from IHDR, trns the color key parsed from tRNS for gray and RGB.
func (imgf *Imagefile) decodePNG(ct, bpc byte, trns []int) error {
	if _, err := imgf.r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	img, err := png.Decode(imgf.r)
	if err != nil {
		return err
	}
	w, h := imgf.W, imgf.H
	if b := img.Bounds(); b.Min != (image.Point{}) || b.Dx() != w || b.Dy() != h {
		return errors.New("PNG image size does not match its header")
	}

	// rows copies n bytes per row from pix, picking the bytes of each pixel
	// of size pixelBytes from offset off on.
	rows := func(pix []byte, stride, pixelBytes, off, n int) []byte {
		out := make([]byte, 0, n*w*h)
		for y := range h {
			line := pix[y*stride : y*stride+pixelBytes*w]
			for x := 0; x < len(line); x += pixelBytes {
				out = append(out, line[x+off:x+off+n]...)
			}
		}
		return out
	}

	var samples, alpha []byte
	bits := 8
	imgf.trns = nil
	switch m := img.(type) {
	case *image.Paletted:
		pal := make([]byte, 0, 3*len(m.Palette))
		palAlpha := make([]byte, len(m.Palette))
		for i, c := range m.Palette {
			n := color.NRGBAModel.Convert(c).(color.NRGBA)
			pal = append(pal, n.R, n.G, n.B)
			palAlpha[i] = n.A
		}
		imgf.colorspace = "Indexed"
		imgf.pal = pal
		samples = rows(m.Pix, m.Stride, 1, 0, 1)
		if paletteNeedsDecoding(8, palAlpha) {
			alpha = make([]byte, len(samples))
			for i, idx := range samples {
				alpha[i] = palAlpha[idx]
			}
		} else {
			for i, a := range palAlpha {
				if a == 0 {
					imgf.trns = []int{i}
				}
			}
		}
	case *image.Gray:
		imgf.colorspace = "DeviceGray"
		samples = rows(m.Pix, m.Stride, 1, 0, 1)
	case *image.Gray16:
		imgf.colorspace = "DeviceGray"
		samples = rows(m.Pix, m.Stride, 2, 0, 2)
		bits = 16
	case *image.RGBA:
		imgf.colorspace = "DeviceRGB"
		samples = rows(m.Pix, m.Stride, 4, 0, 3)
	case *image.RGBA64:
		imgf.colorspace = "DeviceRGB"
		samples = rows(m.Pix, m.Stride, 8, 0, 6)
		bits = 16
	case *image.NRGBA, *image.NRGBA64:
		// Gray or RGB with a color key, gray with alpha or RGBA.
		var pix []byte
		var stride int
		if m8, ok := m.(*image.NRGBA); ok {
			pix, stride = m8.Pix, m8.Stride
		} else {
			m16 := m.(*image.NRGBA64)
			pix, stride, bits = m16.Pix, m16.Stride, 16
		}
		sb := bits / 8
		if ct == colGrayScale || ct == colGrayScaleWithAlpha {
			imgf.colorspace = "DeviceGray"
			samples = rows(pix, stride, 4*sb, 0, sb)
		} else {
			imgf.colorspace = "DeviceRGB"
			samples = rows(pix, stride, 4*sb, 0, 3*sb)
		}
		if ct == colGrayScaleWithAlpha || ct == colTrueColorWithAlpha {
			alpha = rows(pix, stride, 4*sb, 3*sb, sb)
		} else {
			// image/png scales gray below 8 bits up to 8 bits, the key too.
			imgf.trns = trns
			if bpc < 8 {
				imgf.trns = make([]int, len(trns))
				for i, v := range trns {
					imgf.trns[i] = v * 255 / (1<<bpc - 1)
				}
			}
		}
	default:
		return fmt.Errorf("PNG decodes to %T, which is not supported", img)
	}

	imgf.bitsPerComponent = fmt.Sprintf("%d", bits)
	imgf.decodeParms = nil
	imgf.decodeParmsSmask = nil
	// alpha is non-compressed, the data compressed (see finishBitmap)
	imgf.smask = alpha
	imgf.data, err = compress(samples)
	return err
}
