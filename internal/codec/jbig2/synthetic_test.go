package jbig2

import (
	"encoding/binary"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateSynthetic = flag.Bool("update", false, "rewrite the synthetic fixtures under testdata (then run testdata/gen.py to validate them)")

// synthCase is an encoder-built stream with its known lossless result.
type synthCase struct {
	name    string
	page    []byte
	globals []byte
	want    *bitmap // page-sized expected output
	// roundTripOnly marks streams using features the jbig2dec reference
	// decoder does not implement; they are checked by TestSynthetic but
	// not written as cross-validated fixtures.
	roundTripOnly bool
}

// syntheticCases builds the test streams described in encoder_test.go.
func syntheticCases() []synthCase {
	var cases []synthCase
	add := func(name string, want *bitmap, page, globals []byte) {
		cases = append(cases, synthCase{name: name, page: page, globals: globals, want: want})
	}
	addRoundTripOnly := func(name string, want *bitmap, page, globals []byte) {
		cases = append(cases, synthCase{name: name, page: page, globals: globals, want: want, roundTripOnly: true})
	}

	// Generic regions: every template, TPGDON on/off, nominal and
	// non-nominal AT pixels, on an image wide enough for 16-pixel
	// contexts and with an odd width so row padding matters.
	img := testImage(133, 61)
	for tmpl := range uint8(4) {
		for _, tpgdon := range []bool{false, true} {
			for _, nominal := range []bool{true, false} {
				p := &genericParams{template: tmpl, tpgdon: tpgdon, at: nominalAT[tmpl]}
				if !nominal {
					p.at = [4][2]int{{-5, -3}, {4, -2}, {-1, -4}, {6, -1}}
					if tmpl > 0 {
						p.at[0] = [2]int{-2, 0} // current-row AT, the other legal shape
					}
				}
				name := "synth-generic-t" + string(rune('0'+tmpl))
				if tpgdon {
					name += "-tpgdon"
				}
				if !nominal {
					name += "-at"
				}
				w := &segWriter{}
				w.segment(segPageInfo, nil, pageInfoData(img.w, img.h, false, 0, opOr), false)
				w.segment(segGenericLossless, nil, genericRegionData(img, 0, 0, opOr, p, false), false)
				w.segment(segEndOfPage, nil, nil, false)
				add(name, img, w.buf, nil)
			}
		}
	}

	// Tiny and odd sizes through the generic path.
	for _, sz := range [][2]int{{1, 1}, {8, 3}, {37, 13}, {9, 70}} {
		im := testImage(sz[0], sz[1])
		p := &genericParams{template: 0, tpgdon: true, at: nominalAT[0]}
		w := &segWriter{}
		w.segment(segPageInfo, nil, pageInfoData(im.w, im.h, false, 0, opOr), false)
		w.segment(segGenericImmediate, nil, genericRegionData(im, 0, 0, opOr, p, false), false)
		add("synth-size-"+itoa(sz[0])+"x"+itoa(sz[1]), im, w.buf, nil)
	}

	// Composition: page default pixel 1 and every operator, with regions
	// placed at unaligned offsets and partly outside the page.
	{
		pageW, pageH := 70, 40
		img2 := testImage(40, 25)
		for _, op := range []combOp{opOr, opAnd, opXor, opXnor, opReplace} {
			for _, def := range []int{0, 1} {
				want, _ := newBitmap(pageW, pageH)
				if def == 1 {
					want.fill(1)
				}
				p := &genericParams{template: 0, at: nominalAT[0]}
				w := &segWriter{}
				w.segment(segPageInfo, nil, pageInfoData(pageW, pageH, false, def, op), false)
				for i, pos := range [][2]int{{3, 2}, {45, 20}, {-0 + 60, 30}} {
					// A second region placed with replace keeps the test
					// of "op" meaningful even over black pages.
					rop := op
					if i == 2 {
						rop = opReplace
					}
					w.segment(segGenericLossless, nil, genericRegionData(img2, pos[0], pos[1], rop, p, false), false)
					want.compose(img2, pos[0], pos[1], rop)
				}
				add("synth-compose-op"+itoa(int(op))+"-def"+itoa(def), want, w.buf, nil)
			}
		}
	}

	// Unknown data length (7.2.7) with unknown region height, and an
	// unknown page height closed by end-of-stripe segments.
	{
		im := testImage(50, 30)
		p := &genericParams{template: 0, tpgdon: true, at: nominalAT[0]}
		w := &segWriter{}
		w.segment(segPageInfo, nil, pageInfoData(im.w, im.h, true, 0, opOr), false)
		w.segment(segGenericImmediate, nil, genericRegionData(im, 0, 0, opOr, p, true), true)
		w.segment(segEndOfStripe, nil, binary.BigEndian.AppendUint32(nil, 29), false)
		w.segment(segEndOfPage, nil, nil, false)
		w.segment(segEndOfFile, nil, nil, false)
		add("synth-unknown-length", im, w.buf, nil)
	}

	// Symbol dictionaries and text regions.
	var syms []*bitmap
	for i := range 12 {
		syms = append(syms, glyph(i, 5+i%4*3, 7+i/4*4))
	}
	// Sort by (h, w) to match dictionary coding order.
	for i := 1; i < len(syms); i++ {
		for j := i; j > 0 && (syms[j-1].h > syms[j].h || (syms[j-1].h == syms[j].h && syms[j-1].w > syms[j].w)); j-- {
			syms[j-1], syms[j] = syms[j], syms[j-1]
		}
	}
	layout := func(n int, transposed bool) []textInstance {
		var inst []textInstance
		x, y := 4, 3
		for i := range n {
			id := (i*7 + 3) % len(syms)
			inst = append(inst, textInstance{id: id, x: x, y: y})
			if !transposed {
				x += syms[id].w + 1 + i%3
				if x > 150 {
					x, y = 4+i%5, y+20
				}
			} else {
				y += syms[id].h + 1 + i%3
				if y > 150 {
					x, y = x+20, 3+i%5
				}
			}
		}
		return inst
	}
	render := func(w, h int, inst []textInstance, op combOp, def int) *bitmap {
		bm, _ := newBitmap(w, h)
		if def == 1 {
			bm.fill(1)
		}
		for _, in := range inst {
			bm.compose(syms[in.id], in.x, in.y, op)
		}
		return bm
	}
	for corner := range uint8(4) {
		for _, transposed := range []bool{false, true} {
			for _, logStrips := range []uint{0, 2} {
				pw, ph := 180, 110
				if transposed {
					pw, ph = 110, 180
				}
				inst := layout(60, transposed)
				spec := &textRegionSpec{w: pw, h: ph, syms: syms, inst: inst, logStrips: logStrips, refCorner: corner, transposed: transposed, combOp: opOr, dsOffset: -2 + int(corner)}
				sd := &symbolDictSpec{syms: syms, template: 0, at: nominalAT[0]}
				// Half the cases carry the dictionary in a globals stream.
				var globals []byte
				w := &segWriter{}
				g := &segWriter{}
				var dictNum uint32
				if logStrips == 0 {
					dictNum = g.segment(segSymbolDict, nil, symbolDictData(sd, make([]mqCx, 1<<16)), false)
					globals = g.buf
					w.next = 1
				} else {
					dictNum = w.segment(segSymbolDict, nil, symbolDictData(sd, make([]mqCx, 1<<16)), false)
				}
				w.segment(segPageInfo, nil, pageInfoData(pw, ph, false, 0, opOr), false)
				w.segment(segTextRegionLossless, []uint32{dictNum}, textRegionData(spec), false)
				name := "synth-text-corner" + itoa(int(corner)) + "-strips" + itoa(1<<logStrips)
				if transposed {
					name += "-transposed"
				}
				add(name, render(pw, ph, inst, opOr, 0), w.buf, globals)
			}
		}
	}

	// Text region features: SBDEFPIXEL=1 with AND, a text region placed
	// at an offset inside a larger page with an external XOR, a
	// refinement-enabled region whose instances all have RI = 0, seven
	// dictionaries (so the region uses the long referral form; seven
	// rather than five because jbig2dec sizes the retain-bit field as
	// floor((R+1)/8) bytes where the spec says ceil, and the two agree
	// at R = 7) coded with templates 1 and 2, the second importing and
	// re-exporting the first's symbols. The ctxReuse variant additionally retains the
	// first dictionary's coding contexts and reuses them in the second
	// (7.4.3.1.1 bits 8–9), which jbig2dec does not implement.
	for _, ctxReuse := range []bool{false, true} {
		// The region's symbol list is the concatenation of the exports
		// of the referred-to dictionaries, so d1's symbols appear twice.
		parts := [][]*bitmap{syms[:3], syms[3:5], syms[5:6], syms[6:7], syms[7:8], syms[8:10], syms[10:]}
		var all []*bitmap
		all = append(all, parts[0]...)
		all = append(all, parts[0]...)
		for _, p := range parts[1:] {
			all = append(all, p...)
		}
		inst := layout(40, false)
		for i := range inst {
			inst[i].id = (inst[i].id * 5) % len(all)
		}
		pw, ph := 200, 130
		spec := &textRegionSpec{w: 180, h: 110, x: 13, y: 9, op: opXor, syms: all, inst: inst, logStrips: 1, refCorner: refTopLeft, combOp: opAnd, defPixel: 1, dsOffset: 3, refine: true}
		region, _ := newBitmap(180, 110)
		region.fill(1)
		for _, in := range inst {
			region.compose(all[in.id], in.x, in.y, opAnd)
		}
		want, _ := newBitmap(pw, ph)
		want.compose(region, 13, 9, opXor)

		w := &segWriter{}
		cx := make([]mqCx, 1<<16)
		d1 := &symbolDictSpec{syms: parts[0], template: 2, at: nominalAT[2], ctxRetained: ctxReuse}
		n1 := w.segment(segSymbolDict, nil, symbolDictData(d1, cx), false)
		if !ctxReuse {
			cx = make([]mqCx, 1<<16)
		}
		d2 := &symbolDictSpec{syms: parts[1], numInput: len(parts[0]), exportInputs: true, template: 2, at: nominalAT[2], ctxUsed: ctxReuse}
		refs := []uint32{n1, w.segment(segSymbolDict, []uint32{n1}, symbolDictData(d2, cx), false)}
		for _, p := range parts[2:] {
			d := &symbolDictSpec{syms: p, template: 1, at: nominalAT[1]}
			refs = append(refs, w.segment(segSymbolDict, nil, symbolDictData(d, make([]mqCx, 1<<16)), false))
		}
		w.segment(segPageInfo, nil, pageInfoData(pw, ph, false, 0, opOr), false)
		w.segment(segTextRegionImmediate, refs, textRegionData(spec), false)
		if ctxReuse {
			addRoundTripOnly("synth-text-ctxreuse", want, w.buf, nil)
		} else {
			add("synth-text-features", want, w.buf, nil)
		}
	}
	cases = append(cases, refinementCases()...)
	return cases
}

// refinementCases builds the refinement streams: refinement regions over
// the page and over an intermediate region, refined text instances, and
// dictionaries of refined and aggregate symbols, across both templates,
// moved AT pixels and typical prediction.
func refinementCases() []synthCase {
	var cases []synthCase
	add := func(name string, want *bitmap, page, globals []byte, roundTripOnly bool) {
		cases = append(cases, synthCase{name: name, page: page, globals: globals, want: want, roundTripOnly: roundTripOnly})
	}
	nominalRAT := [2][2]int{{-1, -1}, {-1, -1}}
	movedRAT := [2][2]int{{-2, -1}, {2, 1}}

	// A refinement region over the page: a generic region draws img, and
	// the refinement replaces it with an edited copy. jbig2dec refines
	// the whole page without offsetting it by the region's location, so
	// the region sits at the origin where the two readings agree.
	img := testImage(133, 61)
	edited, _ := newBitmap(img.w, img.h)
	edited.compose(img, 0, 0, opOr)
	for y := 5; y < img.h; y += 7 {
		for x := y % 11; x < img.w; x += 13 {
			edited.data[y*edited.stride+x>>3] ^= 0x80 >> uint(x&7)
		}
	}
	for _, c := range []struct {
		name     string
		template uint8
		at       [2][2]int
		tpgron   bool
	}{
		{"t0", 0, nominalRAT, false},
		{"t0-at", 0, movedRAT, false},
		{"t0-tpgron", 0, nominalRAT, true},
		{"t1", 1, nominalRAT, false},
		{"t1-tpgron", 1, nominalRAT, true},
	} {
		gp := &genericParams{template: 0, at: nominalAT[0]}
		w := &segWriter{}
		w.segment(segPageInfo, nil, pageInfoData(img.w, img.h, false, 0, opOr), false)
		w.segment(segGenericImmediate, nil, genericRegionData(img, 0, 0, opOr, gp, false), false)
		w.segment(segRefinementImmediate, nil, refinementRegionData(edited, img, 0, 0, opReplace, c.template, c.at, c.tpgron), false)
		w.segment(segEndOfPage, nil, nil, false)
		add("synth-refine-region-"+c.name, edited, w.buf, nil, false)
	}

	// An intermediate text region refined into place: the intermediate
	// result is not drawn, and the refinement of it is, at an offset.
	syms := []*bitmap{glyph(0, 9, 11), glyph(1, 7, 11), glyph(2, 12, 9), glyph(3, 10, 10)}
	{
		inst := []textInstance{{id: 0, x: 1, y: 2}, {id: 1, x: 12, y: 2}, {id: 2, x: 21, y: 4}, {id: 3, x: 35, y: 1}}
		tw, th := 48, 14
		inter, _ := newBitmap(tw, th)
		for _, in := range inst {
			inter.compose(syms[in.id], in.x, in.y, opOr)
		}
		refined, _ := newBitmap(tw, th)
		refined.compose(inter, 0, 0, opOr)
		for x := 0; x < tw; x += 5 {
			refined.data[(th/2)*refined.stride+x>>3] ^= 0x80 >> uint(x&7)
		}
		pw, ph := 70, 30
		want, _ := newBitmap(pw, ph)
		want.compose(refined, 9, 7, opOr)
		w := &segWriter{}
		d := w.segment(segSymbolDict, nil, symbolDictData(&symbolDictSpec{syms: sortSyms(syms), at: nominalAT[0]}, make([]mqCx, 1<<16)), false)
		w.segment(segPageInfo, nil, pageInfoData(pw, ph, false, 0, opOr), false)
		spec := &textRegionSpec{w: tw, h: th, op: opOr, syms: sortSyms(syms), inst: remapInst(inst, syms), refCorner: refTopLeft}
		t := w.segment(segTextRegionIntermediate, []uint32{d}, textRegionData(spec), false)
		w.segment(segRefinementImmediate, []uint32{t}, refinementRegionData(refined, inter, 9, 7, opOr, 0, nominalRAT, true), false)
		w.segment(segEndOfPage, nil, nil, false)
		add("synth-refine-intermediate", want, w.buf, nil, false)
	}

	// Refined text instances: grown, shrunk and shifted symbols among
	// plain ones, in both refinement templates.
	sorted := sortSyms(syms)
	for _, c := range []struct {
		name     string
		template uint8
		at       [2][2]int
	}{
		{"t0", 0, nominalRAT},
		{"t0-at", 0, movedRAT},
		{"t1", 1, nominalRAT},
	} {
		inst := []textInstance{
			{id: 0, x: 2, y: 3},
			{id: 1, x: 16, y: 3, refined: refine(sorted[1], 3, 2, 0, 0)},
			{id: 2, x: 34, y: 4, refined: refine(sorted[2], -2, -1, 1, -1)},
			{id: 3, x: 50, y: 3},
			{id: 0, x: 2, y: 24, refined: refine(sorted[0], 0, 0, -1, 1)},
			{id: 3, x: 20, y: 24, refined: refine(sorted[3], 5, -3, 2, 0)},
		}
		pw, ph := 72, 44
		want, _ := newBitmap(pw, ph)
		for _, in := range inst {
			want.compose(in.drawn(sorted), in.x, in.y, opOr)
		}
		w := &segWriter{}
		d := w.segment(segSymbolDict, nil, symbolDictData(&symbolDictSpec{syms: sorted, at: nominalAT[0]}, make([]mqCx, 1<<16)), false)
		w.segment(segPageInfo, nil, pageInfoData(pw, ph, false, 0, opOr), false)
		spec := &textRegionSpec{w: pw, h: ph, op: opOr, syms: sorted, inst: inst, refCorner: refTopLeft, refine: true, rTemplate: c.template, rAT: c.at}
		w.segment(segTextRegionImmediate, []uint32{d}, textRegionData(spec), false)
		w.segment(segEndOfPage, nil, nil, false)
		add("synth-text-refined-"+c.name, want, w.buf, nil, false)
	}

	// Refined and aggregate symbols: a plain dictionary in the globals,
	// and a dictionary of symbols refined from it or assembled from its
	// symbols and its own earlier ones, which a text region then draws.
	// The ctxreuse variant retains the first refagg dictionary's contexts
	// and reuses them in a second, which jbig2dec does not implement.
	// jbig2dec also leaves SBRAT at (0, 0) instead of SDRAT (Table 17) in
	// the text region an aggregate is drawn with, and rejects that as out
	// of field, so an instance refined inside an aggregate is checked
	// against it only with template 1, which has no AT pixels; the
	// template 0 variant is round-trip only.
	for _, c := range []struct {
		name        string
		template    uint8
		at          [2][2]int
		refineInAgg bool
		ctxReuse    bool
		jbig2decGap bool
	}{
		{"t0", 0, nominalRAT, false, false, false},
		{"t1", 1, movedRAT, true, false, false},
		{"t0-refined", 0, movedRAT, true, false, true},
		{"ctxreuse", 0, movedRAT, true, true, true},
	} {
		base := sorted
		single := refine(base[1], 1, 1, 0, 0).bm // a refinement of base[1]
		pair := aggregate(base, 22, 12, []textInstance{{id: 0, x: 0, y: 0}, {id: 1, x: 10, y: 1}})
		// The third aggregates an earlier new symbol (single, index 4)
		// and a refined base symbol.
		trioInst := []textInstance{
			{id: 4, x: 0, y: 0},
			{id: 3, x: 9, y: 1},
			{id: 2, x: 21, y: 2},
		}
		if c.refineInAgg {
			trioInst[1].refined = refine(base[3], 1, 0, 0, 0)
		}
		newSyms := []*bitmap{single, pair}
		agg := []aggSym{{id: 1}, {inst: []textInstance{{id: 0, x: 0, y: 0}, {id: 1, x: 10, y: 1}}}}
		trio := aggregate(append(append([]*bitmap(nil), base...), newSyms...), 34, 13, trioInst)
		newSyms = append(newSyms, trio)
		agg = append(agg, aggSym{inst: trioInst})
		// Height classes run in ascending height: 8x12, 22x12, 34x13.
		for i := 1; i < len(newSyms); i++ {
			if newSyms[i].h < newSyms[i-1].h || newSyms[i].h == newSyms[i-1].h && newSyms[i].w < newSyms[i-1].w {
				panic("refagg symbols out of coding order")
			}
		}

		gw := &segWriter{}
		d1 := gw.segment(segSymbolDict, nil, symbolDictData(&symbolDictSpec{syms: base, at: nominalAT[0]}, make([]mqCx, 1<<16)), false)
		gr := make([]mqCx, refineContexts)
		spec2 := &symbolDictSpec{
			syms: newSyms, numInput: len(base), inputSyms: base, at: nominalAT[0],
			refAgg: true, rTemplate: c.template, rAT: c.at, agg: agg, gr: gr, ctxRetained: c.ctxReuse,
		}
		cx := make([]mqCx, 1<<16)
		w := &segWriter{next: gw.next}
		d2 := w.segment(segSymbolDict, []uint32{d1}, symbolDictData(spec2, cx), false)
		exports := newSyms
		refs := []uint32{d2}
		if c.ctxReuse {
			// A second refagg dictionary over d2's exports, coded with
			// the contexts d2 left behind.
			more := refine(newSyms[0], 2, 0, 0, 0).bm
			spec3 := &symbolDictSpec{
				syms: []*bitmap{more}, numInput: len(newSyms), inputSyms: newSyms, at: nominalAT[0],
				refAgg: true, rTemplate: c.template, rAT: c.at, agg: []aggSym{{id: 0}}, gr: gr, ctxUsed: true,
			}
			refs = append(refs, w.segment(segSymbolDict, []uint32{d2}, symbolDictData(spec3, cx), false))
			exports = append(append([]*bitmap(nil), newSyms...), more)
		}
		w.segment(segPageInfo, nil, pageInfoData(90, 40, false, 0, opOr), false)
		var inst []textInstance
		x := 1
		for i := range exports {
			inst = append(inst, textInstance{id: i, x: x, y: 2 + i%2*18})
			x += exports[i].w + 2
		}
		pw, ph := 90, 40
		want, _ := newBitmap(pw, ph)
		for _, in := range inst {
			want.compose(exports[in.id], in.x, in.y, opOr)
		}
		spec := &textRegionSpec{w: pw, h: ph, op: opOr, syms: exports, inst: inst, refCorner: refTopLeft}
		w.segment(segTextRegionImmediate, refs, textRegionData(spec), false)
		w.segment(segEndOfPage, nil, nil, false)
		add("synth-symbol-refagg-"+c.name, want, w.buf, gw.buf, c.jbig2decGap)
	}
	return cases
}

// sortSyms returns syms in symbol dictionary coding order: ascending
// height, then ascending width.
func sortSyms(syms []*bitmap) []*bitmap {
	out := append([]*bitmap(nil), syms...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && (out[j].h < out[j-1].h || out[j].h == out[j-1].h && out[j].w < out[j-1].w); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// remapInst renumbers instances of syms to the order sortSyms gives.
func remapInst(inst []textInstance, syms []*bitmap) []textInstance {
	sorted := sortSyms(syms)
	out := append([]textInstance(nil), inst...)
	for i := range out {
		sym := syms[out[i].id]
		for j, s := range sorted {
			if s == sym {
				out[i].id = j
			}
		}
	}
	return out
}

func itoa(i int) string {
	if i < 0 {
		return "-" + itoa(-i)
	}
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// TestSynthetic round-trips every encoder-built stream through Decode.
// The same streams, once written with -update and validated against
// jbig2dec by testdata/gen.py, are also covered by TestFixtures.
func TestSynthetic(t *testing.T) {
	for _, c := range syntheticCases() {
		t.Run(c.name, func(t *testing.T) {
			got, err := Decode(c.page, c.globals, c.want.w, c.want.h)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if diff := firstDiff(got, c.want.data); diff >= 0 {
				t.Fatalf("bitmap differs from the encoded image at byte %d", diff)
			}
		})
	}
}

func firstDiff(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}

// TestWriteSyntheticFixtures writes the synthetic streams and their
// source bitmaps to testdata when run with -update; gen.py then checks
// them against jbig2dec and installs the expected PBMs.
func TestWriteSyntheticFixtures(t *testing.T) {
	if !*updateSynthetic {
		t.Skip("run with -update to rewrite synthetic fixtures")
	}
	for _, c := range syntheticCases() {
		if c.roundTripOnly {
			continue
		}
		base := filepath.Join("testdata", c.name)
		if err := os.WriteFile(base+".jb2", c.page, 0o600); err != nil {
			t.Fatal(err)
		}
		if c.globals != nil {
			if err := os.WriteFile(base+".globals", c.globals, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(base+".src.pbm", pbmBytes(c.want), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
