package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The PNG decoder is copied from https://github.com/signintech/gopdf and
// adapted to the needs for boxesandglue. gopdf is covered by this license:

// The MIT License (MIT)
//
// Copyright (c) 2015 signintech
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

func readUInt(f io.Reader) (uint, error) {
	buff, err := readBytes(f, 4)
	if err != nil {
		return 0, err
	}
	n := binary.BigEndian.Uint32(buff)
	return uint(n), nil
}

func readInt(f io.Reader) (int, error) {
	u, err := readUInt(f)
	if err != nil {
		return 0, err
	}
	return int(u), nil
}

func readBytes(f io.Reader, len int) ([]byte, error) {
	b := make([]byte, len)
	_, err := f.Read(b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func readByte(f io.Reader) (byte, error) {
	b := make([]byte, 1)
	_, err := f.Read(b)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

const (
	colGrayScale          byte = 0
	colTrueColor          byte = 2
	colIndexedColor       byte = 3
	colGrayScaleWithAlpha byte = 4
	colTrueColorWithAlpha byte = 6
)

// from gopdf
func (imgf *Imagefile) parsePNG() error {
	imgf.r.Seek(0, io.SeekStart)
	b, err := readBytes(imgf.r, 8)
	if err != nil {
		return err
	}
	if !bytes.Equal(b, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}) {
		return errors.New("Not a PNG file")
	}

	imgf.r.Seek(4, io.SeekCurrent) // skip header chunk
	b, err = readBytes(imgf.r, 4)
	if err != nil {
		return err
	}
	// IHDR
	if !bytes.Equal(b, []byte{0x49, 0x48, 0x44, 0x52}) {
		return errors.New("Incorrect PNG file")
	}

	w, err := readInt(imgf.r)
	if err != nil {
		return err
	}
	h, err := readInt(imgf.r)
	if err != nil {
		return err
	}
	imgf.W = w
	imgf.H = h

	bpc, err := readByte(imgf.r)
	if err != nil {
		return err
	}

	ct, err := readByte(imgf.r)
	if err != nil {
		return err
	}

	var colspace string
	switch ct {
	case colGrayScale, colGrayScaleWithAlpha:
		colspace = "DeviceGray"
	case colTrueColor, colTrueColorWithAlpha:
		colspace = "DeviceRGB"
	case colIndexedColor:
		colspace = "Indexed"
	default:
		return errors.New("Unknown color type")
	}
	if !validBitDepth(ct, bpc) {
		return fmt.Errorf("bit depth %d not allowed for PNG color type %d", bpc, ct)
	}

	compressionMethod, err := readByte(imgf.r)
	if err != nil {
		return err
	}
	if compressionMethod != 0 {
		return errors.New("Unknown compression method")
	}

	filterMethod, err := readByte(imgf.r)
	if err != nil {
		return err
	}
	if filterMethod != 0 {
		return errors.New("Unknown filter method")
	}

	interlacing, err := readByte(imgf.r)
	if err != nil {
		return err
	}
	if interlacing != 0 {
		return errors.New("Interlacing not supported")
	}

	_, err = imgf.r.Seek(4, io.SeekCurrent)
	if err != nil {
		return err
	}

	var pal []byte
	var trns []int
	var data []byte
	for {
		un, err := readUInt(imgf.r)
		if err != nil {
			return err
		}
		n := int(un)
		typ, err := readBytes(imgf.r, 4)
		if err != nil {
			return err
		}

		if string(typ) == "PLTE" {
			if pal, err = readBytes(imgf.r, n); err != nil {
				return err
			}
			if _, err = imgf.r.Seek(int64(4), io.SeekCurrent); err != nil {
				return err
			}
		} else if string(typ) == "tRNS" { // Transparency
			var t []byte
			t, err = readBytes(imgf.r, n)
			if err != nil {
				return err
			}

			// Gray and RGB samples are two bytes each whatever the bit depth.
			switch ct {
			case colGrayScale:
				if len(t) < 2 {
					return errors.New("Invalid tRNS chunk")
				}
				trns = []int{int(binary.BigEndian.Uint16(t))}
			case colTrueColor:
				if len(t) < 6 {
					return errors.New("Invalid tRNS chunk")
				}
				trns = []int{
					int(binary.BigEndian.Uint16(t[0:])),
					int(binary.BigEndian.Uint16(t[2:])),
					int(binary.BigEndian.Uint16(t[4:])),
				}
			default:
				pos := strings.Index(string(t), "\x00")
				if pos >= 0 {
					trns = []int{pos}
				}
			}

			_, err = imgf.r.Seek(int64(4), io.SeekCurrent)
			if err != nil {
				return err
			}

		} else if string(typ) == "IDAT" { // Image data
			var d []byte
			d, err = readBytes(imgf.r, n)
			if err != nil {
				return err
			}
			data = append(data, d...)
			_, err = imgf.r.Seek(int64(4), io.SeekCurrent)
			if err != nil {
				return err
			}
		} else if string(typ) == "IEND" { // Image trailer
			break
		} else {
			_, err = imgf.r.Seek(int64(n+4), io.SeekCurrent)
			if err != nil {
				return err
			}
		}

		if n <= 0 {
			break
		}
	} // end for

	imgf.trns = trns
	imgf.pal = pal

	if colspace == "Indexed" && strings.TrimSpace(string(pal)) == "" {
		return errors.New("Missing palette")
	}

	imgf.colorspace = colspace
	imgf.bitsPerComponent = fmt.Sprintf("%d", bpc)

	imgf.decodeParms = Dict{
		"Predictor": 15,
		"Columns":   w,
	}
	if colspace == "DeviceRGB" {
		imgf.decodeParms["Colors"] = 3
	}
	imgf.decodeParmsSmask = Dict{
		"Predictor": 15,
		"Columns":   w,
		"Colors":    1,
	}

	if bpc != 8 {
		imgf.decodeParms["BitsPerComponent"] = imgf.bitsPerComponent
		imgf.decodeParmsSmask["BitsPerComponent"] = imgf.bitsPerComponent
	}

	if ct < colGrayScaleWithAlpha {
		// no alpha
		imgf.data = data
		return nil
	}

	zipReader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer zipReader.Close()
	afterZipData, err := io.ReadAll(zipReader)
	if err != nil {
		return err
	}

	// Split the filtered rows into color and alpha. Each filter works on a
	// byte and the same byte of the previous pixel, so the split rows stay
	// valid for /Predictor 15. Images with alpha are 8 or 16 bit.
	sampleBytes := int(bpc) / 8
	colorBytes := sampleBytes
	if ct == colTrueColorWithAlpha {
		colorBytes = 3 * sampleBytes
	}
	pixelBytes := colorBytes + sampleBytes
	length := pixelBytes * w
	if len(afterZipData) < (1+length)*h {
		return errors.New("PNG image data too short")
	}
	color := make([]byte, 0, (1+colorBytes*w)*h)
	alpha := make([]byte, 0, (1+sampleBytes*w)*h)
	for i := range h {
		pos := (1 + length) * i
		color = append(color, afterZipData[pos])
		alpha = append(alpha, afterZipData[pos])
		line := afterZipData[pos+1 : pos+length+1]
		for j := 0; j < len(line); j += pixelBytes {
			color = append(color, line[j:j+colorBytes]...)
			alpha = append(alpha, line[j+colorBytes:j+pixelBytes]...)
		}
	}
	// alpha and color are non-compressed
	imgf.smask = alpha
	if imgf.data, err = compress(color); err != nil {
		return err
	}

	return nil
}

// validBitDepth reports whether the PNG specification allows bit depth bpc
// for color type ct.
func validBitDepth(ct, bpc byte) bool {
	switch ct {
	case colGrayScale:
		return bpc == 1 || bpc == 2 || bpc == 4 || bpc == 8 || bpc == 16
	case colIndexedColor:
		return bpc == 1 || bpc == 2 || bpc == 4 || bpc == 8
	default:
		return bpc == 8 || bpc == 16
	}
}

func compress(data []byte) ([]byte, error) {
	var results []byte
	var buff bytes.Buffer
	zwr, err := zlib.NewWriterLevel(&buff, zlib.BestSpeed)
	if err != nil {
		return results, err
	}
	_, err = zwr.Write(data)
	if err != nil {
		return nil, err
	}
	zwr.Close()
	return buff.Bytes(), nil
}
