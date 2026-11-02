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

// TestRightToLeftMarks: marks are drawn with no advance onto the letters
// they belong to. Text keeps each mark after its letter and lets no mark
// split a word or join two. The glyphs are those of real files: a vowelled
// Hebrew name (each vowel drawn before its letter, inside it), an Arabic
// word whose hamza is drawn lowered into the lam of the article, a damma
// drawn ahead of its letter, and vowelled Arabic whose marks are drawn at
// their letters' starts.
func TestRightToLeftMarks(t *testing.T) {
	g := func(text string, x, advance, y, size float64) Glyph {
		return Glyph{Text: text, X: x, Y: y, Advance: advance, Size: size}
	}
	hebrew := []Glyph{
		g("ל", 492.67, 7.974, 714.67, 18), g("ִ", 502.97, 0, 714.67, 18),
		g("י", 500.81, 5.976, 714.67, 18), g("ַ", 507.99, 0, 714.67, 18),
		g("ג", 506.78, 6.48, 714.67, 18), g("י", 513.26, 5.976, 714.67, 18),
		g("ִ", 522.84, 0, 714.67, 18), g("ב", 519.24, 8.46, 714.67, 18),
		g("ֲ", 530.02, 0, 714.67, 18), g("א", 527.63, 9.954, 714.67, 18),
	}
	arabic := []Glyph{
		g("ت", 557.59, 7.287, 65.17, 13), g("ا", 564.88, 2.692, 65.17, 13),
		g("ـ", 567.57, 0.67, 65.17, 13), g("ـ", 568.24, 0.67, 65.17, 13),
		g("ي", 568.91, 2.902, 65.17, 13), g("ن", 571.81, 2.902, 65.17, 13),
		g("م", 574.71, 3.217, 65.17, 13), g("أ", 579.21, 0, 61.70, 13),
		g("ل", 577.94, 4.596, 65.17, 13), g("ا", 582.54, 2.035, 65.17, 13),
	}
	// A word to its right drawn first, as its line was drawn in pieces,
	// makes layout sort the line by position, which puts the hamza after
	// the lam.
	arabic = append([]Glyph{g("و", 590, 4, 65.17, 13)}, arabic...)
	// "مِنَ عَنْ": each mark at its letter's start, drawn just before it,
	// where the letter to its right ends; the words a space apart.
	vowelled := []Glyph{
		g("ن", 100, 6, 200, 20), g("ْ", 106, 0, 200, 20), g("ن", 106, 4, 200, 20),
		g("َ", 110, 0, 200, 20), g("ع", 110, 7, 200, 20),
		g("َ", 122, 0, 200, 20), g("ن", 122, 6, 200, 20),
		g("ِ", 128, 0, 200, 20), g("م", 128, 7, 200, 20),
	}
	// "تُفق": the damma drawn just before its letter and a little left of
	// it, inside the letter before, as another producer places marks.
	ahead := []Glyph{
		g("\ufed8", 71.81, 18.192, 718.48, 24), g("\ufed4", 90.05, 17.712, 718.48, 24),
		g("\u064f", 106.61, 0, 719.54, 24), g("\ufe97", 107.81, 15.504, 718.48, 24),
	}
	for _, tc := range []struct {
		glyphs []Glyph
		want   string
	}{
		{ahead, "\ufe97\u064f\ufed4\ufed8"},
		{hebrew, "אֲבִיגַיִל"},
		{arabic, "و الأمنيــات"},
		{vowelled, "مِنَ عَنْن"},
	} {
		for _, mode := range []LayoutMode{LayoutPosition, LayoutColumns} {
			if got := (Page{Glyphs: tc.glyphs}).TextWithOptions(LayoutOptions{Mode: mode}); got != tc.want {
				t.Errorf("mode %d: text = %q, want %q", mode, got, tc.want)
			}
		}
	}

	// Left to right, a mark already follows its letter, also on a line
	// with right-to-left text; it must not make the next letter look a
	// word away.
	latin := []Glyph{g("e", 10, 6, 100, 10), g("́", 13, 0, 100, 10), g("t", 16, 6, 100, 10)}
	if got := (Page{Glyphs: latin}).Text(); got != "ét" {
		t.Errorf("latin text = %q", got)
	}
	mixed := append(slices.Clone(latin), run(" םולש", 22, 100, 6, 10)...)
	if got := (Page{Glyphs: mixed}).Text(); got != "שלום ét" {
		t.Errorf("mixed text = %q", got)
	}
}
