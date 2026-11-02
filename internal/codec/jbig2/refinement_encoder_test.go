package jbig2

// The refinement half of the test encoder in encoder_test.go: the generic
// refinement procedure (6.3) with typical prediction, refined text region
// instances (6.4.11), and aggregate symbols (6.5.8.2). Like the rest of
// the encoder, its streams are cross-checked against jbig2dec by
// testdata/gen.py.

// refineContextEnc computes CONTEXT for pixel (x, y) of the bitmap being
// coded from the template pixels of Figures 12 (GRTEMPLATE 0) and 13
// (GRTEMPLATE 1), in the bit order the reference decoder uses.
func refineContextEnc(p *refineParams, bm *bitmap, x, y int) uint32 {
	cur := func(dx, dy int) uint32 { return bm.get(x+dx, y+dy) }
	ref := func(dx, dy int) uint32 { return p.ref.get(x-p.dx+dx, y-p.dy+dy) }
	if p.template == 0 {
		return ref(p.at[1][0], p.at[1][1])<<12 |
			ref(0, -1)<<11 | ref(1, -1)<<10 |
			ref(-1, 0)<<9 | ref(0, 0)<<8 | ref(1, 0)<<7 |
			ref(-1, 1)<<6 | ref(0, 1)<<5 | ref(1, 1)<<4 |
			cur(p.at[0][0], p.at[0][1])<<3 |
			cur(0, -1)<<2 | cur(1, -1)<<1 | cur(-1, 0)
	}
	return ref(0, -1)<<9 |
		ref(-1, 0)<<8 | ref(0, 0)<<7 | ref(1, 0)<<6 |
		ref(0, 1)<<5 | ref(1, 1)<<4 |
		cur(-1, -1)<<3 | cur(0, -1)<<2 | cur(1, -1)<<1 | cur(-1, 0)
}

// predictable reports the value typical prediction implies for pixel
// (x, y): that of the reference pixel it corresponds to when all nine
// pixels around it agree.
func predictable(p *refineParams, x, y int) (uint32, bool) {
	v := p.ref.get(x-p.dx, y-p.dy)
	for j := -1; j <= 1; j++ {
		for i := -1; i <= 1; i++ {
			if p.ref.get(x-p.dx+i, y-p.dy+j) != v {
				return 0, false
			}
		}
	}
	return v, true
}

// encodeRefinement codes bm against p.ref with the generic refinement
// procedure. With p.tpgron, a row is marked typical when every pixel
// prediction would make agrees with bm, and its predicted pixels are
// then left uncoded.
func encodeRefinement(e *mqEncoder, cx []mqCx, bm *bitmap, p *refineParams) {
	sltp := [2]uint32{0x100, 0x040}[p.template]
	ltp := uint32(0)
	for y := range bm.h {
		if p.tpgron {
			cur := uint32(1)
			for x := range bm.w {
				if v, ok := predictable(p, x, y); ok && v != bm.get(x, y) {
					cur = 0
					break
				}
			}
			e.encode(&cx[sltp], cur^ltp)
			ltp = cur
		}
		for x := range bm.w {
			if ltp == 1 {
				if _, ok := predictable(p, x, y); ok {
					continue
				}
			}
			e.encode(&cx[refineContextEnc(p, bm, x, y)], bm.get(x, y))
		}
	}
}

// refinedInst refines an instance's symbol: bm is the bitmap drawn in
// its place, rdw and rdh its size relative to the symbol, and rdx and
// rdy the further offset of the symbol within it (6.4.11).
type refinedInst struct {
	bm       *bitmap
	rdw, rdh int
	rdx, rdy int
}

// drawn returns the bitmap an instance places: its refinement, if any.
func (in textInstance) drawn(syms []*bitmap) *bitmap {
	if in.refined != nil {
		return in.refined.bm
	}
	return syms[in.id]
}

// textEncCtx is the encoder's counterpart of textCtx: the contexts of
// one text region, or of every aggregate symbol of one dictionary.
type textEncCtx struct {
	iadt, iafs, iads, iait, iari intCtx
	iardw, iardh, iardx, iardy   intCtx
	iaid                         []mqCx
	codeLen                      uint
	gr                           []mqCx
}

