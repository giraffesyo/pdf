package jpx

import "github.com/giraffesyo/pdf/internal/codec/mq"

// Tier-1 decoding: the embedded block coding of T.800 Annex D, which codes
// each code-block's coefficients bit-plane by bit-plane in three passes
// (significance propagation, magnitude refinement, cleanup) over stripes
// four rows high.

// Context numbers: 0–8 zero coding, 9–13 sign coding, 14–16 magnitude
// refinement, 17 run length, 18 uniform.
const (
	ctxRL      = 17
	ctxUniform = 18
	numCtx     = 19
)

// Per-coefficient state flags.
const (
	fSig     = 1 << iota // significant
	fNeg                 // negative, once significant
	fVisited             // coded in this bit-plane's significance pass
	fRefined             // refined at least once
)

// Pass types, in the order they cycle.
const (
	passSig = iota
	passRef
	passClean
)

// t1 decodes code-blocks, reusing its buffers from block to block.
type t1 struct {
	w, h   int
	flags  []uint8 // (w+2)×(h+2), a border of never-significant cells
	mag    []int32 // w×h magnitudes
	low    []uint8 // w×h lowest bit-plane decoded
	cx     [numCtx]mq.Context
	mq     mq.Decoder
	raw    rawDecoder
	orient int
	causal bool
}

// rawDecoder reads the uncoded bits of the arithmetic-coding bypass
// (T.800 D.6): most significant first, with a stuffed bit after 0xFF.
type rawDecoder struct {
	data []byte
	pos  int
	c    uint32
	ct   int
}

func (r *rawDecoder) reset(data []byte) { *r = rawDecoder{data: data} }

func (r *rawDecoder) bit() uint32 {
	if r.ct == 0 {
		next := uint32(0xFF)
		if r.pos < len(r.data) {
			next = uint32(r.data[r.pos])
		}
		if r.c == 0xFF {
			if next > 0x8F {
				r.c, r.ct = 0xFF, 8
			} else {
				r.c, r.ct = next, 7
				r.pos++
			}
		} else {
			r.c, r.ct = next, 8
			r.pos++
		}
	}
	r.ct--
	return r.c >> r.ct & 1
}

func (d *t1) resetContexts() {
	for i := range d.cx {
		d.cx[i] = mq.Context{}
	}
	d.cx[0] = mq.Context{State: 4}
	d.cx[ctxRL] = mq.Context{State: 3}
	d.cx[ctxUniform] = mq.Context{State: 46}
}

// decodeBlock decodes cb's coding passes and stores its coefficients,
// dequantized, into the band.
func (d *t1) decodeBlock(cb *codeBlock, b *band, style, roi int, reversible bool) {
	w, h := cb.x1-cb.x0, cb.y1-cb.y0
	if w <= 0 || h <= 0 || cb.passes == 0 {
		return
	}
	top := b.mb - 1 - cb.zeroBP // the first coded bit-plane
	if top < 0 || top > 30 {
		return // corrupt, or beyond what 32-bit magnitudes hold
	}
	d.w, d.h, d.orient, d.causal = w, h, b.orient, style&styleCausal != 0
	n := (w + 2) * (h + 2)
	d.flags = grow(d.flags, n)
	d.mag = grow(d.mag, w*h)
	d.low = grow(d.low, w*h)
	for i := range d.low {
		d.low[i] = uint8((top + 1) & 0x1F)
	}
	d.resetContexts()

	segIdx, inSeg := 0, 0
	d.startSegment(cb, segIdx)
	p, pass := top, passClean
	for i := range cb.passes {
		if inSeg == cb.segs[segIdx].passes && segIdx+1 < len(cb.segs) {
			segIdx++
			inSeg = 0
			d.startSegment(cb, segIdx)
		}
		raw := style&styleBypass != 0 && i >= 10 && pass != passClean
		switch pass {
		case passSig:
			d.sigPass(p, raw)
		case passRef:
			d.refPass(p, raw)
		case passClean:
			d.cleanPass(p, style&styleSegSymbol != 0)
		}
		if style&styleReset != 0 {
			d.resetContexts()
		}
		inSeg++
		pass++
		if pass > passClean {
			pass = passSig
			p--
			if p < 0 {
				break
			}
		}
	}

	// Reconstruct (T.800 E.1.1.2): the decoded magnitude, plus half the
	// uncertainty left below the last bit-plane decoded when lossy, less
	// the region-of-interest scaling, times the step size.
	for y := range h {
		out := b.coeffs[(cb.y0-b.y0+y)*(b.x1-b.x0)+cb.x0-b.x0:]
		for x := range w {
			m := d.mag[y*w+x]
			if m == 0 {
				continue
			}
			v := float32(m)
			if !reversible {
				if low := d.low[y*w+x]; low > 0 {
					v += float32(int32(1)<<(low-1)) - 0.5
				}
				v += 0.5
			}
			if roi > 0 && m >= 1<<roi {
				v /= float32(int32(1) << roi)
			}
			v *= b.delta
			if d.flags[(y+1)*(w+2)+x+1]&fNeg != 0 {
				v = -v
			}
			out[x] = v
		}
	}
}

