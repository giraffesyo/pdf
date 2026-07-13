package pdf

import (
	"encoding/hex"
	"unicode/utf16"

	ldpdf "github.com/ledongthuc/pdf"
)

const maxCmapEntries = 100000

// parseToUnicode reads a /ToUnicode CMap stream into a code → text map.
// Returns nil when absent or unparsable.
func parseToUnicode(v ldpdf.Value) map[uint32]string {
	if v.Kind() != ldpdf.Stream {
		return nil
	}
	data := readStreamBounded(v)
	if data == nil {
		return nil
	}
	toks := lexCMap(data)
	m := map[uint32]string{}
	for i := 0; i < len(toks); i++ {
		switch toks[i].kw {
		case "beginbfchar":
			for i+2 < len(toks) && toks[i+1].isHex() && toks[i+2].isHex() {
				m[beUint(toks[i+1].hex)] = utf16be(toks[i+2].hex)
				i += 2
				if len(m) > maxCmapEntries {
					return m
				}
			}
		case "beginbfrange":
			for i+3 < len(toks) && toks[i+1].isHex() && toks[i+2].isHex() {
				lo, hi := beUint(toks[i+1].hex), beUint(toks[i+2].hex)
				if hi < lo || hi-lo > 65535 {
					break
				}
				dst := toks[i+3]
				switch {
				case dst.isHex():
					units := utf16beUnits(dst.hex)
					for c := lo; c <= hi; c++ {
						m[c] = incrementedUTF16(units, c-lo)
					}
					i += 3
				case dst.kw == "[":
					j := i + 4
					c := lo
					for j < len(toks) && toks[j].kw != "]" {
						if toks[j].isHex() && c <= hi {
							m[c] = utf16be(toks[j].hex)
							c++
						}
						j++
					}
					i = j
				default:
					i += 3
				}
				if len(m) > maxCmapEntries {
					return m
				}
			}
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

type cmTok struct {
	hex []byte
	kw  string
}

func (t cmTok) isHex() bool { return t.hex != nil }

// lexCMap tokenizes the PostScript-flavored CMap syntax just enough for
// codespace/bfchar/bfrange extraction.
func lexCMap(data []byte) []cmTok {
	var toks []cmTok
	for i := 0; i < len(data); {
		c := data[i]
		switch {
		case c == '%': // comment to end of line
			for i < len(data) && data[i] != '\n' {
				i++
			}
		case c == '<':
			j := i + 1
			var hexDigits []byte
			for j < len(data) && data[j] != '>' {
				if isHexDigit(data[j]) {
					hexDigits = append(hexDigits, data[j])
				}
				j++
			}
			if len(hexDigits)%2 == 1 {
				hexDigits = append(hexDigits, '0')
			}
			raw := make([]byte, len(hexDigits)/2)
			_, _ = hex.Decode(raw, hexDigits) // input pre-filtered to hex digits
			toks = append(toks, cmTok{hex: raw})
			i = j + 1
		case c == '[' || c == ']':
			toks = append(toks, cmTok{kw: string(c)})
			i++
		case c == '(': // literal string; skip (not used by bf entries)
			depth := 1
			i++
			for i < len(data) && depth > 0 {
				switch data[i] {
				case '\\':
					i++
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
		case c <= ' ':
			i++
		default:
			j := i
			for j < len(data) && data[j] > ' ' && data[j] != '<' && data[j] != '[' && data[j] != ']' && data[j] != '(' && data[j] != '%' {
				j++
			}
			toks = append(toks, cmTok{kw: string(data[i:j])})
			i = j
		}
		if len(toks) > 4*maxCmapEntries {
			break
		}
	}
	return toks
}

func isHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func beUint(b []byte) uint32 {
	var v uint32
	for _, x := range b {
		v = v<<8 | uint32(x)
	}
	return v
}

func utf16beUnits(b []byte) []uint16 {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return units
}

func utf16be(b []byte) string {
	return string(utf16.Decode(utf16beUnits(b)))
}

// incrementedUTF16 renders units with the final code unit advanced by off,
// the bfrange continuation rule.
func incrementedUTF16(units []uint16, off uint32) string {
	if len(units) == 0 {
		return ""
	}
	out := make([]uint16, len(units))
	copy(out, units)
	out[len(out)-1] += uint16(off & 0xFFFF) // bfrange spans are capped at 65535
	return string(utf16.Decode(out))
}
