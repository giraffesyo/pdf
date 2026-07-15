package pdf

import (
	"slices"

	"github.com/giraffesyo/pdf/internal/encoding"
	"github.com/giraffesyo/pdf/internal/object"
)

// fontInfo wraps one font dictionary with decode and width lookups.
type fontInfo struct {
	twoByte  bool               // Type0 composite font: 2-byte codes
	toUni    map[uint32]string  // ToUnicode CMap, checked first
	fallback *encoding.Encoding // simple-font encoding (WinAnsi/MacRoman/Differences/...)

	firstChar int
	widths    []float64          // simple fonts: indexed by code-firstChar
	cidWidths map[uint32]float64 // composite fonts
	defWidth  float64
}

func loadFont(cache map[string]*fontInfo, resources object.Value, name string) *fontInfo {
	if f, ok := cache[name]; ok {
		return f
	}
	fv := resources.Key("Font").Key(name)
	var f *fontInfo
	if fv.Kind() == object.Dict {
		f = newFontInfo(fv)
	}
	cache[name] = f
	return f
}

func newFontInfo(fv object.Value) *fontInfo {
	f := &fontInfo{defWidth: 500}
	f.toUni = parseToUnicode(fv.Key("ToUnicode"))

	if fv.Key("Subtype").Name() == "Type0" {
		f.twoByte = true
		f.defWidth = 1000
		desc := fv.Key("DescendantFonts").Index(0)
		if desc.Kind() == object.Dict {
			if dw, ok := desc.Key("DW").Float64(); ok {
				f.defWidth = dw
			}
			f.cidWidths = parseCIDWidths(desc.Key("W"))
		}
		return f
	}

	// Simple font: /FirstChar + /Widths, standard encoding as fallback.
	// Exception: a Type3 font without ToUnicode draws glyph procedures
	// addressed by arbitrary codes; no encoding fallback can recover text,
	// and passing raw codes through produces convincing-looking garbage.
	// Dropping the glyphs lets truly image-like documents fail honestly.
	if fv.Key("Subtype").Name() == "Type3" && f.toUni == nil {
		return f
	}
	f.fallback = fallbackEncoding(fv)
	f.firstChar = int(intOr(fv.Key("FirstChar"), 0))
	if wArr := fv.Key("Widths"); wArr.Kind() == object.Array {
		f.widths = make([]float64, wArr.Len())
		for i := range f.widths {
			f.widths[i], _ = wArr.Index(i).Float64()
		}
	}
	if mw, ok := fv.Key("FontDescriptor").Key("MissingWidth").Float64(); ok {
		f.defWidth = mw
	}
	return f
}

// intOr returns the value's integer, or d for non-integers.
func intOr(v object.Value, d int64) int64 {
	if n, ok := v.Int64(); ok {
		return n
	}
	return d
}

// fallbackEncoding builds a simple font's code→text fallback from its
// /Encoding entry per ISO 32000-1 §9.6.5: a base encoding name, or a
// dictionary carrying /BaseEncoding and /Differences, or — when absent —
// StandardEncoding for nonsymbolic fonts. Symbolic fonts (descriptor flag
// 3 set, flag 6 clear) keep their built-in encoding, which lives inside
// the font program; without parsing it only printable ASCII passes
// through, and high bytes drop honestly.
func fallbackEncoding(fv object.Value) *encoding.Encoding {
	enc := fv.Key("Encoding")
	if enc.Kind() == object.Dict {
		base := enc.Key("BaseEncoding").Name()
		switch base {
		case "WinAnsiEncoding", "MacRomanEncoding":
		default:
			base = "StandardEncoding"
		}
		return encoding.New(base, parseDifferences(enc.Key("Differences")))
	}
	switch name := enc.Name(); name {
	case "WinAnsiEncoding", "MacRomanEncoding":
		return encoding.New(name, nil)
	case "":
		if symbolicFont(fv) {
			return encoding.New("", nil) // ASCII passthrough
		}
		return encoding.New("StandardEncoding", nil)
	default:
		return encoding.New("", nil) // unrecognized named encoding
	}
}

