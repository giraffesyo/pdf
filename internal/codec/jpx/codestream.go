package jpx

import (
	"errors"
	"fmt"
)

// Marker codes (T.800 Table A.2).
const (
	mSOC = 0xFF4F
	mCAP = 0xFF50 // Part 15 (HTJ2K) capabilities
	mSIZ = 0xFF51
	mCOD = 0xFF52
	mCOC = 0xFF53
	mTLM = 0xFF55
	mPLM = 0xFF57
	mPLT = 0xFF58
	mCPF = 0xFF59 // Part 15
	mQCD = 0xFF5C
	mQCC = 0xFF5D
	mRGN = 0xFF5E
	mPOC = 0xFF5F
	mPPM = 0xFF60
	mPPT = 0xFF61
	mCRG = 0xFF63
	mCOM = 0xFF64
	mSOT = 0xFF90
	mSOP = 0xFF91
	mEPH = 0xFF92
	mSOD = 0xFF93
	mEOC = 0xFFD9
)

// Bounds on what the decoder accepts. The standard allows more; images
// in PDFs never come near.
const (
	maxComponents = 16
	maxPrecision  = 16
	maxLevels     = 32
	maxTiles      = 65535
	maxLayers     = 65535
)

// Progression orders (T.800 Table A.16).
const (
	orderLRCP = iota
	orderRLCP
	orderRPCL
	orderPCRL
	orderCPRL
)

// Code-block style flags (T.800 Table A.19).
const (
	styleBypass    = 0x01
	styleReset     = 0x02
	styleTermAll   = 0x04
	styleCausal    = 0x08
	stylePredTerm  = 0x10
	styleSegSymbol = 0x20
)

// component is one image component as SIZ declares it.
type component struct {
	precision int
	signed    bool
	dx, dy    int // subsampling
}

// siz is the image and tiling geometry (T.800 A.5.1).
type siz struct {
	x1, y1   int // Xsiz, Ysiz: the reference grid's far corner
	x0, y0   int // XOsiz, YOsiz: the image area's origin
	tw, th   int // tile size
	tx0, ty0 int // tile grid origin
	comps    []component
}

func (s *siz) tilesAcross() int { return ceilDiv(s.x1-s.tx0, s.tw) }
func (s *siz) tilesDown() int   { return ceilDiv(s.y1-s.ty0, s.th) }

// coding is a component's coding style (COD, COC).
type coding struct {
	levels     int   // decomposition levels
	cbw, cbh   int   // code-block size exponents
	cbStyle    int   // style flags
	reversible bool  // 5/3 wavelet; otherwise 9/7
	ppx, ppy   []int // precinct size exponents per resolution
}

// quantization is a component's quantization (QCD, QCC).
type quantization struct {
	style int // 0 none, 1 scalar derived, 2 scalar expounded
	guard int
	exp   []int // per subband, in band order: LL, then HL, LH, HH per level
	mant  []int
}

// progression is one progression order change (POC), or the COD order
// as a single volume.
type progression struct {
	resStart, compStart int
	layerEnd            int
	resEnd, compEnd     int
	order               int
}

// header holds the markers that may appear in the main header and,
// overriding it, in a tile's first tile-part header.
type header struct {
	cod    *coding // COD's defaults for every component
	codSet bool
	sop    bool
	eph    bool
	order  int
	layers int
	mct    bool
	coc    map[int]*coding
	qcd    *quantization
	qcc    map[int]*quantization
	rgn    map[int]int // ROI shift per component
	poc    []progression
}

// codestream is the parsed main header and the tiles' data.
type codestream struct {
	siz     siz
	main    header
	ppm     [][]byte // PPM segments' data, in order
	ppmData []byte   // the PPM segments joined
	tiles   []*tileData
}

