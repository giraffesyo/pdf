package pdf

import (
	"strings"
	"testing"
)

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

// TestLayoutWordGaps: condensed faces set words 0.15 em apart, which is
// still a word break (poppler breaks at 0.1 em); and a glyph kerned back
// over the space drawn before it keeps the space before it, not after.
func TestLayoutWordGaps(t *testing.T) {
	var glyphs []Glyph
	x := 10.0
	for i, word := range []string{"Two", "years", "after"} {
		if i > 0 {
			x += 0.15 * 18 // a condensed word space, no space glyph
		}
		glyphs = append(glyphs, run(word, x, 100, 9, 18)...)
		x += 9 * float64(len(word))
	}
	if got := (Page{Glyphs: glyphs}).Text(); got != "Two years after" {
		t.Errorf("condensed text = %q", got)
	}

	kerned := concat(
		run("doesn't", 10, 100, 6, 12),
		[]Glyph{{Text: " ", X: 52, Y: 100, Advance: 6, Size: 12}},
		run("work", 51.7, 100, 6, 12), // pulled back over the space
		run("works", 150, 102, 6, 12), // a raised line joining the row
	)
	if got := (Page{Glyphs: kerned}).Text(); got != "doesn't work works" {
		t.Errorf("kerned text = %q", got)
	}
}

// TestOffPageText: a line drawn wholly outside the media box is on no
// page and leaves the text, in every mode; a line that starts on the page
// and runs past its edge stays whole; KeepOffPageText keeps everything,
// and Glyphs reports everything either way.
func TestOffPageText(t *testing.T) {
	page := Page{
		MediaBox: Rect{MaxX: 200, MaxY: 200},
		Glyphs: concat(
			run("on the page", 10, 150, 6, 10),
			run("running past the edge", 150, 120, 6, 10),
			run("above the page", 10, 400, 6, 10),
			run("left of it", -300, 100, 6, 10),
		),
	}
	want := "on the page\nrunning past the edge"
	for _, mode := range []LayoutMode{LayoutPosition, LayoutContentOrder, LayoutColumns} {
		if got := page.TextWithOptions(LayoutOptions{Mode: mode}); got != want {
			t.Errorf("mode %d: text = %q, want %q", mode, got, want)
		}
	}
	if got := page.TextWithOptions(LayoutOptions{KeepOffPageText: true}); !strings.Contains(got, "above the page") ||
		!strings.Contains(got, "left of it") {
		t.Errorf("KeepOffPageText text = %q", got)
	}
}

// TestLayoutScripts: a footnote marker raised after its sentence and a
// subscript lowered after its base join their line, though their small
// size puts them outside its usual tolerance; a small caption set below a
// heading, not at its end, does not.
func TestLayoutScripts(t *testing.T) {
	page := Page{Glyphs: concat(
		run("people safe.", 10, 600, 5, 9.8),
		run("1", 70, 603.26, 3.4, 5.71), // raised 0.33 em, where the line ends
		run(" Although", 73.4, 600, 5, 9.8),
		run("H", 10, 560, 6, 10),
		run("2", 16, 557.5, 3.5, 6), // lowered 0.25 em
		run("O", 19.5, 560, 6, 10),
		run("Heading", 10, 500, 12, 24),
		run("caption", 10, 492, 5, 9), // under the heading's start
	)}
	if got, want := page.Text(), "people safe.1 Although\nH2O\nHeading\ncaption"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}
