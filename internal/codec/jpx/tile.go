package jpx

import (
	"math"
	"slices"
)

// Subband orientations.
const (
	bandLL = iota
	bandHL
	bandLH
	bandHH
)

// tile is one tile's components, decoded.
type tile struct {
	x0, y0, x1, y1 int // on the reference grid
	comps          []*tileComp
}

type tileComp struct {
	x0, y0, x1, y1 int // tile-component bounds (T.800 B-12)
	coding         *coding
	quant          *quantization
	roi            int
	prec           int
	signed         bool
	dx, dy         int
	res            []*resolution
	data           []float32 // reconstructed samples, row-major over the bounds
}

type resolution struct {
	x0, y0, x1, y1 int
	r              int // resolution number, 0 the lowest
	ppx, ppy       int // precinct size exponents
	pw, ph         int // precincts across and down
	bands          []*band
}

type band struct {
	orient         int
	x0, y0, x1, y1 int
	nb             int     // decomposition level the band belongs to
	mb             int     // magnitude bit-planes (T.800 E-2)
	delta          float32 // quantization step; 1 when reversible
	cbw, cbh       int     // code-block size exponents in this band
	precincts      []precinct
	coeffs         []float32
}

type precinct struct {
	cw, ch int // code-blocks across and down
	blocks []codeBlock
	incl   *tagTree
	zbp    *tagTree
}

type codeBlock struct {
	x0, y0, x1, y1 int // in band coordinates
	included       bool
	lblock         int
	zeroBP         int
	passes         int
	segs           []dataSegment
}

// dataSegment is a run of coding passes terminated together, and the
// bytes that code them.
type dataSegment struct {
	data   []byte
	passes int
}

// newTile lays out tile t's components, resolutions, subbands, precincts
// and code-blocks.
func (cs *codestream) newTile(t *tileData) *tile {
	s := &cs.siz
	p, q := t.index%s.tilesAcross(), t.index/s.tilesAcross()
	tl := &tile{
		x0: max(s.tx0+p*s.tw, s.x0), y0: max(s.ty0+q*s.th, s.y0),
		x1: min(s.tx0+(p+1)*s.tw, s.x1), y1: min(s.ty0+(q+1)*s.th, s.y1),
	}
	for c, comp := range s.comps {
		tc := &tileComp{
			x0: ceilDiv(tl.x0, comp.dx), y0: ceilDiv(tl.y0, comp.dy),
			x1: ceilDiv(tl.x1, comp.dx), y1: ceilDiv(tl.y1, comp.dy),
			coding: cs.codingFor(t, c), quant: cs.quantizationFor(t, c), roi: cs.roiShift(t, c),
			prec: comp.precision, signed: comp.signed, dx: comp.dx, dy: comp.dy,
		}
		tc.layout()
		tl.comps = append(tl.comps, tc)
	}
	return tl
}

func (tc *tileComp) layout() {
	cod := tc.coding
	nl := cod.levels
	for r := 0; r <= nl; r++ {
		scale := 1 << (nl - r)
		res := &resolution{
			x0: ceilDiv(tc.x0, scale), y0: ceilDiv(tc.y0, scale),
			x1: ceilDiv(tc.x1, scale), y1: ceilDiv(tc.y1, scale),
			r: r, ppx: cod.ppx[r], ppy: cod.ppy[r],
		}
		if res.x1 > res.x0 {
			res.pw = ceilDiv(res.x1, 1<<res.ppx) - floorDiv(res.x0, 1<<res.ppx)
		}
		if res.y1 > res.y0 {
			res.ph = ceilDiv(res.y1, 1<<res.ppy) - floorDiv(res.y0, 1<<res.ppy)
		}
		orients := []int{bandLL}
		if r > 0 {
			orients = []int{bandHL, bandLH, bandHH}
		}
		for _, o := range orients {
			res.bands = append(res.bands, tc.newBand(res, o))
		}
		tc.res = append(tc.res, res)
	}
}

// bandIndex numbers subbands as quantization lists them: LL, then HL, LH
// and HH from the lowest resolution up.
func bandIndex(r, orient int) int {
	if r == 0 {
		return 0
	}
	return 3*(r-1) + orient
}

