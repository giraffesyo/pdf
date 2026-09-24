package pdf

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/giraffesyo/pdf/pdftest"
)

// annotatedDoc is a one-page document whose page text is "Name:" and whose
// annotations are the objects from 6 on, listed in /Annots.
func annotatedDoc(annots ...string) []byte {
	refs := make([]string, len(annots))
	for i := range annots {
		refs[i] = itoa(6+i) + " 0 R"
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /DR << /Font << /Helv 5 0 R >> >> >> >>",
		pdftest.Pages(3),
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> " +
			"/Contents 4 0 R /Annots [" + strings.Join(refs, " ") + "] >>",
		pdftest.Stream("", "BT /F1 12 Tf 72 700 Td (Name:) Tj ET"),
		pdftest.Helvetica(),
	}
	return pdftest.Build(1, append(objs, annots...)...)
}

// TestAnnotationAppearanceText: a filled form field's value is painted by
// its widget's appearance stream, not the page content. It joins the
// page's text where the viewer shows it — the form's bounding box mapped
// onto the annotation rectangle — unless the caller opts out.
func TestAnnotationAppearanceText(t *testing.T) {
	// Stream objects cannot nest inside a dictionary, so the appearance is
	// its own object: the widget is object 6 and its appearance object 7.
	doc := annotatedDoc(
		"<< /Type /Annot /Subtype /Widget /Rect [120 695 320 715] /AP << /N 7 0 R >> >>",
		pdftest.Stream("/Subtype /Form /BBox [0 0 100 10] /Matrix [1 0 0 1 0 0]",
			"/Tx BMC BT /Helv 10 Tf 2 2 Td (Jane Doe) Tj ET EMC"),
	)
	d, err := extractOptions(t, doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != "Name: Jane Doe" {
		t.Errorf("text = %q", got)
	}
	// BBox [0 0 100 10] onto Rect [120 695 320 715] scales by 2: the
	// field's text origin (2, 2) lands at (124, 699) at size 20.
	var first Glyph
	for _, g := range d.Pages[0].Glyphs {
		if g.Text == "J" {
			first = g
		}
	}
	if math.Abs(first.X-124) > 0.01 || math.Abs(first.Y-699) > 0.01 || math.Abs(first.Size-20) > 0.01 {
		t.Errorf("appearance glyph at (%.2f, %.2f) size %.2f, want (124, 699) size 20", first.X, first.Y, first.Size)
	}

	d, err = extractOptions(t, doc, Options{IgnoreAnnotationAppearances: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != "Name:" {
		t.Errorf("text with IgnoreAnnotationAppearances = %q", got)
	}
}

// TestAnnotationAppearanceSelection: hidden annotations stay hidden, and
// an appearance with one stream per state paints the state /AS selects.
func TestAnnotationAppearanceSelection(t *testing.T) {
	form := func(text string) string {
		return pdftest.Stream("/Subtype /Form /BBox [0 0 100 20] /Resources << /Font << /Helv 5 0 R >> >>",
			"BT /Helv 10 Tf 2 5 Td ("+text+") Tj ET")
	}
	doc := annotatedDoc(
		"<< /Type /Annot /Subtype /Widget /F 2 /Rect [120 695 220 715] /AP << /N 9 0 R >> >>",
		"<< /Type /Annot /Subtype /Widget /AS /Yes /Rect [120 670 220 690] /AP << /N << /Yes 10 0 R /Off 11 0 R >> >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] >>",
		form("hidden"),
		form("checked"),
		form("unchecked"),
	)
	d, err := extractOptions(t, doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != "Name:\nchecked" {
		t.Errorf("text = %q", got)
	}
}

// TestFieldValuesWithoutAppearances: when the form asks viewers to
// regenerate appearances (/NeedAppearances), or a field has none, a text
// field's value, a combo box's selection, and a list box's options are
// drawn from the fields themselves — over any stale appearance.
func TestFieldValuesWithoutAppearances(t *testing.T) {
	fields := []string{
		"<< /Type /Annot /Subtype /Widget /FT /Tx /T (name) /V (Jane Doe) /DA (/Helv 0 Tf 0 g) " +
			"/Rect [120 690 320 712] /AP << /N 9 0 R >> >>",
		"<< /Type /Annot /Subtype /Widget /FT /Ch /Ff 131072 /T (size) /V (Large) /DA (/Helv 10 Tf 0 g) " +
			"/Rect [120 640 220 660] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Ch /T (color) /Opt [(Red) [(g) (Green)]] /DA (/Helv 10 Tf 0 g) " +
			"/Rect [300 560 400 620] >>",
		pdftest.Stream("/Subtype /Form /BBox [0 0 200 22] /Resources << /Font << /Helv 5 0 R >> >>",
			"BT /Helv 10 Tf 2 6 Td (stale) Tj ET"),
	}
	regenerate := annotatedDoc(fields...)
	regenerate = bytes.Replace(regenerate, []byte("/AcroForm <<"), []byte("/AcroForm << /NeedAppearances true"), 1)
	d, err := extractOptions(t, regenerate, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.Text(), "Name: Jane Doe\nLarge\nRed\nGreen"; got != want {
		t.Errorf("regenerated text = %q, want %q", got, want)
	}

	// Without /NeedAppearances the stored appearance is what a viewer shows.
	d, err = extractOptions(t, annotatedDoc(fields...), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.Text(), "Name: stale\nLarge\nRed\nGreen"; got != want {
		t.Errorf("stored-appearance text = %q, want %q", got, want)
	}
}
