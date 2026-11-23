package pdf

import (
	"strings"
	"testing"
)

// para lays out lines of text downward from (x, y), one per pitch.
func para(x, y, pitch float64, lines ...string) []Glyph {
	var glyphs []Glyph
	for i, line := range lines {
		glyphs = append(glyphs, run(line, x, y-float64(i)*pitch, 5, 10)...)
	}
	return glyphs
}

var (
	leftColumn = []string{
		"the left column opens the story here",
		"and carries it down through several",
		"lines of justified prose until the",
		"column ends near the bottom of it",
	}
	rightColumn = []string{
		"then the right column picks it up",
		"and finishes the thought for good",
		"with a few more lines of prose to",
		"read after the whole left column",
	}
)

// TestColumnReadingOrder: side-by-side columns read one after the other,
// with a title above them and a table below them in their places, whether
// the columns' baselines align (LaTeX) or not (browsers).
func TestColumnReadingOrder(t *testing.T) {
	want := strings.Join(append(append([]string{"A Title Across The Page"}, leftColumn...), append(rightColumn,
		"Item Qty", "Widgets 12")...), "\n")
	for _, offset := range []float64{0, 4} {
		page := Page{Glyphs: concat(
			run("A Title Across The Page", 100, 700, 10, 10),
			para(40, 660, 12, leftColumn...),
			para(240, 660+offset, 12, rightColumn...),
			para(40, 580, 12, "Item Qty", "Widgets 12"),
		)}
		if got := page.Text(); got != want {
			t.Errorf("baseline offset %v: text =\n%s\nwant\n%s", offset, got, want)
		}
	}
}

// TestColumnReadingOrderKeepsTablesAndLists: a table of short cells reads
// row by row, a bulleted list keeps each bullet with its item, and
// paragraphs aligned alternately left and right are not columns.
// LayoutColumns reads every gutter as a column boundary, tables included.
func TestColumnReadingOrderKeepsTablesAndLists(t *testing.T) {
	table := Page{Glyphs: concat(
		para(40, 700, 12, "Name of item", "Alpha widget", "Beta widget", "Gamma widget"),
		para(240, 700, 12, "Score total", "12 points", "34 points", "56 points"),
	)}
	if got, want := table.Text(), "Name of item Score total\nAlpha widget 12 points\nBeta widget 34 points\nGamma widget 56 points"; got != want {
		t.Errorf("table text = %q, want %q", got, want)
	}
	if got, want := table.TextWithOptions(LayoutOptions{Mode: LayoutColumns}),
		"Name of item\nAlpha widget\nBeta widget\nGamma widget\nScore total\n12 points\n34 points\n56 points"; got != want {
		t.Errorf("table with LayoutColumns = %q, want %q", got, want)
	}

	list := Page{Glyphs: concat(
		para(40, 700, 12, "•", "•", "•"),
		para(60, 700, 12, "first item of the list runs long", "second item of the list runs long", "third item of the list runs long"),
	)}
	if got := list.Text(); !strings.HasPrefix(got, "• first item") || strings.Count(got, "\n") != 2 {
		t.Errorf("list text = %q", got)
	}

	alternating := Page{Glyphs: concat(
		para(300, 700, 12, "a right aligned paragraph of words"),
		para(40, 688, 12, "then a left aligned paragraph here", "that runs on for a second line"),
		para(300, 664, 12, "and a right aligned one after it", "which also has a second line"),
	)}
	want := "a right aligned paragraph of words\nthen a left aligned paragraph here\n" +
		"that runs on for a second line\nand a right aligned one after it\nwhich also has a second line"
	if got := alternating.Text(); got != want {
		t.Errorf("alternating paragraphs = %q", got)
	}
}

// TestColumnReadingOrderLaTeXGutter: LaTeX sets two columns 10pt apart,
// one em of its 10pt text and less of 11pt text, and ends justified lines
// with a space glyph. A section heading in one column shares a baseline
// with the other column's text. The columns still read one after the
// other.
func TestColumnReadingOrderLaTeXGutter(t *testing.T) {
	left := []string{
		"the left column opens the story here ",
		"and carries it down through several ",
		"lines of justified prose until the  ",
		"column ends near the bottom of it   ",
	}
	right := []string{
		"then the right column picks it up",
		"and finishes the thought for good",
		"with a few more lines of prose to",
	}
	want := strings.Join(append(append(
		[]string{"the left column opens the story here", "and carries it down through several",
			"lines of justified prose until the", "column ends near the bottom of it"},
		"2 Heading"), right...), "\n")
	for _, size := range []float64{10, 11} {
		advance := size / 2
		// The left column's text ends at 40+36*advance; the gutter is 10.
		x := 40 + 36*advance + 10
		var glyphs []Glyph
		for i, line := range left {
			glyphs = append(glyphs, run(line, 40, 700-float64(i)*12, advance, size)...)
		}
		glyphs = append(glyphs, run("2 Heading", x, 700, 1.4*advance, 1.4*size)...)
		for i, line := range right {
			glyphs = append(glyphs, run(line, x, 688-float64(i)*12, advance, size)...)
		}
		if got := (Page{Glyphs: glyphs}).Text(); got != want {
			t.Errorf("%vpt: text =\n%s\nwant\n%s", size, got, want)
		}
	}
}

// TestColumnReadingOrderMarginHeading: a heading set in the margin beside
// the paragraph it starts stays below the end of the paragraph before it.
func TestColumnReadingOrderMarginHeading(t *testing.T) {
	page := Page{Glyphs: concat(
		para(200, 700, 12, "the paragraph before the heading runs",
			"over a few lines of text and ends", "here, above the gap."),
		run("Margin Head", 40, 660, 6, 12),
		para(200, 660, 12, "and the paragraph after the heading",
			"starts beside it and runs on down",
			"for a few more lines of prose too"),
	)}
	want := "the paragraph before the heading runs\nover a few lines of text and ends\nhere, above the gap.\n" +
		"Margin Head\nand the paragraph after the heading\nstarts beside it and runs on down\nfor a few more lines of prose too"
	if got := page.Text(); got != want {
		t.Errorf("text =\n%s\nwant\n%s", got, want)
	}
}