// tileData collects a tile's tile-parts.
type tileData struct {
	index int
	hdr   header
	parts [][]byte // packet data of each tile-part
	ppt   [][]byte // PPT segments by Zppt
	ppm   [][]byte // this tile's share of PPM headers, by tile-part
}

type reader struct {
	b   []byte
	pos int
	err error
}

func (r *reader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("jpx: "+format, args...)
	}
}

func (r *reader) u8() int {
	if r.pos+1 > len(r.b) {
		r.fail("truncated marker segment")
		return 0
	}
	v := r.b[r.pos]
	r.pos++
	return int(v)
}

func (r *reader) u16() int {
	if r.pos+2 > len(r.b) {
		r.fail("truncated marker segment")
		return 0
	}
	v := int(r.b[r.pos])<<8 | int(r.b[r.pos+1])
	r.pos += 2
	return v
}

func (r *reader) u32() int {
	if r.pos+4 > len(r.b) {
		r.fail("truncated marker segment")
		return 0
	}
	v := int(r.b[r.pos])<<24 | int(r.b[r.pos+1])<<16 | int(r.b[r.pos+2])<<8 | int(r.b[r.pos+3])
	r.pos += 4
	return v
}

func (r *reader) rest() []byte {
	if r.pos >= len(r.b) {
		return nil
	}
	return r.b[r.pos:]
}

// parseMainHeader reads SOC and SIZ and the main header up to the first
// SOT, returning the codestream and the offset of that SOT.
func parseMainHeader(data []byte) (*codestream, int, error) {
	if len(data) < 4 || int(data[0])<<8|int(data[1]) != mSOC || int(data[2])<<8|int(data[3]) != mSIZ {
		return nil, 0, errors.New("jpx: not a JPEG 2000 codestream")
	}
	cs := &codestream{}
	pos := 2
	for pos+2 <= len(data) {
		marker := int(data[pos])<<8 | int(data[pos+1])
		if marker == mSOT || marker == mEOC {
			break
		}
		seg, next, err := segment(data, pos)
		if err != nil {
			return nil, 0, err
		}
		switch marker {
		case mSIZ:
			if err := cs.siz.parse(seg); err != nil {
				return nil, 0, err
			}
		case mCAP, mCPF:
			return nil, 0, fmt.Errorf("jpx: high-throughput (Part 15) codestream: %w", errors.ErrUnsupported)
		case mPPM:
			if len(seg) < 1 {
				return nil, 0, errors.New("jpx: empty PPM segment")
			}
			cs.ppm = append(cs.ppm, seg[1:]) // Zppm orders them; writers emit them in order
		default:
			if err := cs.main.parse(marker, seg, len(cs.siz.comps)); err != nil {
				return nil, 0, err
			}
		}
		pos = next
	}
	if len(cs.siz.comps) == 0 {
		return nil, 0, errors.New("jpx: no SIZ segment")
	}
	if cs.main.cod == nil || cs.main.qcd == nil {
		return nil, 0, errors.New("jpx: main header lacks COD or QCD")
	}
	return cs, pos, nil
}

// segment returns the body of the marker segment at pos and the offset
// after it.
func segment(data []byte, pos int) ([]byte, int, error) {
	if pos+4 > len(data) {
		return nil, 0, errors.New("jpx: truncated marker segment")
	}
	n := int(data[pos+2])<<8 | int(data[pos+3])
	if n < 2 || pos+2+n > len(data) {
		return nil, 0, fmt.Errorf("jpx: marker 0x%04X segment overruns the data", int(data[pos])<<8|int(data[pos+1]))
	}
	return data[pos+4 : pos+2+n], pos + 2 + n, nil
}

