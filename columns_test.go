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
