package pdf

import "testing"

// run lays out text as glyphs of a fixed advance from (x, y), in order.
func run(text string, x, y, advance, size float64) []Glyph {
	var glyphs []Glyph
	for _, r := range text {
		glyphs = append(glyphs, Glyph{Text: string(r), X: x, Y: y, Advance: advance, Size: size})
		x += advance
	}
	return glyphs
}

func concat(runs ...[]Glyph) []Glyph {
	var glyphs []Glyph
	for _, r := range runs {
		glyphs = append(glyphs, r...)
	}
	return glyphs
}

// TestLayoutDuplicateGlyphs: fake bold paints a string twice at nearly
// the same place. Text reads it once — whether the copies are drawn string
// by string or glyph by glyph, and in every layout mode — while Glyphs
// keeps both copies and KeepDuplicateGlyphs restores them in text.
func TestLayoutDuplicateGlyphs(t *testing.T) {
	byString := concat(
		run("Heading", 10, 100, 6, 10),
		run("Heading", 10.01, 100, 6, 10), // a hundredth of a point off
		run("body", 10, 80, 6, 10),
	)
	var byGlyph []Glyph
	for _, g := range run("Bold", 10, 100, 6, 10) {
		byGlyph = append(byGlyph, g, g)
	}
	for _, mode := range []LayoutMode{LayoutPosition, LayoutContentOrder, LayoutColumns} {
		page := Page{CropBox: Rect{MaxX: 300, MaxY: 200}, Glyphs: byString}
		if got := page.TextWithOptions(LayoutOptions{Mode: mode}); got != "Heading\nbody" {
			t.Errorf("mode %d: string-repainted text = %q", mode, got)
		}
		page.Glyphs = byGlyph
		if got := page.TextWithOptions(LayoutOptions{Mode: mode}); got != "Bold" {
			t.Errorf("mode %d: glyph-repainted text = %q", mode, got)
		}
	}
	page := Page{Glyphs: byGlyph}
	if got := page.TextWithOptions(LayoutOptions{KeepDuplicateGlyphs: true}); got != "BBoolldd" {
		t.Errorf("KeepDuplicateGlyphs text = %q", got)
	}
	if len(page.Glyphs) != 8 {
		t.Errorf("Glyphs = %d, want every painted glyph", len(page.Glyphs))
	}
	// Repeated letters that are not in the same place are not duplicates.
	page = Page{Glyphs: run("aa", 10, 100, 6, 10)}
	if got := page.Text(); got != "aa" {
		t.Errorf("adjacent repeated letters = %q", got)
	}
}

// TestLayoutOverlaidRuns: two strings drawn from the same origin on one
// baseline — an overlay or stamp over a line — must not interleave letter
// by letter; each keeps its own line. A single glyph drawn over another,
// as TeX positions accents, stays in place.
func TestLayoutOverlaidRuns(t *testing.T) {
	page := Page{Glyphs: concat(
		run("Original page 0", 72, 720, 7.5, 15),
		run("Page 1 - red", 72, 720, 12, 24),
	)}
	if got := page.Text(); got != "Original page 0\nPage 1 - red" {
		t.Errorf("overlaid text = %q", got)
	}

	accent := concat(
		run("caf", 10, 100, 6, 10),
		[]Glyph{{Text: "´", X: 29.5, Y: 100, Advance: 3, Size: 10}}, // centred over the e
		run("e noir", 28, 100, 6, 10),
	)
	page = Page{Glyphs: accent}
	if got := page.Text(); got != "cafe´ noir" {
		t.Errorf("accented text = %q", got)
	}

	// A line drawn right half first reads left to right, on one line.
	page = Page{Glyphs: concat(run("world", 46, 100, 6, 10), run("hello", 10, 100, 6, 10))}
	if got := page.Text(); got != "hello world" {
		t.Errorf("out-of-order line = %q", got)
	}
}

// TestLayoutWhitespace: space glyphs at a line's ends are dropped, and a
// space glyph from a smaller line that falls inside a word on this
// baseline does not split the word.
func TestLayoutWhitespace(t *testing.T) {
	page := Page{Glyphs: concat(
		run(" indented ", 10, 100, 6, 10),
		run("next", 10, 80, 6, 10),
	)}
	if got := page.Text(); got != "indented\nnext" {
		t.Errorf("trimmed text = %q", got)
	}

	// A line of space glyphs alone writes nothing, not an empty line.
	page = Page{Glyphs: concat(
		run("  ", 10, 120, 6, 10),
		run("title", 10, 100, 6, 10),
		run("   ", 10, 80, 6, 10),
		run("body", 10, 60, 6, 10),
	)}
	if got := page.Text(); got != "title\nbody" {
		t.Errorf("text around blank lines = %q", got)
	}

	title := run("GATEWAY", 100, 725.94, 20, 28)
	stray := Glyph{Text: " ", X: 179, Y: 724.08, Advance: 3, Size: 11} // inside "W"
	page = Page{Glyphs: append([]Glyph{stray}, title...)}
	if got := page.Text(); got != "GATEWAY" {
		t.Errorf("title with a stray space = %q", got)
	}

	// Space glyphs between words still separate them.
	page = Page{Glyphs: run("two words", 10, 100, 6, 10)}
	if got := page.Text(); got != "two words" {
		t.Errorf("spaced text = %q", got)
	}
}