func (s *siz) parse(seg []byte) error {
	r := &reader{b: seg}
	rsiz := r.u16()
	s.x1, s.y1, s.x0, s.y0 = r.u32(), r.u32(), r.u32(), r.u32()
	s.tw, s.th, s.tx0, s.ty0 = r.u32(), r.u32(), r.u32(), r.u32()
	n := r.u16()
	if r.err != nil {
		return r.err
	}
	if rsiz&0x4000 != 0 {
		return fmt.Errorf("jpx: high-throughput (Part 15) codestream: %w", errors.ErrUnsupported)
	}
	switch {
	case n < 1 || n > maxComponents:
		return fmt.Errorf("jpx: %d components: %w", n, errors.ErrUnsupported)
	case s.x0 >= s.x1 || s.y0 >= s.y1:
		return errors.New("jpx: empty image area")
	case s.tw < 1 || s.th < 1 || s.tx0 > s.x0 || s.ty0 > s.y0 || s.tx0+s.tw <= s.x0 || s.ty0+s.th <= s.y0:
		return errors.New("jpx: invalid tiling")
	case s.x1 > 1<<31-1 || s.y1 > 1<<31-1:
		return errors.New("jpx: image too large")
	}
	if s.tilesAcross()*s.tilesDown() > maxTiles {
		return errors.New("jpx: too many tiles")
	}
	for range n {
		ssiz, dx, dy := r.u8(), r.u8(), r.u8()
		c := component{precision: ssiz&0x7F + 1, signed: ssiz&0x80 != 0, dx: dx, dy: dy}
		if c.precision > maxPrecision {
			return fmt.Errorf("jpx: %d-bit component: %w", c.precision, errors.ErrUnsupported)
		}
		if dx < 1 || dy < 1 {
			return errors.New("jpx: zero component subsampling")
		}
		s.comps = append(s.comps, c)
	}
	return r.err
}

// compIndex reads a component index, one byte below 257 components.
func compIndex(r *reader, ncomp int) int {
	if ncomp < 257 {
		return r.u8()
	}
	return r.u16()
}

// parse applies one main-header or tile-part-header marker segment.
func (h *header) parse(marker int, seg []byte, ncomp int) error {
	r := &reader{b: seg}
	switch marker {
	case mCOD:
		scod := r.u8()
		h.order, h.layers, h.mct = r.u8(), r.u16(), r.u8() == 1
		c, err := parseCoding(r, scod&1 != 0)
		if err != nil {
			return err
		}
		if h.order > orderCPRL {
			return fmt.Errorf("jpx: unknown progression order %d", h.order)
		}
		if h.layers < 1 {
			return errors.New("jpx: zero quality layers")
		}
		h.cod, h.codSet, h.sop, h.eph = c, true, scod&2 != 0, scod&4 != 0
	case mCOC:
		ci := compIndex(r, ncomp)
		scoc := r.u8()
		c, err := parseCoding(r, scoc&1 != 0)
		if err != nil {
			return err
		}
		if ci >= ncomp {
			return fmt.Errorf("jpx: COC for component %d of %d", ci, ncomp)
		}
		if h.coc == nil {
			h.coc = map[int]*coding{}
		}
		h.coc[ci] = c
	case mQCD:
		q, err := parseQuantization(r)
		if err != nil {
			return err
		}
		h.qcd = q
	case mQCC:
		ci := compIndex(r, ncomp)
		q, err := parseQuantization(r)
		if err != nil {
			return err
		}
		if ci >= ncomp {
			return fmt.Errorf("jpx: QCC for component %d of %d", ci, ncomp)
		}
		if h.qcc == nil {
			h.qcc = map[int]*quantization{}
		}
		h.qcc[ci] = q
	case mRGN:
		ci := compIndex(r, ncomp)
		style, shift := r.u8(), r.u8()
		if r.err != nil {
			return r.err
		}
		if style != 0 || ci >= ncomp || shift > 37 {
			return errors.New("jpx: invalid RGN segment")
		}
		if h.rgn == nil {
			h.rgn = map[int]int{}
		}
		h.rgn[ci] = shift
	case mPOC:
		for len(r.rest()) > 0 && r.err == nil {
			p := progression{resStart: r.u8(), compStart: compIndex(r, ncomp), layerEnd: r.u16(), resEnd: r.u8()}
			p.compEnd = compIndex(r, ncomp)
			if p.compEnd == 0 && ncomp < 257 {
				p.compEnd = 256
			}
			p.order = r.u8()
			if p.order > orderCPRL {
				return fmt.Errorf("jpx: unknown progression order %d", p.order)
			}
			h.poc = append(h.poc, p)
		}
		return r.err
	case mTLM, mPLM, mPLT, mCRG, mCOM:
		// Informational: lengths and registration the decoder does not need.
	default:
		if marker&0xFF00 != 0xFF00 {
			return fmt.Errorf("jpx: expected a marker, found 0x%04X", marker)
		}
		// Unknown segments carry their length; skip them.
	}
	return r.err
}

