package jpx

import (
	"errors"
	"fmt"
)

// The JP2 file format (T.800 Annex I) wraps a codestream in boxes that
// carry, among other things, the colour interpretation. PDFs embed either
// a JP2 file or a bare codestream.

// jp2 is what the decoder takes from a JP2 file's header boxes.
type jp2 struct {
	codestream []byte
	colour     ColorSpace
	palette    *palette
	mapping    []channelMap // cmap: output channel → component or palette column
	defs       []channelDef // cdef
}

// palette is a pclr box: entries rows of columns values.
type palette struct {
	entries   int
	columns   int
	precision []int // per column
	signed    []bool
	values    []int // entries × columns, row-major
}

type channelMap struct {
	comp    int
	palette bool
	column  int
}

// channelDef is one cdef entry: channel i is colour (typ 0) or opacity
// (1, 2) and is associated with colour number assoc (1-based; 0 the whole
// image).
type channelDef struct {
	channel, typ, assoc int
}

// maxPaletteEntries is the JP2 limit on palette entries.
const maxPaletteEntries = 1024

// parseJP2 finds the codestream and colour boxes of a JP2 file. A bare
// codestream is returned as is.
func parseJP2(data []byte) (*jp2, error) {
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0x4F {
		return &jp2{codestream: data}, nil
	}
	j := &jp2{}
	if err := j.boxes(data, 0); err != nil {
		return nil, err
	}
	if j.codestream == nil {
		return nil, errors.New("jpx: no codestream")
	}
	return j, nil
}

func (j *jp2) boxes(data []byte, depth int) error {
	if depth > 4 {
		return errors.New("jpx: boxes nested too deeply")
	}
	for len(data) >= 8 {
		size := int(uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3]))
		typ := string(data[4:8])
		head := 8
		switch size {
		case 0:
			size = len(data)
		case 1:
			if len(data) < 16 {
				return errors.New("jpx: truncated box header")
			}
			var big uint64
			for _, b := range data[8:16] {
				big = big<<8 | uint64(b)
			}
			if big > uint64(len(data)) {
				size = len(data) + 1 // overruns; handled below
			} else {
				size = int(big)
			}
			head = 16
		}
		if size < head {
			return errors.New("jpx: invalid box length")
		}
		if size > len(data) {
			if typ != "jp2c" {
				return fmt.Errorf("jpx: %q box overruns the data", typ)
			}
			size = len(data) // a truncated codestream: decode what is there
		}
		body := data[head:size]
		switch typ {
		case "jp2h", "res ":
			if typ == "jp2h" {
				if err := j.boxes(body, depth+1); err != nil {
					return err
				}
			}
		case "jp2c":
			if j.codestream == nil {
				j.codestream = body
			}
		case "colr":
			if j.colour == Unknown {
				j.colour = parseColr(body)
			}
		case "pclr":
			p, err := parsePclr(body)
			if err != nil {
				return err
			}
			j.palette = p
		case "cmap":
			for i := 0; i+4 <= len(body); i += 4 {
				j.mapping = append(j.mapping, channelMap{
					comp:    int(body[i])<<8 | int(body[i+1]),
					palette: body[i+2] == 1,
					column:  int(body[i+3]),
				})
			}
		case "cdef":
			if len(body) < 2 {
				break
			}
			n := int(body[0])<<8 | int(body[1])
			for i := range n {
				o := 2 + 6*i
				if o+6 > len(body) {
					break
				}
				j.defs = append(j.defs, channelDef{
					channel: int(body[o])<<8 | int(body[o+1]),
					typ:     int(body[o+2])<<8 | int(body[o+3]),
					assoc:   int(body[o+4])<<8 | int(body[o+5]),
				})
			}
		}
		data = data[size:]
	}
	return nil
}

// parseColr reads a colr box's colour space: an enumerated space, or the
// data colour space of a restricted ICC profile.
func parseColr(b []byte) ColorSpace {
	if len(b) < 3 {
		return Unknown
	}
	switch b[0] {
	case 1:
		if len(b) < 7 {
			return Unknown
		}
		switch uint32(b[3])<<24 | uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6]) {
		case 16, 20, 21: // sRGB, e-sRGB, ROMM-RGB
			return RGB
		case 17: // greyscale
			return Gray
		case 18, 24: // sYCC, e-sYCC
			return YCC
		case 12: // CMYK
			return CMYK
		}
	case 2, 3:
		// An ICC profile follows; its header names the data colour space
		// at byte 16.
		if icc := b[3:]; len(icc) >= 20 {
			switch string(icc[16:20]) {
			case "RGB ":
				return RGB
			case "GRAY":
				return Gray
			case "CMYK":
				return CMYK
			case "YCbr":
				return YCC
			}
		}
	}
	return Unknown
}

func parsePclr(b []byte) (*palette, error) {
	if len(b) < 3 {
		return nil, errors.New("jpx: truncated pclr box")
	}
	p := &palette{entries: int(b[0])<<8 | int(b[1]), columns: int(b[2])}
	if p.entries < 1 || p.entries > maxPaletteEntries || p.columns < 1 {
		return nil, errors.New("jpx: invalid pclr box")
	}
	pos := 3
	for range p.columns {
		if pos >= len(b) {
			return nil, errors.New("jpx: truncated pclr box")
		}
		p.precision = append(p.precision, int(b[pos]&0x7F)+1)
		p.signed = append(p.signed, b[pos]&0x80 != 0)
		pos++
	}
	for range p.entries {
		for c := range p.columns {
			n := (p.precision[c] + 7) / 8
			if n > 4 || pos+n > len(b) {
				return nil, errors.New("jpx: truncated pclr box")
			}
			v := 0
			for _, x := range b[pos : pos+n] {
				v = v<<8 | int(x)
			}
			p.values = append(p.values, v)
			pos += n
		}
	}
	return p, nil
}