func newTextEncCtx(numSyms int) *textEncCtx {
	codeLen := symCodeLen(numSyms)
	return &textEncCtx{iaid: make([]mqCx, 1<<(codeLen+1)), codeLen: codeLen, gr: make([]mqCx, refineContexts)}
}

// encodeRefinedInst codes an instance's RI and, when it is refined, its
// size and offset deltas and the refined bitmap.
func encodeRefinedInst(e *mqEncoder, tc *textEncCtx, sym *bitmap, r *refinedInst, rTemplate uint8, rAT [2][2]int) {
	if r == nil {
		e.encodeInt(&tc.iari, 0, false)
		return
	}
	e.encodeInt(&tc.iari, 1, false)
	e.encodeInt(&tc.iardw, r.rdw, false)
	e.encodeInt(&tc.iardh, r.rdh, false)
	e.encodeInt(&tc.iardx, r.rdx, false)
	e.encodeInt(&tc.iardy, r.rdy, false)
	floorHalf := func(v int) int {
		if v < 0 {
			return -((-v + 1) / 2)
		}
		return v / 2
	}
	p := &refineParams{
		template: rTemplate, at: rAT, ref: sym,
		dx: floorHalf(r.rdw) + r.rdx,
		dy: floorHalf(r.rdh) + r.rdy,
	}
	encodeRefinement(e, tc.gr, r.bm, p)
}

// refine makes a refined instance of sym: the symbol grown or shrunk by
// (rdw, rdh), shifted by the offset the decoder will derive, and edited
// so the refinement has pixels of its own to code.
func refine(sym *bitmap, rdw, rdh, rdx, rdy int) *refinedInst {
	bm, err := newBitmap(sym.w+rdw, sym.h+rdh)
	if err != nil {
		panic(err)
	}
	floorHalf := func(v int) int {
		if v < 0 {
			return -((-v + 1) / 2)
		}
		return v / 2
	}
	bm.compose(sym, floorHalf(rdw)+rdx, floorHalf(rdh)+rdy, opOr)
	for i := 0; i < min(bm.w, bm.h); i += 3 {
		bm.data[i*bm.stride+(bm.w-1-i)>>3] ^= 0x80 >> uint((bm.w-1-i)&7)
	}
	return &refinedInst{bm: bm, rdw: rdw, rdh: rdh, rdx: rdx, rdy: rdy}
}

// aggSym describes how a symbol of a dictionary with SDREFAGG is coded:
// as several instances of earlier symbols (len(inst) > 1), or as one
// earlier symbol, id, refined with offset (rdx, rdy).
type aggSym struct {
	inst     []textInstance
	id       int
	rdx, rdy int
}

// aggregate draws the instances of an aggregate symbol into a w×h bitmap
// as the decoder will: ORed in place, top-left corners.
func aggregate(syms []*bitmap, w, h int, inst []textInstance) *bitmap {
	bm, err := newBitmap(w, h)
	if err != nil {
		panic(err)
	}
	for _, in := range inst {
		bm.compose(in.drawn(syms), in.x, in.y, opOr)
	}
	return bm
}

// refinementRegionData builds a refinement region segment body (7.4.7)
// coding bm against ref.
func refinementRegionData(bm, ref *bitmap, x, y int, op combOp, template uint8, at [2][2]int, tpgron bool) []byte {
	b := regionInfoData(bm.w, bm.h, x, y, op)
	flags := template
	if tpgron {
		flags |= 2
	}
	b = append(b, flags)
	if template == 0 {
		b = append(b, i8(at[0][0]), i8(at[0][1]), i8(at[1][0]), i8(at[1][1]))
	}
	e := newMQEncoder()
	encodeRefinement(e, make([]mqCx, refineContexts), bm, &refineParams{template: template, at: at, ref: ref, tpgron: tpgron})
	return append(b, e.flush()...)
}
