package pdf

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/giraffesyo/pdf/pdftest"
)

func extract(t *testing.T, doc []byte) []Glyph {
	t.Helper()
	d, err := Extract(context.Background(), bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(d.Pages) == 0 {
		return nil
	}
	return d.Pages[0].Glyphs
}

// lineText joins glyphs into lines by Y for easy assertions (position
// details are covered by dedicated tests).
func lineText(glyphs []Glyph) []string {
	sort.SliceStable(glyphs, func(i, j int) bool {
		if glyphs[i].Y != glyphs[j].Y {
			return glyphs[i].Y > glyphs[j].Y
		}
		return glyphs[i].X < glyphs[j].X
	})
	var lines []string
	lastY := 0.0
	cur := ""
	for _, g := range glyphs {
		if cur != "" && lastY-g.Y > 2 {
			lines = append(lines, cur)
			cur = ""
		}
		cur += g.Text
		lastY = g.Y
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// simpleDoc builds a one-page PDF whose content stream uses Helvetica as /F1.
func simpleDoc(content string) []byte {
	return pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", content),
		pdftest.Helvetica(),
	)
}

func TestTdPositionedText(t *testing.T) {
	// Td-relative positioning only (no Tm): the pattern the upstream
	// library loses positions on.
	glyphs := extract(t, simpleDoc(`BT
/F1 12 Tf
72 720 Td
(Objective) Tj
0 -20 Td
(Build reliable systems) Tj
ET`))
	lines := lineText(glyphs)
	if len(lines) != 2 || lines[0] != "Objective" || lines[1] != "Build reliable systems" {
		t.Errorf("lines = %q", lines)
	}
}

func TestTStarAndLeading(t *testing.T) {
	glyphs := extract(t, simpleDoc(`BT
/F1 10 Tf
14 TL
72 700 Td
(first) Tj
T*
(second) Tj
T*
(third) Tj
ET`))
	lines := lineText(glyphs)
	want := []string{"first", "second", "third"}
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestFormXObjectText(t *testing.T) {
	// The Google-Docs export pattern: page content only invokes a Form
	// XObject; all text lives inside it.
	form := pdftest.Stream(
		"/Type /XObject /Subtype /Form /BBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >>",
		`BT /F1 12 Tf 72 700 Td (inside the form) Tj ET`)
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /XObject << /X1 6 0 R >> >>"),
		pdftest.Stream("", "q /X1 Do Q"),
		pdftest.Helvetica(),
		form,
	)
	lines := lineText(extract(t, doc))
	if len(lines) != 1 || lines[0] != "inside the form" {
		t.Errorf("lines = %q (Form XObject text must be extracted)", lines)
	}
}

func TestFormXObjectMatrixOffset(t *testing.T) {
	// The form's /Matrix must transform positions so form text interleaves
	// correctly with page text.
	form := pdftest.Stream(
		"/Type /XObject /Subtype /Form /BBox [0 0 612 792] /Matrix [1 0 0 1 0 -100] /Resources << /Font << /F1 5 0 R >> >>",
		`BT /F1 12 Tf 72 700 Td (form line) Tj ET`)
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> /XObject << /X1 6 0 R >> >>"),
		pdftest.Stream("", `BT /F1 12 Tf 72 650 Td (page line) Tj ET
q /X1 Do Q`),
		pdftest.Helvetica(),
		form,
	)
	// Form text lands at y=600 (700-100), below the page line at 650.
	lines := lineText(extract(t, doc))
	if len(lines) != 2 || lines[0] != "page line" || lines[1] != "form line" {
		t.Errorf("lines = %q (matrix offset should order form text below)", lines)
	}
}

func TestSelfReferencingFormTerminates(t *testing.T) {
	form := pdftest.Stream(
		"/Type /XObject /Subtype /Form /BBox [0 0 612 792] /Resources << /XObject << /X1 6 0 R >> /Font << /F1 5 0 R >> >>",
		`BT /F1 12 Tf 72 700 Td (loop) Tj ET /X1 Do`)
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /XObject << /X1 6 0 R >> >>"),
		pdftest.Stream("", "/X1 Do"),
		pdftest.Helvetica(),
		form,
	)
	glyphs := extract(t, doc) // must terminate (depth cap), not hang or blow the stack
	if len(glyphs) == 0 {
		t.Error("expected some glyphs from the capped recursion")
	}
}

