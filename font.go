package pdf

import ldpdf "github.com/ledongthuc/pdf"

// fontInfo wraps one font dictionary with decode and width lookups.
type fontInfo struct {
	twoByte  bool               // Type0 composite font: 2-byte codes
	toUni    map[uint32]string  // ToUnicode CMap, checked first
	fallback ldpdf.TextEncoding // library encoder (WinAnsi/MacRoman/Differences/...)

	firstChar int
	widths    []float64          // simple fonts: indexed by code-firstChar
	cidWidths map[uint32]float64 // composite fonts
	defWidth  float64
}

func loadFont(cache map[string]*fontInfo, resources ldpdf.Value, name string) *fontInfo {
	if f, ok := cache[name]; ok {
		return f
	}
	fv := resources.Key("Font").Key(name)
	var f *fontInfo
	if fv.Kind() == ldpdf.Dict {
		f = newFontInfo(fv)
	}
	cache[name] = f
	return f
}

func newFontInfo(fv ldpdf.Value) *fontInfo {
	f := &fontInfo{defWidth: 500}
	f.toUni = parseToUnicode(fv.Key("ToUnicode"))

	if fv.Key("Subtype").Name() == "Type0" {
		f.twoByte = true
		f.defWidth = 1000
		desc := fv.Key("DescendantFonts").Index(0)
		if desc.Kind() == ldpdf.Dict {
			if dw := desc.Key("DW"); dw.Kind() == ldpdf.Integer || dw.Kind() == ldpdf.Real {
				f.defWidth = dw.Float64()
			}
			f.cidWidths = parseCIDWidths(desc.Key("W"))
		}
		return f
	}

	// Simple font: /FirstChar + /Widths, library encoder as fallback.
	// Exception: a Type3 font without ToUnicode draws glyph procedures
	// addressed by arbitrary codes; no encoding fallback can recover text,
	// and passing raw codes through produces convincing-looking garbage.
	// Dropping the glyphs lets truly image-like documents fail honestly.
	if fv.Key("Subtype").Name() == "Type3" && f.toUni == nil {
		return f
	}
	f.fallback = ldpdf.Font{V: fv}.Encoder()
	f.firstChar = int(fv.Key("FirstChar").Int64())
	if wArr := fv.Key("Widths"); wArr.Kind() == ldpdf.Array {
		f.widths = make([]float64, wArr.Len())
		for i := range f.widths {
			f.widths[i] = wArr.Index(i).Float64()
		}
	}
	if mw := fv.Key("FontDescriptor").Key("MissingWidth"); mw.Kind() == ldpdf.Integer || mw.Kind() == ldpdf.Real {
		f.defWidth = mw.Float64()
	}
	return f
}

// parseCIDWidths reads a CIDFont /W array: sequences of either
// "c [w1 w2 ...]" or "cFirst cLast w".
func parseCIDWidths(w ldpdf.Value) map[uint32]float64 {
	if w.Kind() != ldpdf.Array {
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
		case ldpdf.Array:
			start := clampCID(c.Int64())
			for j := 0; j < next.Len() && j < 65536; j++ {
				out[start+uint32(j)] = next.Index(j).Float64()
			}
			i += 2
		case ldpdf.Integer, ldpdf.Real:
			if i+2 >= w.Len() {
				return out
			}
			lo, hi := clampCID(c.Int64()), clampCID(next.Int64())
			width := w.Index(i + 2).Float64()
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

// decode splits raw string bytes into per-code decoded glyphs.
func (f *fontInfo) decode(raw string) []decoded {
	if f == nil {
		return nil
	}
	var out []decoded
	if f.twoByte {
		for i := 0; i+1 < len(raw); i += 2 {
			code := uint32(raw[i])<<8 | uint32(raw[i+1])
			out = append(out, decoded{
				text:  f.mapCode(code, raw[i:i+2]),
				width: f.cidWidth(code),
			})
		}
		return out
	}
	for i := range len(raw) {
		code := uint32(raw[i])
		out = append(out, decoded{
			text:  f.mapCode(code, raw[i:i+1]),
			width: f.simpleWidth(int(code)),
			space: raw[i] == ' ',
		})
	}
	return out
}

// mapCode decodes one character code: ToUnicode wins, then the library
// encoder, then U+FFFD (stripped later).
func (f *fontInfo) mapCode(code uint32, rawBytes string) string {
	if s, ok := f.toUni[code]; ok {
		return s
	}
	if f.fallback != nil {
		return f.fallback.Decode(rawBytes)
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