func grow[T uint8 | int32](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	s = s[:n]
	clear(s)
	return s
}

func (d *t1) startSegment(cb *codeBlock, i int) {
	if i >= len(cb.segs) {
		d.mq.Reset(nil)
		d.raw.reset(nil)
		return
	}
	d.mq.Reset(cb.segs[i].data)
	d.raw.reset(cb.segs[i].data)
}

// neighbours reports the significant horizontal, vertical and diagonal
// neighbours of the coefficient at flag index i, row y of its stripe.
func (d *t1) neighbours(i, y int) (h, v, dg int) {
	s := d.w + 2
	f := d.flags
	south := !d.causal || y%4 != 3
	h = int(f[i-1]&fSig) + int(f[i+1]&fSig)
	v = int(f[i-s] & fSig)
	dg = int(f[i-s-1]&fSig) + int(f[i-s+1]&fSig)
	if south {
		v += int(f[i+s] & fSig)
		dg += int(f[i+s-1]&fSig) + int(f[i+s+1]&fSig)
	}
	return h, v, dg
}

// zeroContext is T.800 Table D.1.
func (d *t1) zeroContext(h, v, dg int) int {
	switch d.orient {
	case bandHL:
		h, v = v, h
	case bandHH:
		hv := h + v
		switch {
		case dg >= 3:
			return 8
		case dg == 2:
			if hv >= 1 {
				return 7
			}
			return 6
		case dg == 1:
			switch {
			case hv >= 2:
				return 5
			case hv == 1:
				return 4
			}
			return 3
		default:
			switch {
			case hv >= 2:
				return 2
			case hv == 1:
				return 1
			}
			return 0
		}
	}
	switch {
	case h == 2:
		return 8
	case h == 1:
		switch {
		case v >= 1:
			return 7
		case dg >= 1:
			return 6
		}
		return 5
	case v == 2:
		return 4
	case v == 1:
		return 3
	case dg >= 2:
		return 2
	case dg == 1:
		return 1
	}
	return 0
}

// signContribution is a neighbour's term in T.800 Table D.2: +1
// significant positive, −1 significant negative, 0 insignificant.
func signContribution(f uint8) int {
	if f&fSig == 0 {
		return 0
	}
	if f&fNeg != 0 {
		return -1
	}
	return 1
}

// decodeSign decodes a newly significant coefficient's sign (T.800 D.3.2)
// and marks it significant.
func (d *t1) decodeSign(i, y int, raw bool) {
	var neg uint32
	if raw {
		neg = d.raw.bit()
	} else {
		s := d.w + 2
		f := d.flags
		hc := max(-1, min(1, signContribution(f[i-1])+signContribution(f[i+1])))
		vn := signContribution(f[i-s])
		if !d.causal || y%4 != 3 {
			vn += signContribution(f[i+s])
		}
		vc := max(-1, min(1, vn))
		ctx, xor := signContext(hc, vc)
		neg = d.mq.Decode(&d.cx[ctx]) ^ xor
	}
	d.flags[i] |= fSig
	if neg != 0 {
		d.flags[i] |= fNeg
	}
}