func (tc *tileComp) newBand(res *resolution, orient int) *band {
	nl := tc.coding.levels
	b := &band{orient: orient, nb: nl}
	if res.r > 0 {
		b.nb = nl - res.r + 1
	}
	// T.800 B-15: the band's bounds on its own grid.
	xo, yo := 0, 0
	if orient == bandHL || orient == bandHH {
		xo = 1
	}
	if orient == bandLH || orient == bandHH {
		yo = 1
	}
	if res.r == 0 {
		b.x0, b.y0, b.x1, b.y1 = res.x0, res.y0, res.x1, res.y1
	} else {
		half := 1 << (b.nb - 1)
		scale := 1 << b.nb
		b.x0, b.x1 = ceilDiv(tc.x0-half*xo, scale), ceilDiv(tc.x1-half*xo, scale)
		b.y0, b.y1 = ceilDiv(tc.y0-half*yo, scale), ceilDiv(tc.y1-half*yo, scale)
	}

	// Quantization (T.800 E.1).
	q := tc.quant
	i := bandIndex(res.r, orient)
	var eps, mu int
	switch {
	case q.style == 1: // derived from the LL values
		eps, mu = q.exp[0]-nl+b.nb, q.mant[0]
		if res.r == 0 {
			eps = q.exp[0]
		}
	case i < len(q.exp):
		eps, mu = q.exp[i], q.mant[i]
	default:
		eps, mu = q.exp[len(q.exp)-1], q.mant[len(q.mant)-1]
	}
	b.mb = q.guard + eps - 1 + tc.roi
	b.delta = 1
	if !tc.coding.reversible {
		gain := [4]int{0, 1, 1, 2}[orient]
		b.delta = float32(math.Ldexp(1+float64(mu)/2048, tc.prec+gain-eps))
	}

	// Code-blocks, by precinct (T.800 B.6, B.7).
	shift := 0
	if res.r > 0 {
		shift = 1
	}
	ppx, ppy := max(res.ppx-shift, 0), max(res.ppy-shift, 0)
	b.cbw, b.cbh = min(tc.coding.cbw, ppx), min(tc.coding.cbh, ppy)
	px0, py0 := floorDiv(res.x0, 1<<res.ppx), floorDiv(res.y0, 1<<res.ppy)
	b.precincts = make([]precinct, res.pw*res.ph)
	if b.x1 <= b.x0 || b.y1 <= b.y0 {
		return b
	}
	for py := range res.ph {
		for px := range res.pw {
			// The precinct's area on the band's grid.
			ax0, ay0 := (px0+px)<<ppx, (py0+py)<<ppy
			ax1, ay1 := ax0+1<<ppx, ay0+1<<ppy
			ax0, ay0, ax1, ay1 = max(ax0, b.x0), max(ay0, b.y0), min(ax1, b.x1), min(ay1, b.y1)
			if ax1 <= ax0 || ay1 <= ay0 {
				continue
			}
			pr := &b.precincts[py*res.pw+px]
			cx0, cy0 := floorDiv(ax0, 1<<b.cbw), floorDiv(ay0, 1<<b.cbh)
			cx1, cy1 := ceilDiv(ax1, 1<<b.cbw), ceilDiv(ay1, 1<<b.cbh)
			pr.cw, pr.ch = cx1-cx0, cy1-cy0
			pr.blocks = make([]codeBlock, pr.cw*pr.ch)
			for j := range pr.ch {
				for k := range pr.cw {
					cb := &pr.blocks[j*pr.cw+k]
					cb.x0, cb.y0 = max((cx0+k)<<b.cbw, ax0), max((cy0+j)<<b.cbh, ay0)
					cb.x1, cb.y1 = min((cx0+k+1)<<b.cbw, ax1), min((cy0+j+1)<<b.cbh, ay1)
					cb.lblock = 3
				}
			}
			pr.incl, pr.zbp = newTagTree(pr.cw, pr.ch), newTagTree(pr.cw, pr.ch)
		}
	}
	return b
}

// packetKey identifies a precinct's packets.
type packetKey struct{ c, r, p int }

// decodePackets reads the tile's packets (T.800 B.9, B.10), filling the
// code-blocks with their coding passes' data.
func (cs *codestream) decodePackets(t *tileData, tl *tile) {
	layers, _, sop, eph, vols := cs.tileProgression(t)
	var body []byte
	for _, part := range t.parts {
		body = append(body, part...)
	}
	var headers []byte
	switch {
	case len(t.ppt) > 0:
		for _, h := range t.ppt {
			headers = append(headers, h...)
		}
	case len(t.ppm) > 0:
		for _, h := range t.ppm {
			headers = append(headers, h...)
		}
	}
	packed := headers != nil
	bodyPos := 0
	hdr := &bitReader{data: body}
	if packed {
		hdr = &bitReader{data: headers}
	}
	done := map[packetKey]int{}
	exhausted := false
	visit := func(l, r, c, p int) bool {
		key := packetKey{c, r, p}
		if done[key] != l {
			return true // sent in an earlier progression volume
		}
		done[key] = l + 1
		if !packed {
			hdr.pos = bodyPos
		}
		if sop && hdr.pos+6 <= len(hdr.data) && hdr.data[hdr.pos] == 0xFF && hdr.data[hdr.pos+1] == 0x91 && !packed {
			hdr.pos += 6
		}
		if packed && sop && bodyPos+6 <= len(body) && body[bodyPos] == 0xFF && body[bodyPos+1] == 0x91 {
			bodyPos += 6
		}
		if hdr.pos >= len(hdr.data) {
			exhausted = true
			return false
		}
		hdr.reset()
		lengths := tl.comps[c].packetHeader(hdr, tl.comps[c].res[r], p, l)
		hdr.align()
		if eph && hdr.pos+2 <= len(hdr.data) && hdr.data[hdr.pos] == 0xFF && hdr.data[hdr.pos+1] == 0x92 {
			hdr.pos += 2
		}
		if !packed {
			bodyPos = hdr.pos
		}
		for _, ln := range lengths {
			end := min(bodyPos+ln.n, len(body))
			seg := &ln.cb.segs[ln.seg]
			seg.data = append(seg.data, body[bodyPos:end]...)
			bodyPos = end
		}
		return true
	}
	for _, v := range vols {
		if exhausted {
			break
		}
		cs.progress(tl, v, min(v.layerEnd, layers), visit)
	}
}

