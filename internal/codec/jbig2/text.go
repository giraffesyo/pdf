package jbig2

import (
	"errors"
	"fmt"
)

// Text region segments (T.88 §6.4, §7.4.4) with arithmetic coding.

// maxInstances bounds SBNUMINSTANCES; the work budget bounds the
// compositing cost of each instance on top of that.
const maxInstances = 1 << 24

// textRegion holds the parsed text region segment data header (7.4.4.1).
type textRegion struct {
	info       regionInfo
	logStrips  uint
	refCorner  uint8
	transposed bool
	combOp     combOp
	defPixel   uint32
	dsOffset   int
	numInst    uint32
	refine     bool      // SBREFINE: an RI value precedes each instance
	rTemplate  uint8     // SBRTEMPLATE
	rAT        [2][2]int // SBRAT; template 0 only
}

// textCtx holds the arithmetic contexts of the text region decoding
// procedure. A text region segment starts with a fresh set; a symbol
// dictionary that aggregates symbols keeps one set for all of them
// (6.5.8.2, Table 17), so it is passed in rather than made per call.
type textCtx struct {
	iadt, iafs, iads, iait, iari intCtx
	iardw, iardh, iardx, iardy   intCtx
	iaid                         []mqCx
	codeLen                      uint   // SBSYMCODELEN
	gr                           []mqCx // refinement contexts
}

func newTextCtx(codeLen uint) *textCtx {
	return &textCtx{iaid: make([]mqCx, 1<<(codeLen+1)), codeLen: codeLen}
}

// decodeTextRegion parses and decodes a type 4/6/7 segment body using the
// exports of the referred-to symbol dictionaries as SBSYMS.
func (d *decoder) decodeTextRegion(data []byte, dicts []*symbolDict) (*bitmap, *regionInfo, error) {
	r := &reader{data: data}
	var t textRegion
	var err error
	if t.info, err = readRegionInfo(r); err != nil {
		return nil, nil, err
	}
	flags, err := r.u16()
	if err != nil {
		return nil, nil, err
	}
	sbHuff := flags&1 != 0
	refine := flags>>1&1 != 0
	t.logStrips = uint(flags >> 2 & 3)
	t.refCorner = uint8(flags >> 4 & 3)
	t.transposed = flags>>6&1 != 0
	t.combOp = combOp(flags >> 7 & 3)
	t.defPixel = uint32(flags >> 9 & 1)
	t.dsOffset = int(flags >> 10 & 0x1F)
	if t.dsOffset > 15 {
		t.dsOffset -= 32 // signed 5-bit field
	}
	t.rTemplate = uint8(flags >> 15 & 1)
	if sbHuff {
		return nil, nil, fmt.Errorf("huffman-coded text region: %w", errors.ErrUnsupported)
	}
	if refine && t.rTemplate == 0 {
		if t.rAT, err = readRefineAT(r); err != nil {
			return nil, nil, err
		}
	}
	t.refine = refine
	if t.numInst, err = r.u32(); err != nil {
		return nil, nil, err
	}
	if t.numInst > maxInstances {
		return nil, nil, fmt.Errorf("text region declares %d instances", t.numInst)
	}
	var syms []*bitmap
	for _, sd := range dicts {
		syms = append(syms, sd.exported...)
	}
	if len(syms) == 0 {
		return nil, nil, errors.New("text region refers to no symbols")
	}
	// Symbols are placed by coordinates, not context, so the region bitmap
	// only needs its page-visible part.
	t.info.h = d.visibleRows(&t.info)
	t.info.w = min(t.info.w, max(d.page.w-t.info.x, 0))
	if err := d.charge(t.info.w, t.info.h); err != nil {
		return nil, nil, err
	}
	bm, err := newBitmap(t.info.w, t.info.h)
	if err != nil {
		return nil, nil, err
	}
	if t.defPixel != 0 {
		bm.fill(1)
	}
	tc := newTextCtx(symCodeLen(len(syms)))
	if refine {
		tc.gr = make([]mqCx, refineContexts)
	}
	if err := d.decodeTextInstances(&t, bm, syms, newMQDecoder(r.rest()), tc); err != nil {
		return nil, nil, err
	}
	return bm, &t.info, nil
}

// symCodeLen returns SBSYMCODELEN for n symbols (7.4.4.1.1): ceil(log2(n)),
// which is 0 for a single symbol — every ID then costs no bits. T.88's
// Huffman erratum raises this to at least 1, but for arithmetic coding
// real encoders (jbig2enc) and the reference decoder use the plain
// logarithm, and a one-symbol stream only decodes with 0.
func symCodeLen(n int) uint {
	l := uint(0)
	for 1<<l < n {
		l++
	}
	return l
}

