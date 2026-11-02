package jbig2

import (
	"errors"
	"fmt"
)

// Generic refinement region decoding procedure (T.88 §6.3) with
// arithmetic coding. A refinement codes a bitmap against a reference
// bitmap it closely resembles: each pixel's context mixes already decoded
// pixels of the new bitmap with the reference's pixels around the same
// spot. Symbol dictionaries refine and aggregate symbols with it, text
// regions refine individual instances, and refinement region segments
// refine part of the page or an intermediate region.

// refineParams are the inputs of 6.3.2 relevant to arithmetic decoding.
type refineParams struct {
	w, h     int
	template uint8 // GRTEMPLATE: 0 (13-pixel context) or 1 (10-pixel)
	ref      *bitmap
	dx, dy   int       // GRREFERENCEDX, GRREFERENCEDY
	at       [2][2]int // GRAT1 (new bitmap) and GRAT2 (reference); template 0 only
	tpgron   bool
}

// refineContexts is the size of a refinement context set: template 0
// forms 13-bit contexts, template 1 10-bit ones, and both share one size
// so a set can be reused whichever template the next user asks for.
const refineContexts = 1 << 13

// refineSLTP is the context of the typical-prediction bit SLTP per
// template (6.3.5.6, Figures 14 and 15), in the bit layout of
// refineContext. It is the same layout and value jbig2dec, the reference
// decoder, uses.
var refineSLTP = [2]uint32{0x100, 0x040}

// readRefineAT reads GRAT1 and GRAT2 (7.4.7.3, 7.4.3.1.3, 7.4.4.1.3).
func readRefineAT(r *reader) ([2][2]int, error) {
	var at [2][2]int
	for i := range at {
		for j := range at[i] {
			v, err := r.i8()
			if err != nil {
				return at, err
			}
			at[i][j] = v
		}
	}
	return at, nil
}

// refineContext forms CONTEXT for pixel (x, y) from the template pixels
// of Figures 12 and 13: three or four already decoded pixels of the new
// bitmap and six or nine reference pixels around (x-dx, y-dy).
func refineContext(p *refineParams, bm *bitmap, x, y int) uint32 {
	ref := p.ref
	rx, ry := x-p.dx, y-p.dy
	ctx := bm.get(x-1, y) | bm.get(x+1, y-1)<<1 | bm.get(x, y-1)<<2
	if p.template == 0 {
		return ctx | bm.get(x+p.at[0][0], y+p.at[0][1])<<3 |
			ref.get(rx+1, ry+1)<<4 | ref.get(rx, ry+1)<<5 | ref.get(rx-1, ry+1)<<6 |
			ref.get(rx+1, ry)<<7 | ref.get(rx, ry)<<8 | ref.get(rx-1, ry)<<9 |
			ref.get(rx+1, ry-1)<<10 | ref.get(rx, ry-1)<<11 |
			ref.get(rx+p.at[1][0], ry+p.at[1][1])<<12
	}
	return ctx | bm.get(x-1, y-1)<<3 |
		ref.get(rx+1, ry+1)<<4 | ref.get(rx, ry+1)<<5 |
		ref.get(rx+1, ry)<<6 | ref.get(rx, ry)<<7 | ref.get(rx-1, ry)<<8 |
		ref.get(rx, ry-1)<<9
}

// typicalPixel reports the value of pixel (x, y) under typical
// prediction (6.3.5.6): when the reference pixel it corresponds to and
// all eight of its neighbours agree, the pixel takes their value and is
// not coded.
func typicalPixel(p *refineParams, x, y int) (uint32, bool) {
	rx, ry := x-p.dx, y-p.dy
	v := p.ref.get(rx, ry)
	for j := -1; j <= 1; j++ {
		for i := -1; i <= 1; i++ {
			if p.ref.get(rx+i, ry+j) != v {
				return 0, false
			}
		}
	}
	return v, true
}

// decodeRefinement runs the arithmetic generic refinement region
// decoding procedure (6.3.5) into a fresh bitmap, using the caller's
// decoder and context set: symbol dictionaries and text regions share
// both across every bitmap they refine.
func decodeRefinement(p *refineParams, mq *mqDecoder, cx []mqCx) (*bitmap, error) {
	if p.template > 1 {
		return nil, fmt.Errorf("invalid GRTEMPLATE %d", p.template)
	}
	if len(cx) < refineContexts {
		return nil, errors.New("refinement context set too small")
	}
	if p.ref == nil {
		return nil, errors.New("refinement has no reference bitmap")
	}
	bm, err := newBitmap(p.w, p.h)
	if err != nil {
		return nil, err
	}
	if p.w == 0 || p.h == 0 {
		return bm, nil
	}
	ltp := uint32(0)
	for y := range p.h {
		if p.tpgron {
			ltp ^= mq.decode(&cx[refineSLTP[p.template]])
		}
		row := bm.row(y)
		for x := range p.w {
			bit, typical := uint32(0), false
			if ltp == 1 {
				bit, typical = typicalPixel(p, x, y)
			}
			if !typical {
				bit = mq.decode(&cx[refineContext(p, bm, x, y)])
			}
			if bit != 0 {
				row[x>>3] |= 0x80 >> uint(x&7)
			}
		}
	}
	return bm, nil
}

// decodeRefinementRegion parses a refinement region segment (7.4.7) and
// refines its reference: the intermediate region it refers to, or else
// the part of the page it covers.
func (d *decoder) decodeRefinementRegion(seg *segment) (*bitmap, *regionInfo, error) {
	r := &reader{data: seg.data}
	info, err := readRegionInfo(r)
	if err != nil {
		return nil, nil, err
	}
	if info.h < 0 {
		return nil, nil, errors.New("refinement region of unknown height")
	}
	flags, err := r.u8()
	if err != nil {
		return nil, nil, err
	}
	p := refineParams{w: info.w, h: info.h, template: flags & 1, tpgron: flags>>1&1 != 0}
	if p.template == 0 {
		if p.at, err = readRefineAT(r); err != nil {
			return nil, nil, err
		}
	}
	// 7.4.7.4: an intermediate region this segment refers to is the
	// reference, and is used up by it; otherwise the page under the
	// region is.
	for _, n := range seg.referred {
		if reg, ok := d.regions[n]; ok {
			p.ref = reg
			delete(d.regions, n)
			break
		}
	}
	if seg.typ != segRefinementIntermediate {
		// Rows below the page are never seen, and the new bitmap's
		// contexts only reach upward, so decoding stops at the page.
		p.h = d.visibleRows(&info)
		info.h = p.h
	}
	if err := d.charge(p.w, p.h); err != nil {
		return nil, nil, err
	}
	if p.ref == nil {
		if err := d.charge(p.w, p.h); err != nil {
			return nil, nil, err
		}
		if p.ref, err = newBitmap(p.w, p.h); err != nil {
			return nil, nil, err
		}
		p.ref.compose(d.page, -info.x, -info.y, opReplace)
	}
	bm, err := decodeRefinement(&p, newMQDecoder(r.rest()), make([]mqCx, refineContexts))
	if err != nil {
		return nil, nil, err
	}
	return bm, &info, nil
}