// parseDifferences reads a /Differences array — integers set the current
// code, names assign consecutive codes — resolving glyph names to text.
func parseDifferences(arr object.Value) map[byte]string {
	if arr.Kind() != object.Array {
		return nil
	}
	var m map[byte]string
	code := -1
	for i := range arr.Len() {
		el := arr.Index(i)
		switch el.Kind() {
		case object.Integer:
			n, _ := el.Int64()
			code = int(n)
		case object.Name:
			if code >= 0 && code <= 0xFF {
				if m == nil {
					m = map[byte]string{}
				}
				m[byte(code)] = encoding.GlyphToText(el.Name())
			}
			code++
		}
	}
	return m
}

func symbolicFont(fv object.Value) bool {
	flags := intOr(fv.Key("FontDescriptor").Key("Flags"), 0)
	return flags&4 != 0 && flags&32 == 0
}

// parseCIDWidths reads a CIDFont /W array: sequences of either
// "c [w1 w2 ...]" or "cFirst cLast w".
func parseCIDWidths(w object.Value) map[uint32]float64 {
	if w.Kind() != object.Array {
		return nil
	}
	out := map[uint32]float64{}
	for i := 0; i < w.Len(); {
		c := w.Index(i)
		if i+1 >= w.Len() {
			break
		}
		next := w.Index(i + 1)
		switch next.Kind() {
		case object.Array:
			start := clampCID(intOr(c, 0))
			for j := 0; j < next.Len() && j < 65536; j++ {
				out[start+uint32(j)], _ = next.Index(j).Float64()
			}
			i += 2
		case object.Integer, object.Real:
			if i+2 >= w.Len() {
				return out
			}
			lo, hi := clampCID(intOr(c, 0)), clampCID(intOr(next, 0))
			width, _ := w.Index(i + 2).Float64()
			if hi-lo < 65536 {
				for code := lo; code <= hi; code++ {
					out[code] = width
				}
			}
			i += 3
		default:
			return out
		}
	}
	return out
}

// clampCID bounds a CID read from a (possibly hostile) file to the valid
// two-byte range.
func clampCID(v int64) uint32 {
	if v < 0 {
		return 0
	}
	if v > 0xFFFF {
		return 0xFFFF
	}
	return uint32(v)
}

type decoded struct {
	text  string
	width float64 // glyph-space units (1/1000 em)
	space bool    // single-byte code 32: word spacing applies
}

// appendDecoded splits raw string bytes into per-code decoded glyphs and
// appends them to dst.
func (f *fontInfo) appendDecoded(dst []decoded, raw []byte) []decoded {
	if f == nil {
		return dst
	}
	n := len(raw)
	if f.twoByte {
		n /= 2
	}
	dst = slices.Grow(dst, n)

	if f.twoByte {
		for i := 0; i+1 < len(raw); i += 2 {
			code := uint32(raw[i])<<8 | uint32(raw[i+1])
			dst = append(dst, decoded{
				text:  f.mapCode(code),
				width: f.cidWidth(code),
			})
		}
		return dst
	}
	for i := range raw {
		code := uint32(raw[i])
		dst = append(dst, decoded{
			text:  f.mapCode(code),
			width: f.simpleWidth(int(code)),
			space: raw[i] == ' ',
		})
	}
	return dst
}

// mapCode decodes one character code: ToUnicode wins, then the simple-font
// encoding fallback (single-byte codes only — Type0 fonts never set one),
// then U+FFFD (stripped later).
func (f *fontInfo) mapCode(code uint32) string {
	if s, ok := f.toUni[code]; ok {
		return s
	}
	if f.fallback != nil && code <= 0xFF {
		return f.fallback.Decode(byte(code))
	}
	return "�"
}

func (f *fontInfo) simpleWidth(code int) float64 {
	idx := code - f.firstChar
	if idx >= 0 && idx < len(f.widths) && f.widths[idx] > 0 {
		return f.widths[idx]
	}
	return f.defWidth
}

func (f *fontInfo) cidWidth(code uint32) float64 {
	if w, ok := f.cidWidths[code]; ok {
		return w
	}
	return f.defWidth
}