// signContext is T.800 Table D.3.
func signContext(h, v int) (int, uint32) {
	switch {
	case h == 1:
		return 12 + v, 0
	case h == -1:
		return 12 - v, 1
	case v == 1:
		return 10, 0
	case v == -1:
		return 10, 1
	}
	return 9, 0
}

func (d *t1) sigPass(p int, raw bool) {
	w, s := d.w, d.w+2
	for y0 := 0; y0 < d.h; y0 += 4 {
		for x := range w {
			for y := y0; y < min(y0+4, d.h); y++ {
				i := (y+1)*s + x + 1
				if d.flags[i]&fSig != 0 {
					continue
				}
				h, v, dg := d.neighbours(i, y)
				if h+v+dg == 0 {
					continue
				}
				var bit uint32
				if raw {
					bit = d.raw.bit()
				} else {
					bit = d.mq.Decode(&d.cx[d.zeroContext(h, v, dg)])
				}
				d.low[y*w+x] = uint8(p & 0x1F)
				d.flags[i] |= fVisited
				if bit != 0 {
					d.decodeSign(i, y, raw)
					d.mag[y*w+x] |= 1 << p
				}
			}
		}
	}
}

func (d *t1) refPass(p int, raw bool) {
	w, s := d.w, d.w+2
	for y0 := 0; y0 < d.h; y0 += 4 {
		for x := range w {
			for y := y0; y < min(y0+4, d.h); y++ {
				i := (y+1)*s + x + 1
				f := d.flags[i]
				if f&fSig == 0 || f&fVisited != 0 {
					continue
				}
				var bit uint32
				if raw {
					bit = d.raw.bit()
				} else {
					ctx := 16
					if f&fRefined == 0 {
						ctx = 14
						if h, v, dg := d.neighbours(i, y); h+v+dg > 0 {
							ctx = 15
						}
					}
					bit = d.mq.Decode(&d.cx[ctx])
				}
				if bit != 0 {
					d.mag[y*w+x] |= 1 << p
				}
				d.low[y*w+x] = uint8(p & 0x1F)
				d.flags[i] |= fRefined
			}
		}
	}
}

func (d *t1) cleanPass(p int, segSymbol bool) {
	w, s := d.w, d.w+2
	for y0 := 0; y0 < d.h; y0 += 4 {
		for x := range w {
			y := y0
			if y0+4 <= d.h && d.runnable(x, y0) {
				// Run-length mode: one decision for the whole column.
				if d.mq.Decode(&d.cx[ctxRL]) == 0 {
					for k := range 4 {
						d.low[(y0+k)*w+x] = uint8(p & 0x1F)
					}
					continue
				}
				r := int(d.mq.Decode(&d.cx[ctxUniform])<<1 | d.mq.Decode(&d.cx[ctxUniform]))
				for k := range r {
					d.low[(y0+k)*w+x] = uint8(p & 0x1F)
				}
				y = y0 + r
				i := (y+1)*s + x + 1
				d.decodeSign(i, y, false)
				d.mag[y*w+x] |= 1 << p
				d.low[y*w+x] = uint8(p & 0x1F)
				y++
			}
			for ; y < min(y0+4, d.h); y++ {
				i := (y+1)*s + x + 1
				if d.flags[i]&(fSig|fVisited) != 0 {
					continue
				}
				h, v, dg := d.neighbours(i, y)
				d.low[y*w+x] = uint8(p & 0x1F)
				if d.mq.Decode(&d.cx[d.zeroContext(h, v, dg)]) != 0 {
					d.decodeSign(i, y, false)
					d.mag[y*w+x] |= 1 << p
				}
			}
		}
	}
	for i := range d.flags {
		d.flags[i] &^= fVisited
	}
	if segSymbol {
		for range 4 {
			d.mq.Decode(&d.cx[ctxUniform])
		}
	}
}

// runnable reports whether the stripe column at x, rows y0..y0+3, is all
// insignificant, unvisited and without significant neighbours: the
// condition for run-length coding.
func (d *t1) runnable(x, y0 int) bool {
	s := d.w + 2
	for k := range 4 {
		i := (y0+k+1)*s + x + 1
		if d.flags[i]&(fSig|fVisited) != 0 {
			return false
		}
		if h, v, dg := d.neighbours(i, y0+k); h+v+dg != 0 {
			return false
		}
	}
	return true
}
