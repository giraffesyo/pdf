// Package stdfont holds the advance widths of the 14 standard PDF fonts,
// which PDF 1.4 and earlier let a document use without a /Widths array,
// leaving the metrics to the reader. Without them every glyph measures
// alike, and text positioned from real widths — words set by the gaps
// between them, columns, overlays — reads wrong.
//
// The widths come from Adobe's Core 14 AFM files by internal/gen; see
// NOTICE for their terms.
package stdfont

import (
	"bytes"
	"strings"
)

// Font is a standard font's metrics.
type Font struct {
	latin   map[string]uint16 // by the text a glyph spells
	byCode  *[256]uint16      // by code, for Symbol and ZapfDingbats
	courier bool              // every glyph 600
}

// Lookup returns the standard font a /BaseFont names — directly, or by
// the names Windows and others give their metric-compatible faces (Arial
// for Helvetica, Times New Roman for Times, Courier New for Courier), a
// subset prefix or style suffix included — and whether it names one.
func Lookup(baseFont string) (Font, bool) {
	name := baseFont
	if plus := strings.IndexByte(name, '+'); plus == 6 {
		name = name[plus+1:]
	}
	// Lower-cased, without separators, in a buffer of its own: no
	// allocation for the few fonts a document names.
	var buf [64]byte
	family := buf[:0]
	for i := 0; i < len(name) && len(family) < len(buf); i++ {
		switch c := name[i]; {
		case c == ' ' || c == '-' || c == ',':
		case c >= 'A' && c <= 'Z':
			family = append(family, c+'a'-'A')
		default:
			family = append(family, c)
		}
	}
	has := func(s string) bool { return bytes.Contains(family, []byte(s)) }
	starts := func(s string) bool { return bytes.HasPrefix(family, []byte(s)) }
	bold := has("bold") || has("black") || has("heavy")
	italic := has("italic") || has("oblique")
	switch {
	case starts("symbol"):
		return Font{byCode: &symbolWidths}, true
	case starts("zapfdingbats"), starts("dingbats"):
		return Font{byCode: &zapfDingbatsWidths}, true
	case starts("courier"):
		return Font{courier: true}, true
	case starts("helvetica"), starts("arial"):
		if bold {
			return Font{latin: latinWidths["Helvetica-Bold"]}, true
		}
		return Font{latin: latinWidths["Helvetica"]}, true
	case starts("times"):
		switch {
		case bold && italic:
			return Font{latin: latinWidths["Times-BoldItalic"]}, true
		case bold:
			return Font{latin: latinWidths["Times-Bold"]}, true
		case italic:
			return Font{latin: latinWidths["Times-Italic"]}, true
		}
		return Font{latin: latinWidths["Times-Roman"]}, true
	}
	return Font{}, false
}

// Width returns the advance width, in thousandths of the font size, of
// the glyph a code selects — found by the text it spells, or for Symbol
// and ZapfDingbats by the code in their built-in encodings — and whether
// the font has it.
func (f Font) Width(code byte, text string) (float64, bool) {
	switch {
	case f.courier:
		return 600, true
	case f.byCode != nil:
		if w := f.byCode[code]; w != 0 {
			return float64(w), true
		}
	case f.latin != nil:
		if w, ok := f.latin[text]; ok {
			return float64(w), true
		}
	}
	return 0, false
}