func TestType0ToUnicodeDecoding(t *testing.T) {
	// The Word/Google export pattern: Identity-H two-byte codes decoded
	// purely through the ToUnicode CMap (bfchar and bfrange forms), with
	// an unmapped code that must be dropped, not emitted as U+FFFD.
	cmap := pdftest.ToUnicodeCMap(`2 beginbfchar
<0001> <0053>
<0002> <006B>
endbfchar
1 beginbfrange
<0010> <0012> <0069>
endbfrange`)
	// 0001→S 0002→k, 0010..0012→i,j,k ; 0099 is unmapped.
	content := `BT /F1 12 Tf 72 700 Td ` + pdftest.Hex2(1, 2, 0x10, 0x11, 0x12, 0x99) + ` Tj ET`
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", content),
		pdftest.Type0Font(6, 7),
		pdftest.CIDFont("/W [1 [500 500] 16 18 500]"),
		cmap,
	)
	lines := lineText(extract(t, doc))
	if len(lines) != 1 || lines[0] != "Skijk" {
		t.Errorf("lines = %q, want [Skijk] (ToUnicode decode, unmapped dropped)", lines)
	}
}

func TestDifferencesWithToUnicodePriority(t *testing.T) {
	// The Type3-style pattern: /Encoding /Differences maps codes to
	// meaningless glyph names, but /ToUnicode carries the real mapping.
	// ToUnicode must win; without it the codes come out as control bytes.
	cmap := pdftest.ToUnicodeCMap(`2 beginbfchar
<03> <0041>
<10> <0042>
endbfchar`)
	font := "<< /Type /Font /Subtype /Type1 /BaseFont /FAKEFT+Weird " +
		"/Encoding << /Differences [3 /g3 16 /g16] >> /FirstChar 3 /Widths [500] /ToUnicode 6 0 R >>"
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", "BT /F1 12 Tf 72 700 Td (\x03\x10) Tj ET"),
		font,
		cmap,
	)
	lines := lineText(extract(t, doc))
	if len(lines) != 1 || lines[0] != "AB" {
		t.Errorf("lines = %q, want [AB] (ToUnicode must outrank Differences)", lines)
	}
}

func TestTJOffsetsProduceGaps(t *testing.T) {
	// A TJ kerning array where a large negative offset separates words;
	// glyph X positions must reflect the offsets.
	glyphs := extract(t, simpleDoc(`BT /F1 10 Tf 72 700 Td [(ab) -500 (cd)] TJ ET`))
	if len(glyphs) != 4 {
		t.Fatalf("got %d glyphs", len(glyphs))
	}
	sort.SliceStable(glyphs, func(i, j int) bool { return glyphs[i].X < glyphs[j].X })
	gap := glyphs[2].X - (glyphs[1].X + glyphs[1].Advance)
	if gap < 4.5 || gap > 5.5 { // -500/1000 * 10pt = 5pt
		t.Errorf("gap = %v, want ~5", gap)
	}
}

