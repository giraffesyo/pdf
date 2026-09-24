package cjk

import "testing"

// TestUnicode checks CIDs against Adobe's Unicode CMaps, the tables'
// source: each pair appears there as a mapping from the character.
func TestUnicode(t *testing.T) {
	for _, tc := range []struct {
		ordering string
		cid      uint32
		want     rune
	}{
		{"Japan1", 1, ' '},
		{"Japan1", 34, 'A'},
		{"Japan1", 1125, '亜'},
		{"GB1", 4559, '中'},
		{"CNS1", 661, '中'},
		{"Korea1", 1086, '가'},
		{"Korea1", 1087, '각'}, // within a range
		{"KR", 221, '가'},
	} {
		got, ok := Unicode(tc.ordering, tc.cid)
		if !ok || got != tc.want {
			t.Errorf("Unicode(%s, %d) = %q, %v; want %q", tc.ordering, tc.cid, got, ok, tc.want)
		}
	}
	if _, ok := Unicode("Japan1", 0); ok {
		t.Error("CID 0, .notdef, has a character")
	}
	if _, ok := Unicode("Identity", 34); ok {
		t.Error("an unknown collection has characters")
	}
}