// progress iterates one progression volume's packets in its order.
func (cs *codestream) progress(tl *tile, v progression, layers int, visit func(l, r, c, p int) bool) {
	ncomp := len(tl.comps)
	cEnd := min(v.compEnd, ncomp)
	maxRes := 0
	for _, tc := range tl.comps {
		maxRes = max(maxRes, tc.coding.levels+1)
	}
	rEnd := min(v.resEnd, maxRes)
	precincts := func(c, r int) int {
		tc := tl.comps[c]
		if r >= len(tc.res) {
			return 0
		}
		return tc.res[r].pw * tc.res[r].ph
	}
	switch v.order {
	case orderLRCP:
		for l := range layers {
			for r := v.resStart; r < rEnd; r++ {
				for c := v.compStart; c < cEnd; c++ {
					for p := range precincts(c, r) {
						if !visit(l, r, c, p) {
							return
						}
					}
				}
			}
		}
	case orderRLCP:
		for r := v.resStart; r < rEnd; r++ {
			for l := range layers {
				for c := v.compStart; c < cEnd; c++ {
					for p := range precincts(c, r) {
						if !visit(l, r, c, p) {
							return
						}
					}
				}
			}
		}
	default:
		// Position-driven orders visit precincts by where they start on
		// the reference grid (T.800 B.12.1.3–5).
		type pos struct{ y, x, c, r, p int }
		var all []pos
		for c := v.compStart; c < cEnd; c++ {
			tc := tl.comps[c]
			for r := v.resStart; r < min(rEnd, len(tc.res)); r++ {
				res := tc.res[r]
				scale := 1 << (tc.coding.levels - r)
				px0, py0 := floorDiv(res.x0, 1<<res.ppx), floorDiv(res.y0, 1<<res.ppy)
				for p := range res.pw * res.ph {
					x := (px0 + p%res.pw) << res.ppx
					y := (py0 + p/res.pw) << res.ppy
					gx, gy := x*tc.dx*scale, y*tc.dy*scale
					if x < res.x0 {
						gx = tl.x0
					}
					if y < res.y0 {
						gy = tl.y0
					}
					all = append(all, pos{gy, gx, c, r, p})
				}
			}
		}
		slices.SortStableFunc(all, func(a, b pos) int {
			var ka, kb [4]int
			switch v.order {
			case orderRPCL:
				ka, kb = [4]int{a.r, a.y, a.x, a.c}, [4]int{b.r, b.y, b.x, b.c}
			case orderPCRL:
				ka, kb = [4]int{a.y, a.x, a.c, a.r}, [4]int{b.y, b.x, b.c, b.r}
			default: // CPRL
				ka, kb = [4]int{a.c, a.y, a.x, a.r}, [4]int{b.c, b.y, b.x, b.r}
			}
			for i := range ka {
				if ka[i] != kb[i] {
					if ka[i] < kb[i] {
						return -1
					}
					return 1
				}
			}
			return 0
		})
		for _, q := range all {
			for l := range layers {
				if !visit(l, q.r, q.c, q.p) {
					return
				}
			}
		}
	}
}

// segmentLength is one codeword segment's length from a packet header,
// for the segment its bytes extend.
type segmentLength struct {
	cb  *codeBlock
	seg int
	n   int
}