func TestControlAndPUAGlyphsDropped(t *testing.T) {
	glyphs := extract(t, simpleDoc("BT /F1 12 Tf 72 700 Td (ok\x01) Tj ET"))
	for _, g := range glyphs {
		if strings.ContainsAny(g.Text, "\x01�") {
			t.Errorf("junk rune survived: %q", g.Text)
		}
	}
	if text := glyphText(glyphs); text != "ok" {
		t.Errorf("text = %q, want ok", text)
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"plain":               "plain",
		"a\u00a0b\x01\uE000c": "a bc", // NBSP, control, private use
		"\uFB00\uFB01\uFB02\uFB03\uFB04\uFB05\uFB06": "fffiflffifflstst",
		"De\uFB01nitions": "Definitions",
		"\uFB02 \uFB01":   "fl fi",  // unrelated runes pass through
		"\uFB07":          "\uFB07", // past the Latin ligature block
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCyclicPageTreeRejected(t *testing.T) {
	// A /Pages node listing itself as a kid: the library's page walk would
	// recurse forever (stack overflow), so Extract must reject it up front.
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		"<< /Type /Pages /Kids [2 0 R] /Count 1 >>",
	)
	if _, err := Extract(context.Background(), bytes.NewReader(doc), int64(len(doc))); err == nil {
		t.Error("Extract must reject a self-referencing page tree")
	}
}

// TestPageTreeCycleSkipped: a page tree that lists one of its own nodes
// again keeps the pages around the cycle, as poppler does, and reports it.
func TestPageTreeCycleSkipped(t *testing.T) {
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		"<< /Type /Pages /Kids [3 0 R 2 0 R] /Count 2 >>",
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", "BT /F1 12 Tf 72 700 Td (kept) Tj ET"),
		pdftest.Helvetica(),
	)
	d, err := Extract(context.Background(), bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got := d.Text(); got != "kept" {
		t.Errorf("text = %q", got)
	}
	if len(d.Warnings) != 1 || d.Warnings[0].Code != WarningMalformedDocument {
		t.Errorf("warnings = %v", d.Warnings)
	}
}

func TestExtractDocumentText(t *testing.T) {
	doc := simpleDoc(`BT /F1 12 Tf 72 720 Td (hello world) Tj ET`)
	d, err := Extract(context.Background(), bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(d.Pages))
	}
	if got := d.Text(); got != "hello world" {
		t.Errorf("Text() = %q", got)
	}
}

func TestExtractContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	doc := simpleDoc(`BT /F1 12 Tf 72 720 Td (x) Tj ET`)
	if _, err := Extract(ctx, bytes.NewReader(doc), int64(len(doc))); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func FuzzExtract(f *testing.F) {
	seed := "BT /F1 12 Tf 72 720 Td (seed text) Tj ET"
	f.Add(simpleDoc(seed))
	f.Add([]byte("%PDF-1.4 garbage"))
	// A damaged header claiming an absurd object number once sized the
	// rebuilt xref table for it (160 MB, ~1 s); see rebuildObjectBound.
	f.Add([]byte("  0444444440000000  0000000000000000000000 obj 000000000\x18\x1800000"))
	f.Add(pdftest.BuildXrefStream(1,
		pdftest.Catalog(2), pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", seed), pdftest.Helvetica()))
	f.Add(pdftest.BuildObjStm(1, []int{1, 2, 3, 5},
		pdftest.Catalog(2), pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", seed), pdftest.Helvetica()))
	f.Add(pdftest.BuildHybrid(1,
		pdftest.Catalog(2), pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", seed), pdftest.Helvetica()))
	f.Add(pdftest.BuildEncrypted(1, pdftest.EncryptSpec{R: 4},
		pdftest.Catalog(2), pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", seed), pdftest.Helvetica()))
	for _, doc := range fuzzSeedDocuments() {
		f.Add(doc)
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		// Extract must never panic or hang: the object layer returns errors
		// rather than panicking, and the page tree is gated before walking.
		// Text runs the layout — columns, right-to-left order, duplicates,
		// scripts — in every mode.
		doc, _ := Extract(context.Background(), bytes.NewReader(data), int64(len(data)))
		if doc == nil {
			return
		}
		for _, page := range doc.Pages {
			for _, mode := range []LayoutMode{LayoutPosition, LayoutContentOrder, LayoutColumns} {
				_ = page.TextWithOptions(LayoutOptions{Mode: mode})
			}
		}
	})
}

// fuzzSeedDocuments are documents that reach the paths added since the
// fuzz corpus was seeded: Type3 fonts, forms regenerated from their
// fields, annotation appearances, Unicode and CJK composite fonts, forms
// that draw themselves, side-by-side columns, right-to-left text, and
// text off the page.
func fuzzSeedDocuments() [][]byte {
	page := func(resources, content string, extra ...string) []byte {
		objs := append([]string{
			"<< /Type /Catalog /Pages 2 0 R /AcroForm << /NeedAppearances true /DR << /Font << /Helv 5 0 R >> >> >> >>",
			pdftest.Pages(3),
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources " + resources +
				" /Contents 4 0 R /Annots [7 0 R 8 0 R 9 0 R] >>",
			pdftest.Stream("", content),
			pdftest.Helvetica(),
		}, extra...)
		return pdftest.Build(1, objs...)
	}
	widgets := []string{
		"<< /Type /Font /Subtype /Type3 /FontBBox [0 0 50 50] /FontMatrix [1 0 0 1 0 0] /CharProcs << >> " +
			"/Encoding << /Differences [72 /H 97 /a97 98 /Q7] /BaseEncoding /WinAnsiEncoding >> /FirstChar 72 /LastChar 98 /Widths [25] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Tx /Ff 8192 /V (s3cret) /DA (/Helv 0 Tf) /Rect [10 10 90 30] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Btn /V /Yes /MK << /CA (l) >> /Rect [10 40 22 52] /AP << /N << /Yes 10 0 R /Off 10 0 R >> >> >>",
		"<< /Type /Annot /Subtype /Widget /FT /Ch /TI 1 /V [(B) (C)] /Opt [(A) [(b) (B)] (C)] /DA (/ZaDb 10 Tf) /Rect [10 60 90 80] >>",
		pdftest.Stream("/Subtype /Form /BBox [0 0 12 12] /Matrix [1 0 0 1 0 0]", "BT /ZaDb 9 Tf (4) Tj ET /X Do"),
	}
	columns := "BT /F1 10 Tf 20 250 Td (a left column of prose words) Tj 0 -12 Td (continues down the left side) Tj " +
		"0 -12 Td (for a third line here) Tj ET BT /F1 10 Tf 170 250 Td (and a right column follows it) Tj " +
		"0 -12 Td (with prose of its own too) Tj 0 -12 Td (down its three lines) Tj ET " +
		"BT /F1 10 Tf 20 700 Td (off the page) Tj ET BT /F1 5 Tf 120 253 Td (1) Tj ET"
	return [][]byte{
		page("<< /Font << /F1 5 0 R /T3 6 0 R >> >>", columns+" BT /T3 0.24 Tf 10 100 Td (HaQ) Tj ET", widgets...),
		page("<< /Font << /F1 5 0 R >> /XObject << /X 10 0 R >> >>", "/X Do", widgets...),
		pdftest.Build(1,
			pdftest.Catalog(2), pdftest.Pages(3),
			pdftest.Page(2, 4, "<< /Font << /F1 5 0 R /F2 7 0 R >> >>"),
			pdftest.Stream("", "BT /F1 10 Tf 72 700 Td <0041597D0042> Tj /F2 10 Tf 0 -20 Td <0465034A0029> Tj ET"),
			"<< /Type /Font /Subtype /Type0 /BaseFont /S /Encoding /UniJIS-UTF16-H /DescendantFonts [6 0 R] >>",
			"<< /Type /Font /Subtype /CIDFontType0 /BaseFont /S /DW 1000 /W [34 [600]] "+
				"/CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 6 >> >>",
			"<< /Type /Font /Subtype /Type0 /BaseFont /S /Encoding /Identity-H /DescendantFonts [6 0 R] >>",
		),
		pdftest.Build(1,
			pdftest.Catalog(2), pdftest.Pages(3),
			pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
			pdftest.Stream("", "BT /F1 12 Tf 72 700 Td <0645062D0645062F> Tj 0 -14 Td <05E905DC05D5> Tj ET"),
			pdftest.Type0Font(6, 7),
			pdftest.CIDFont(""),
			pdftest.ToUnicodeCMap("1 beginbfrange\n<0000> <FFFF> <0000>\nendbfrange"),
		),
	}
}

func TestGarbageStreamNoPanic(t *testing.T) {
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< >>"),
		pdftest.Stream("", "BT this is (not \x00\xff balanced garbage Tj ["),
	)
	_ = extract(t, doc) // must not panic
}
