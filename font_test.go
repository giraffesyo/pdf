package pdf

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/giraffesyo/pdf/internal/object"
	"github.com/giraffesyo/pdf/pdftest"
)

// TestLoadFontSharedAcrossStreams: one font object referenced from several
// content streams — every page of a typical document — must be decoded
// once. Fonts given as direct dictionaries have no identity to share.
func TestLoadFontSharedAcrossStreams(t *testing.T) {
	direct := "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3, 4),
		pdftest.Page(2, 5, "<< /Font << /F1 6 0 R /D "+direct+" >> >>"),
		pdftest.Page(2, 5, "<< /Font << /F2 6 0 R /D "+direct+" >> >>"),
		pdftest.Stream("", "BT /F1 12 Tf (x) Tj ET"),
		pdftest.Helvetica(),
	)
	r, err := object.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatal(err)
	}
	res1 := object.Inherited(r.Page(1), "Resources")
	res2 := object.Inherited(r.Page(2), "Resources")
	shared := newFontCache()
	names1 := map[string]*fontInfo{}
	names2 := map[string]*fontInfo{}

	f1 := loadFont(names1, shared, res1, "F1", nil, 0)
	f2 := loadFont(names2, shared, res2, "F2", nil, 0)
	if f1 == nil || f1 != f2 {
		t.Errorf("indirect font decoded per stream: page1=%p page2=%p, want one shared fontInfo", f1, f2)
	}
	if got := loadFont(names1, shared, res1, "F1", nil, 0); got != f1 {
		t.Errorf("repeat lookup in the same stream = %p, want cached %p", got, f1)
	}
	if len(shared.m) != 1 {
		t.Errorf("shared cache has %d entries, want 1 (only the indirect font)", len(shared.m))
	}

	d1 := loadFont(names1, shared, res1, "D", nil, 0)
	d2 := loadFont(names2, shared, res2, "D", nil, 0)
	if d1 == nil || d2 == nil || d1 == d2 {
		t.Errorf("direct-dictionary fonts: page1=%p page2=%p, want separate per-stream entries", d1, d2)
	}
	if len(shared.m) != 1 {
		t.Errorf("direct-dictionary font entered the shared cache (%d entries)", len(shared.m))
	}

	// A nil shared cache must degrade to per-stream caching, not panic.
	if got := loadFont(map[string]*fontInfo{}, nil, res1, "F1", nil, 0); got == nil {
		t.Error("loadFont with nil shared cache returned nil font")
	}
}

// TestUnicodePredefinedCMaps: the predefined Unicode CMaps encode text as
// Unicode, so without a CMapResolver their characters still read from the
// codes — UCS-2, UTF-16 surrogate pairs, UTF-8 — and printable ASCII takes
// its width from CIDs 1–95, where every Adobe CJK collection keeps it.
func TestUnicodePredefinedCMaps(t *testing.T) {
	for _, tc := range []struct {
		cmap, codes, want string
	}{
		{"UniJIS-UCS2-H", "<0041597D0042>", "A好B"},
		{"UniGB-UTF16-H", "<0041D842DF9F>", "A𠮟"},
		{"UniKS-UTF8-H", "<41E5A5BD42>", "A好B"},
	} {
		doc := pdftest.Build(1,
			pdftest.Catalog(2),
			pdftest.Pages(3),
			pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
			pdftest.Stream("", "BT /F1 10 Tf 72 700 Td "+tc.codes+" Tj ET"),
			"<< /Type /Font /Subtype /Type0 /BaseFont /Synthetic /Encoding /"+tc.cmap+" /DescendantFonts [6 0 R] >>",
			"<< /Type /Font /Subtype /CIDFontType0 /BaseFont /Synthetic /DW 1000 /W [34 [600]] "+
				"/CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 6 >> >>",
		)
		glyphs := extract(t, doc)
		var text strings.Builder
		for _, g := range glyphs {
			text.WriteString(g.Text)
		}
		if text.String() != tc.want {
			t.Errorf("%s: text = %q, want %q", tc.cmap, text.String(), tc.want)
			continue
		}
		// "A" is CID 34 (0x41 − 0x1f), width 600; the next glyph starts 6
		// points on at size 10.
		if got := glyphs[1].X - glyphs[0].X; math.Abs(got-6) > 1e-9 {
			t.Errorf("%s: advance of A = %v, want 6", tc.cmap, got)
		}
	}
}

// TestCIDFallbacks: with no ToUnicode and no cmap in the font, a font in
// an Adobe CJK collection reads its CIDs through the collection's table —
// but only when its Encoding CMap gave the CIDs; and a
// ToUnicode named Identity-H, which some producers write for UCS-2 codes,
// reads the codes as UCS-2.
func TestCIDFallbacks(t *testing.T) {
	text := func(encoding, toUnicode, ordering, codes string) string {
		t.Helper()
		doc := pdftest.Build(1,
			pdftest.Catalog(2),
			pdftest.Pages(3),
			pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
			pdftest.Stream("", "BT /F1 10 Tf 72 700 Td "+codes+" Tj ET"),
			"<< /Type /Font /Subtype /Type0 /BaseFont /Synthetic /Encoding /"+encoding+toUnicode+" /DescendantFonts [6 0 R] >>",
			"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /Synthetic /DW 500 "+
				"/CIDSystemInfo << /Registry (Adobe) /Ordering ("+ordering+") /Supplement 2 >> >>",
		)
		var b strings.Builder
		for _, g := range extract(t, doc) {
			b.WriteString(g.Text)
		}
		return b.String()
	}
	// CIDs 41 74 1 61 1125 842: "Hi ¥亜ぁ" in Adobe-Japan1, whose CID 61
	// is the yen sign, from Adobe's own tables.
	if got := text("Identity-H", "", "Japan1", "<0029004A0001003D0465034A>"); got != "Hi ¥亜ぁ" {
		t.Errorf("Japan1 CIDs = %q", got)
	}
	if got := text("Identity-H", "", "GB1", "<0029004A11CF>"); got != "Hi中" {
		t.Errorf("GB1 CIDs = %q", got)
	}
	// An Encoding CMap that cannot be read leaves the CIDs unknown.
	if got := text("90ms-RKSJ-H", "", "Japan1", "<48>"); got != "" {
		t.Errorf("unresolved encoding = %q, want nothing", got)
	}
	if got := text("Identity-H", " /ToUnicode /Identity-H", "Identity", "<004100720069>"); got != "Ari" {
		t.Errorf("ToUnicode /Identity-H = %q", got)
	}
}
