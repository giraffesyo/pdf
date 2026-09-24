// Package dct decodes the PDF DCTDecode filter's JPEG data (ISO 32000-1
// §7.4.8) through image/jpeg, bridging where PDF writers and image/jpeg
// disagree.
//
// image/jpeg decodes a four-component JPEG only with an Adobe APP14
// marker, and then takes the samples as Adobe's inverted CMYK, where 255
// is no ink. Writers that put CMYK JPEGs straight into PDFs (Ghostscript,
// many prepress tools) often omit the marker and store plain CMYK, where
// 255 is full ink; the PDF's DeviceCMYK colour space says what the
// samples are. Such data decodes here with a marker supplied and the
// samples read as written.
package dct

import (
	"bytes"
	"image"
	"image/jpeg"
)

// DecodeConfig returns the image's dimensions and colour model.
func DecodeConfig(data []byte) (image.Config, error) {
	return jpeg.DecodeConfig(bytes.NewReader(fixup(data)))
}

// Decode decodes the JPEG data. A four-component image yields *image.CMYK
// whose samples are ink amounts, 255 full ink, whether or not the data
// carried an Adobe marker.
func Decode(data []byte) (image.Image, error) {
	fixed := fixup(data)
	img, err := jpeg.Decode(bytes.NewReader(fixed))
	if err != nil {
		return nil, err
	}
	if len(fixed) != len(data) {
		// The supplied marker made image/jpeg invert plain samples.
		if c, ok := img.(*image.CMYK); ok {
			for i := range c.Pix {
				c.Pix[i] = ^c.Pix[i]
			}
		}
	}
	return img, nil
}

// adobeUntransformed is an APP14 "Adobe" segment with transform 0: the
// components are stored as they are, no YCC or YCCK conversion.
var adobeUntransformed = []byte{
	0xFF, 0xEE, 0x00, 0x0E, 'A', 'd', 'o', 'b', 'e',
	0x00, 0x64, // version 100
	0x00, 0x00, 0x00, 0x00, // flags
	0x00, // transform
}

// fixup returns data with an Adobe marker inserted after SOI when it is a
// four-component JPEG without one, and data unchanged otherwise.
func fixup(data []byte) []byte {
	if !plainFourComponent(data) {
		return data
	}
	out := make([]byte, 0, len(data)+len(adobeUntransformed))
	out = append(out, data[:2]...)
	out = append(out, adobeUntransformed...)
	return append(out, data[2:]...)
}

// plainFourComponent reports whether the header segments, up to the first
// scan, declare four components and carry no Adobe APP14 marker.
func plainFourComponent(data []byte) bool {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return false
	}
	four := false
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return false
		}
		marker := data[i+1]
		if marker == 0xFF { // fill byte
			i++
			continue
		}
		if marker == 0xD8 || marker >= 0xD0 && marker <= 0xD7 || marker == 0x01 {
			i += 2 // no length
			continue
		}
		if marker == 0xD9 { // EOI before any scan
			return four
		}
		n := int(data[i+2])<<8 | int(data[i+3])
		if n < 2 || i+2+n > len(data) {
			return false
		}
		seg := data[i+4 : i+2+n]
		switch {
		case marker == 0xEE && bytes.HasPrefix(seg, []byte("Adobe")):
			return false
		case marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC:
			four = len(seg) >= 6 && seg[5] == 4 // precision, height, width, components
		case marker == 0xDA:
			return four
		}
		i += 2 + n
	}
	return four
}
