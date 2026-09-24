package stdfont

import "testing"

// TestWidths checks widths against the Core 14 AFM files they come from.
func TestWidths(t *testing.T) {
	for _, tc := range []struct {
		font string
		code byte
		text string
		want float64
	}{
		{"Times-Roman", 'A', "A", 722},
		{"TimesNewRomanPSMT", 'e', "e", 444},
		{"TimesNewRoman,Bold", 'e', "e", 444},
		{"Times-Italic", 'e', "e", 444},
		{"Times-BoldItalic", 'W', "W", 889},
		{"ABCDEF+Arial-BoldMT", 'i', "i", 278},
		{"Helvetica", 'm', "m", 833},
		{"Helvetica-Oblique", 'm', "m", 833},
		{"Courier-Bold", 'W', "W", 600},
		{"Symbol", 0x61, "α", 631},
		{"ZapfDingbats", 0x34, "✔", 846},
		{"Times-Roman", 0x93, "“", 444},
	} {
		f, ok := Lookup(tc.font)
		if !ok {
			t.Errorf("Lookup(%q) failed", tc.font)
			continue
		}
		if got, ok := f.Width(tc.code, tc.text); !ok || got != tc.want {
			t.Errorf("%s %q: width %v, %v; want %v", tc.font, tc.text, got, ok, tc.want)
		}
	}
	if _, ok := Lookup("Garamond"); ok {
		t.Error("Garamond is not a standard font")
	}
}