// decodeTextInstances runs the text region decoding procedure (6.4.5)
// with arithmetic integer decoding, placing symbols onto bm. Symbol IDs
// are tc.codeLen bits and index syms.
func (d *decoder) decodeTextInstances(t *textRegion, bm *bitmap, syms []*bitmap, mq *mqDecoder, tc *textCtx) error {
	strips := 1 << t.logStrips
	v, ok := mq.decodeInt(&tc.iadt)
	if !ok {
		return errors.New("OOB initial STRIPT")
	}
	stript := -int(v) * strips
	firsts := 0
	inst := 0
	for inst < int(t.numInst) {
		dt, ok := mq.decodeInt(&tc.iadt)
		if !ok {
			return errors.New("OOB strip delta T")
		}
		stript += int(dt) * strips
		dfs, ok := mq.decodeInt(&tc.iafs)
		if !ok {
			return errors.New("OOB first symbol S")
		}
		firsts += int(dfs)
		curs := firsts
		first := true
		for {
			if !first {
				ids, ok := mq.decodeInt(&tc.iads)
				if !ok {
					break // OOB: end of strip
				}
				curs += int(ids) + t.dsOffset
			}
			first = false
			if inst >= int(t.numInst) {
				return errors.New("text region decodes more instances than declared")
			}
			curt := 0
			if strips > 1 {
				ct, ok := mq.decodeInt(&tc.iait)
				if !ok {
					return errors.New("OOB symbol T")
				}
				curt = int(ct)
			}
			tt := stript + curt
			id := mq.decodeIAID(tc.iaid, tc.codeLen)
			if int(id) >= len(syms) {
				return fmt.Errorf("symbol ID %d out of range (%d symbols)", id, len(syms))
			}
			sym := syms[id]
			if t.refine {
				ri, ok := mq.decodeInt(&tc.iari)
				if !ok {
					return errors.New("OOB refinement flag")
				}
				if ri != 0 {
					var err error
					if sym, err = d.refineInstance(t, sym, mq, tc); err != nil {
						return err
					}
				}
			}
			// 6.4.5 step 3 c x: in non-transposed mode S runs along x and
			// the right-corner cases advance CURS before drawing, which
			// places the symbol's left edge at the original CURS either
			// way; likewise for S along y when transposed.
			var x, y int
			if !t.transposed {
				x = curs
				y = tt
				if t.refCorner == refBottomLeft || t.refCorner == refBottomRight {
					y = tt - sym.h + 1
				}
				curs += sym.w - 1
			} else {
				y = curs
				x = tt
				if t.refCorner == refTopRight || t.refCorner == refBottomRight {
					x = tt - sym.w + 1
				}
				curs += sym.h - 1
			}
			// Every instance costs at least one unit, so instances of
			// empty symbols — which touch no pixels — still run out the
			// budget rather than looping up to maxInstances per region
			// or, in a dictionary, per aggregate symbol.
			d.work -= max(1, bm.compose(sym, x, y, t.combOp))
			if d.work < 0 {
				return errBudget
			}
			inst++
		}
	}
	return nil
}

// REFCORNER values (7.4.4.1.1).
const (
	refBottomLeft  = 0
	refTopLeft     = 1
	refBottomRight = 2
	refTopRight    = 3
)

// refineInstance decodes the refined bitmap of a text region instance
// whose RI is set (6.4.11): its size and offset relative to the symbol,
// then the symbol refined to that size.
func (d *decoder) refineInstance(t *textRegion, ibo *bitmap, mq *mqDecoder, tc *textCtx) (*bitmap, error) {
	var rd [4]int32 // RDW, RDH, RDX, RDY
	for i, cx := range []*intCtx{&tc.iardw, &tc.iardh, &tc.iardx, &tc.iardy} {
		v, ok := mq.decodeInt(cx)
		if !ok {
			return nil, errors.New("OOB refinement delta")
		}
		rd[i] = v
	}
	w, h := int64(ibo.w)+int64(rd[0]), int64(ibo.h)+int64(rd[1])
	if w < 0 || h < 0 || w > MaxPixels || h > MaxPixels {
		return nil, fmt.Errorf("invalid refined symbol size %dx%d", w, h)
	}
	if err := d.charge(int(w), int(h)); err != nil {
		return nil, err
	}
	p := refineParams{
		w: int(w), h: int(h),
		template: t.rTemplate, at: t.rAT,
		ref: ibo,
		dx:  int(rd[0]>>1) + int(rd[2]), // floor(RDW/2) + RDX
		dy:  int(rd[1]>>1) + int(rd[3]),
	}
	return decodeRefinement(&p, mq, tc.gr)
}
