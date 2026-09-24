package pdf

import (
	"slices"
	"strings"
	"testing"
)

// TestRightToLeftLogicalOrder: glyphs are painted in visual order, left to
// right across the page; Text reads right-to-left scripts back in logical
// order, keeping numbers and embedded left-to-right words readable. Each
// case gives the line as painted and as it reads. The mixed-direction
// cases match Chrome's print-to-PDF of the logical text read back, where
// pdftotext gets five of the seven wrong.
func TestRightToLeftLogicalOrder(t *testing.T) {
	for _, tc := range []struct{ visual, logical string }{
		{"ملاعلاب ابحرم", "مرحبا بالعالم"},
		{"رالود 250 رعسلا", "السعر 250 دولار"},
		{"םלוע world םולש", "שלום world עולם"},
		{"The word مالس means peace.", "The word سلام means peace."},
		{"Price: 1,250.75 لكيش today", "Price: 1,250.75 شيكل today"},
		// A mirrored glyph maps back to its logical character, so the
		// parenthesis painted at the right reads "(".
		{"3.14 :םלוע )םולש(", "(שלום) עולם: 3.14"},
		{"03-1234567 ןופלט", "טלפון 03-1234567"},
		{"plain left-to-right text", "plain left-to-right text"},
	} {
		page := Page{Glyphs: run(tc.visual, 10, 100, 6, 10)}
		if got := page.Text(); got != tc.logical {
			t.Errorf("visual %q: text = %q, want %q", tc.visual, got, tc.logical)
		}
	}
}

// FuzzBidi checks that converting a line to logical order only reorders
// its pieces: none lost, none duplicated, none invented.
func FuzzBidi(f *testing.F) {
	f.Add("ملاعلاب ابحرم")
	f.Add("3.14 :םלוע )םולש(")
	f.Add("Price: 1,250.75 لكيش today")
	f.Fuzz(func(t *testing.T, s string) {
		pieces := strings.Split(s, "")
		got := logicalOrder(pieces)
		a, b := slices.Clone(pieces), slices.Clone(got)
		slices.Sort(a)
		slices.Sort(b)
		if !slices.Equal(a, b) {
			t.Fatalf("logicalOrder(%q) = %q: not a permutation", pieces, got)
		}
	})
}