func parseCoding(r *reader, precincts bool) (*coding, error) {
	c := &coding{levels: r.u8(), cbw: r.u8() + 2, cbh: r.u8() + 2, cbStyle: r.u8(), reversible: r.u8() == 1}
	if r.err != nil {
		return nil, r.err
	}
	if c.levels > maxLevels {
		return nil, fmt.Errorf("jpx: %d decomposition levels", c.levels)
	}
	if c.cbw > 10 || c.cbh > 10 || c.cbw+c.cbh > 12 {
		return nil, errors.New("jpx: invalid code-block size")
	}
	for range c.levels + 1 {
		pp := 0xFF // the maximal precinct: 2^15 in each direction
		if precincts {
			pp = r.u8()
		}
		c.ppx, c.ppy = append(c.ppx, min(pp&0xF, 15)), append(c.ppy, min(pp>>4, 15))
	}
	return c, r.err
}

func parseQuantization(r *reader) (*quantization, error) {
	sq := r.u8()
	q := &quantization{style: sq & 0x1F, guard: sq >> 5}
	for len(r.rest()) > 0 && r.err == nil {
		switch q.style {
		case 0:
			q.exp, q.mant = append(q.exp, r.u8()>>3), append(q.mant, 0)
		case 1, 2:
			v := r.u16()
			q.exp, q.mant = append(q.exp, v>>11), append(q.mant, v&0x7FF)
		default:
			return nil, fmt.Errorf("jpx: unknown quantization style %d", q.style)
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(q.exp) == 0 {
		return nil, errors.New("jpx: quantization without step sizes")
	}
	return q, nil
}

// parseTiles reads the tile-parts from pos, the first SOT.
func (cs *codestream) parseTiles(data []byte, pos int) error {
	byIndex := map[int]*tileData{}
	ntiles := cs.siz.tilesAcross() * cs.siz.tilesDown()
	ppmNext := 0
	for pos+2 <= len(data) {
		marker := int(data[pos])<<8 | int(data[pos+1])
		if marker == mEOC {
			break
		}
		if marker != mSOT {
			return fmt.Errorf("jpx: expected SOT, found 0x%04X", marker)
		}
		seg, next, err := segment(data, pos)
		if err != nil {
			return err
		}
		r := &reader{b: seg}
		index, psot := r.u16(), r.u32()
		if r.err != nil {
			return r.err
		}
		if index >= ntiles {
			return fmt.Errorf("jpx: tile %d of %d", index, ntiles)
		}
		end := len(data)
		if psot != 0 && pos+psot <= len(data) {
			end = pos + psot
		}
		t := byIndex[index]
		first := t == nil
		if first {
			t = &tileData{index: index}
			byIndex[index] = t
			cs.tiles = append(cs.tiles, t)
		}
		// The tile-part header, up to SOD.
		pos = next
		for {
			if pos+2 > end {
				return errors.New("jpx: tile-part header without SOD")
			}
			marker := int(data[pos])<<8 | int(data[pos+1])
			if marker == mSOD {
				pos += 2
				break
			}
			seg, next, err := segment(data, pos)
			if err != nil {
				return err
			}
			if next > end {
				return errors.New("jpx: marker segment overruns its tile-part")
			}
			if marker == mPPT {
				if len(seg) < 1 {
					return errors.New("jpx: empty PPT segment")
				}
				t.ppt = append(t.ppt, seg[1:])
			} else if err := t.hdr.parse(marker, seg, len(cs.siz.comps)); err != nil {
				return err
			}
			pos = next
		}
		body := data[pos:end]
		if psot == 0 && len(body) >= 2 && body[len(body)-2] == 0xFF && body[len(body)-1] == 0xD9 {
			body = body[:len(body)-2]
		}
		t.parts = append(t.parts, body)
		if len(cs.ppm) > 0 {
			if ppmNext >= 0 {
				var chunk []byte
				chunk, ppmNext = cs.nextPPM(ppmNext)
				t.ppm = append(t.ppm, chunk)
			}
		}
		pos = end
	}
	return nil
}

// nextPPM returns the packed headers of the next tile-part from the
// concatenated PPM data: an Nppm length, then that many bytes. It
// returns -1 as the next offset once the data is spent.
func (cs *codestream) nextPPM(offset int) ([]byte, int) {
	if cs.ppmData == nil {
		for _, seg := range cs.ppm {
			cs.ppmData = append(cs.ppmData, seg...)
		}
	}
	d := cs.ppmData
	if offset+4 > len(d) {
		return nil, -1
	}
	n := int(d[offset])<<24 | int(d[offset+1])<<16 | int(d[offset+2])<<8 | int(d[offset+3])
	start := offset + 4
	end := min(start+n, len(d))
	if n < 0 || start > end {
		return nil, -1
	}
	return d[start:end], end
}

// codingFor returns component c's coding style for a tile: tile COC, tile
// COD, main COC, main COD, in that order of precedence (T.800 A.6.1).
func (cs *codestream) codingFor(t *tileData, c int) *coding {
	if cc, ok := t.hdr.coc[c]; ok {
		return cc
	}
	if t.hdr.cod != nil {
		return t.hdr.cod
	}
	if cc, ok := cs.main.coc[c]; ok {
		return cc
	}
	return cs.main.cod
}

// quantizationFor returns component c's quantization for a tile, by the
// same precedence.
func (cs *codestream) quantizationFor(t *tileData, c int) *quantization {
	if q, ok := t.hdr.qcc[c]; ok {
		return q
	}
	if t.hdr.qcd != nil {
		return t.hdr.qcd
	}
	if q, ok := cs.main.qcc[c]; ok {
		return q
	}
	return cs.main.qcd
}

// roiShift returns component c's region-of-interest shift for a tile.
func (cs *codestream) roiShift(t *tileData, c int) int {
	if s, ok := t.hdr.rgn[c]; ok {
		return s
	}
	return cs.main.rgn[c]
}

// tileProgression returns the tile's layers, MCT flag, SOP/EPH use and
// progression volumes.
func (cs *codestream) tileProgression(t *tileData) (layers int, mct, sop, eph bool, vols []progression) {
	h := &cs.main
	if t.hdr.codSet {
		h = &t.hdr
	}
	layers, mct, sop, eph = h.layers, h.mct, h.sop, h.eph
	pocs := cs.main.poc
	if len(t.hdr.poc) > 0 {
		pocs = t.hdr.poc
	}
	if len(pocs) > 0 {
		return layers, mct, sop, eph, pocs
	}
	return layers, mct, sop, eph, []progression{{layerEnd: layers, resEnd: maxLevels + 1, compEnd: len(cs.siz.comps), order: h.order}}
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	if a <= 0 {
		return -((-a) / b)
	}
	return (a + b - 1) / b
}

func floorDiv(a, b int) int {
	if a >= 0 {
		return a / b
	}
	return -((-a + b - 1) / b)
}