// packetHeader decodes one packet header (T.800 B.10) for precinct p of
// res at layer l, recording the new coding passes and returning the
// lengths of the data that follows.
func (tc *tileComp) packetHeader(h *bitReader, res *resolution, p, l int) []segmentLength {
	if h.bit() == 0 {
		return nil // an empty packet
	}
	var lengths []segmentLength
	style := tc.coding.cbStyle
	for _, b := range res.bands {
		if p >= len(b.precincts) {
			continue
		}
		pr := &b.precincts[p]
		for i := range pr.blocks {
			cb := &pr.blocks[i]
			var included bool
			if !cb.included {
				included = pr.incl.decode(h, i, l+1)
			} else {
				included = h.bit() == 1
			}
			if !included {
				continue
			}
			if !cb.included {
				zbp := 0
				for !pr.zbp.decode(h, i, zbp+1) && zbp < 255 {
					zbp++
				}
				cb.zeroBP = zbp
				cb.included = true
			}
			n := decodePassCount(h)
			for h.bit() == 1 && cb.lblock < 32 {
				cb.lblock++
			}
			for n > 0 {
				if len(cb.segs) == 0 || cb.segs[len(cb.segs)-1].passes >= maxSegmentPasses(len(cb.segs)-1, style) {
					cb.segs = append(cb.segs, dataSegment{})
				}
				si := len(cb.segs) - 1
				seg := &cb.segs[si]
				take := min(n, maxSegmentPasses(si, style)-seg.passes)
				bits := cb.lblock + floorLog2(take)
				length := h.bits(bits)
				seg.passes += take
				cb.passes += take
				n -= take
				lengths = append(lengths, segmentLength{cb: cb, seg: si, n: length})
			}
		}
	}
	return lengths
}

// maxSegmentPasses is how many coding passes segment i may hold before
// the coder terminated it (T.800 D.4, Table D.9).
func maxSegmentPasses(i int, style int) int {
	switch {
	case style&styleTermAll != 0:
		return 1
	case style&styleBypass != 0:
		if i == 0 {
			return 10
		}
		if i%2 == 1 {
			return 2 // significance and refinement, raw
		}
		return 1 // cleanup, arithmetic
	}
	return 1 << 20
}

func decodePassCount(h *bitReader) int {
	if h.bit() == 0 {
		return 1
	}
	if h.bit() == 0 {
		return 2
	}
	if n := h.bits(2); n != 3 {
		return 3 + n
	}
	if n := h.bits(5); n != 31 {
		return 6 + n
	}
	return 37 + h.bits(7)
}

func floorLog2(n int) int {
	l := 0
	for n > 1 {
		n >>= 1
		l++
	}
	return l
}

// bitReader reads packet headers: bits most significant first, with a
// zero bit stuffed after every 0xFF byte (T.800 B.10.1).
type bitReader struct {
	data []byte
	pos  int
	buf  uint32
	ct   int
}

func (b *bitReader) reset() { b.buf, b.ct = 0, 0 }

func (b *bitReader) byteIn() {
	b.buf = b.buf << 8 & 0xFFFF
	b.ct = 8
	if b.buf == 0xFF00 {
		b.ct = 7
	}
	if b.pos < len(b.data) {
		b.buf |= uint32(b.data[b.pos])
		b.pos++
	}
}

func (b *bitReader) bit() int {
	if b.ct == 0 {
		b.byteIn()
	}
	b.ct--
	return int(b.buf >> b.ct & 1)
}

func (b *bitReader) bits(n int) int {
	v := 0
	for range min(n, 32) {
		v = v<<1 | b.bit()
	}
	return v
}

// align ends a packet header: the rest of the byte is padding, and a
// byte after 0xFF carries the stuffed bit.
func (b *bitReader) align() {
	if b.buf&0xFF == 0xFF {
		b.byteIn()
	}
	b.ct = 0
}

// tagTree is a tag tree (T.800 B.10.2) over a w×h array of leaves.
type tagTree struct {
	levels [][]tagNode
	widths []int
}

type tagNode struct {
	value, low int
}

func newTagTree(w, h int) *tagTree {
	t := &tagTree{}
	for {
		nodes := make([]tagNode, w*h)
		for i := range nodes {
			nodes[i].value = math.MaxInt32
		}
		t.levels = append(t.levels, nodes)
		t.widths = append(t.widths, w)
		if w <= 1 && h <= 1 {
			break
		}
		w, h = (w+1)/2, (h+1)/2
	}
	return t
}

// decode reads until leaf i's value is known to be below threshold, or
// not; it reports whether it is.
func (t *tagTree) decode(b *bitReader, i, threshold int) bool {
	var path [32]*tagNode
	x, y := i%t.widths[0], i/t.widths[0]
	n := 0
	for lv := range t.levels {
		path[n] = &t.levels[lv][y*t.widths[lv]+x]
		n++
		x, y = x/2, y/2
	}
	low := 0
	var node *tagNode
	for k := n - 1; k >= 0; k-- {
		node = path[k]
		if low > node.low {
			node.low = low
		} else {
			low = node.low
		}
		for low < threshold && low < node.value {
			if b.bit() == 1 {
				node.value = low
			} else {
				low++
			}
		}
		node.low = low
	}
	return node.value < threshold
}
