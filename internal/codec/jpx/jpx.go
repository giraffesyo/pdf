// Package jpx decodes JPEG 2000 (ITU-T T.800 | ISO/IEC 15444-1), the
// coding of the PDF JPXDecode filter (ISO 32000-1 §7.4.9): JP2 files and
// bare codestreams. It is written from the specification.
//
// Supported: every progression order and progression order changes, tiles
// and tile-parts, precincts, packed packet headers (PPM, PPT), SOP and EPH
// markers, all code-block styles, the 5/3 and 9/7 wavelets, scalar and no
// quantization, region-of-interest shifts, the reversible and irreversible
// component transforms, component subsampling, and the JP2 colour, palette,
// channel-mapping and channel-definition boxes. Components deeper than 16
// bits and the Part 15 high-throughput coder return errors wrapping
// errors.ErrUnsupported.
//
// Samples are delivered at 8 bits. Decode never panics on malformed input;
// damaged or truncated code-block data decodes to what it holds.
package jpx

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
)

// ColorSpace is the colour interpretation a JP2 file declares.
type ColorSpace int

// The colour spaces Decode reports. Unknown is a bare codestream, or a JP2
// file whose colour specification is none of these.
const (
	Unknown ColorSpace = iota
	Gray
	RGB
	CMYK
	YCC // sYCC; Decode converts it to RGB
)

// Config is an image's size and channel count, read from its headers.
type Config struct {
	Width, Height int
	Components    int // codestream components, before any palette
}

// Image is a decoded image: one plane of Width×Height 8-bit samples per
// colour channel, in colour order, opacity channels removed.
type Image struct {
	Width, Height int
	Channels      [][]byte
	ColorSpace    ColorSpace
}

// Options adjusts Decode.
type Options struct {
	// MaxSamples bounds the samples Decode allocates over all
	// components; zero means no bound.
	MaxSamples int
	// Indexed returns palette indices instead of applying the file's
	// palette: a PDF whose image dictionary declares an Indexed colour
	// space supplies the palette itself.
	Indexed bool
}

// DecodeConfig reads the image's dimensions without decoding it.
func DecodeConfig(data []byte) (Config, error) {
	j, err := parseJP2(data)
	if err != nil {
		return Config{}, err
	}
	cs, _, err := parseMainHeader(j.codestream)
	if err != nil {
		return Config{}, err
	}
	return Config{Width: cs.siz.x1 - cs.siz.x0, Height: cs.siz.y1 - cs.siz.y0, Components: len(cs.siz.comps)}, nil
}

// Decode decodes the image.
func Decode(data []byte, opts Options) (*Image, error) {
	maxSamples := opts.MaxSamples
	j, err := parseJP2(data)
	if err != nil {
		return nil, err
	}
	cs, pos, err := parseMainHeader(j.codestream)
	if err != nil {
		return nil, err
	}
	s := &cs.siz
	width, height := s.x1-s.x0, s.y1-s.y0
	total := 0
	for _, c := range s.comps {
		cw, ch := ceilDiv(s.x1, c.dx)-ceilDiv(s.x0, c.dx), ceilDiv(s.y1, c.dy)-ceilDiv(s.y0, c.dy)
		total += cw * ch
	}
	if maxSamples > 0 && (width > maxSamples/height || total > maxSamples) {
		return nil, fmt.Errorf("jpx: %d×%d image with %d components exceeds the sample limit", width, height, len(s.comps))
	}
	if err := cs.parseTiles(j.codestream, pos); err != nil && len(cs.tiles) == 0 {
		return nil, err
	}

	planes := make([][]byte, len(s.comps))
	for c, comp := range s.comps {
		cw, ch := ceilDiv(s.x1, comp.dx)-ceilDiv(s.x0, comp.dx), ceilDiv(s.y1, comp.dy)-ceilDiv(s.y0, comp.dy)
		planes[c] = make([]byte, cw*ch)
	}
	for _, t := range cs.tiles {
		cs.decodeTile(t, planes)
	}
	if opts.Indexed {
		j.palette, j.mapping = nil, nil
	}
	return j.assemble(s, planes)
}

// decodeTile decodes one tile into the component planes.
func (cs *codestream) decodeTile(t *tileData, planes [][]byte) {
	tl := cs.newTile(t)
	for _, tc := range tl.comps {
		for _, res := range tc.res {
			for _, b := range res.bands {
				if b.x1 > b.x0 && b.y1 > b.y0 {
					b.coeffs = make([]float32, (b.x1-b.x0)*(b.y1-b.y0))
				}
			}
		}
	}
	cs.decodePackets(t, tl)
	decodeBlocks(tl)
	for _, tc := range tl.comps {
		tc.reconstruct()
	}
	_, mct, _, _, _ := cs.tileProgression(t)
	if mct && len(tl.comps) >= 3 {
		inverseMCT(tl.comps[:3])
	}
	s := &cs.siz
	for c, tc := range tl.comps {
		cw := ceilDiv(s.x1, tc.dx) - ceilDiv(s.x0, tc.dx)
		ox, oy := ceilDiv(s.x0, tc.dx), ceilDiv(s.y0, tc.dy)
		w := tc.x1 - tc.x0
		shift := float32(0)
		if !tc.signed {
			shift = float32(int(1) << (tc.prec - 1))
		}
		maxv := float32(int(1)<<tc.prec - 1)
		lo := float32(0)
		if tc.signed {
			lo = -float32(int(1) << (tc.prec - 1))
			maxv = -lo - 1
			shift = 0
		}
		for y := tc.y0; y < tc.y1; y++ {
			row := planes[c][(y-oy)*cw:]
			src := tc.data[(y-tc.y0)*w:]
			for x := tc.x0; x < tc.x1; x++ {
				v := src[x-tc.x0] + shift
				v = float32(math.Round(float64(v)))
				v = max(lo, min(v, maxv))
				if tc.signed {
					v -= lo // store signed samples offset to unsigned
				}
				row[x-ox] = to8(v, tc.prec)
			}
		}
	}
}

// blockJob is one code-block to decode, with what its decoding needs.
type blockJob struct {
	cb *codeBlock
	b  *band
	tc *tileComp
}

// decodeBlocks decodes the tile's code-blocks, which are independent, on
// up to GOMAXPROCS goroutines.
func decodeBlocks(tl *tile) {
	var jobs []blockJob
	for _, tc := range tl.comps {
		for _, res := range tc.res {
			for _, b := range res.bands {
				for p := range b.precincts {
					pr := &b.precincts[p]
					for i := range pr.blocks {
						if pr.blocks[i].passes > 0 {
							jobs = append(jobs, blockJob{&pr.blocks[i], b, tc})
						}
					}
				}
			}
		}
	}
	workers := min(runtime.GOMAXPROCS(0), (len(jobs)+15)/16)
	if workers <= 1 {
		var dec t1
		for _, j := range jobs {
			dec.decodeBlock(j.cb, j.b, j.tc.coding.cbStyle, j.tc.roi, j.tc.coding.reversible)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			var dec t1
			for {
				i := int(next.Add(1) - 1)
				if i >= len(jobs) {
					return
				}
				j := jobs[i]
				dec.decodeBlock(j.cb, j.b, j.tc.coding.cbStyle, j.tc.roi, j.tc.coding.reversible)
			}
		})
	}
	wg.Wait()
}

// to8 scales a prec-bit sample to 8 bits.
func to8(v float32, prec int) byte {
	switch {
	case prec == 8:
		return byte(v)
	case prec < 8:
		return byte(int(v) * 255 / (1<<prec - 1) & 0xFF)
	}
	return byte(int(v) >> (prec - 8) & 0xFF)
}

// inverseMCT undoes the component transform (T.800 G.2, G.3) on the first
// three components.
func inverseMCT(c []*tileComp) {
	a, b, d := c[0].data, c[1].data, c[2].data
	n := min(len(a), len(b), len(d))
	if c[0].coding.reversible {
		for i := range n {
			y0, y1, y2 := a[i], b[i], d[i]
			g := y0 - float32(math.Floor(float64(y1+y2)/4))
			a[i], b[i], d[i] = y2+g, g, y1+g
		}
		return
	}
	for i := range n {
		y0, y1, y2 := a[i], b[i], d[i]
		a[i] = y0 + 1.402*y2
		b[i] = y0 - 0.34413*y1 - 0.71414*y2
		d[i] = y0 + 1.772*y1
	}
}

// assemble turns component planes into the image's colour channels:
// upsampling subsampled components, applying a palette, dropping
// opacity, and converting sYCC.
func (j *jp2) assemble(s *siz, planes [][]byte) (*Image, error) {
	width, height := s.x1-s.x0, s.y1-s.y0
	full := make([][]byte, len(planes))
	for c, comp := range s.comps {
		full[c] = upsample(planes[c], s, comp)
	}
	channels := full
	if j.palette != nil {
		var err error
		channels, err = j.applyPalette(full, s)
		if err != nil {
			return nil, err
		}
	}
	channels = j.colourChannels(channels)
	img := &Image{Width: width, Height: height, Channels: channels, ColorSpace: j.colour}
	if img.ColorSpace == YCC && len(channels) >= 3 {
		yccToRGB(channels[:3])
		img.ColorSpace = RGB
	}
	return img, nil
}

// upsample maps a component's plane onto the image grid by nearest
// neighbour.
func upsample(plane []byte, s *siz, comp component) []byte {
	width, height := s.x1-s.x0, s.y1-s.y0
	if comp.dx == 1 && comp.dy == 1 {
		return plane
	}
	cw := ceilDiv(s.x1, comp.dx) - ceilDiv(s.x0, comp.dx)
	ch := ceilDiv(s.y1, comp.dy) - ceilDiv(s.y0, comp.dy)
	ox, oy := ceilDiv(s.x0, comp.dx), ceilDiv(s.y0, comp.dy)
	out := make([]byte, width*height)
	if cw <= 0 || ch <= 0 || len(plane) < cw*ch {
		return out // a component too subsampled to have samples
	}
	for y := range height {
		sy := min(max((s.y0+y)/comp.dy-oy, 0), ch-1)
		for x := range width {
			sx := min(max((s.x0+x)/comp.dx-ox, 0), cw-1)
			out[y*width+x] = plane[sy*cw+sx]
		}
	}
	return out
}

func (j *jp2) applyPalette(comps [][]byte, s *siz) ([][]byte, error) {
	p := j.palette
	mapping := j.mapping
	for _, m := range mapping {
		if m.comp >= len(comps) || m.palette && m.column >= p.columns {
			mapping = nil // unusable: fall back to the palette over component 0
			break
		}
	}
	if len(mapping) == 0 {
		for col := range p.columns {
			mapping = append(mapping, channelMap{comp: 0, palette: true, column: col})
		}
	}
	var out [][]byte
	for _, m := range mapping {
		if m.comp >= len(comps) {
			return nil, errors.New("jpx: channel mapping names a missing component")
		}
		if !m.palette {
			out = append(out, comps[m.comp])
			continue
		}
		if m.column >= p.columns {
			return nil, errors.New("jpx: channel mapping names a missing palette column")
		}
		// The index is the component's raw sample, before scaling to 8 bits.
		prec := s.comps[m.comp].precision
		src := comps[m.comp]
		ch := make([]byte, len(src))
		for i, v := range src {
			idx := int(v)
			if prec > 8 {
				idx = int(v) << (prec - 8)
			} else if prec < 8 {
				idx = int(v) * (1<<prec - 1) / 255
			}
			idx = min(idx, p.entries-1)
			ch[i] = to8(float32(p.values[idx*p.columns+m.column]), p.precision[m.column])
		}
		out = append(out, ch)
	}
	return out, nil
}

// colourChannels orders the colour channels by their cdef associations
// and drops opacity channels. Without cdef, a channel beyond what the
// colour space needs is taken as opacity.
func (j *jp2) colourChannels(ch [][]byte) [][]byte {
	if len(j.defs) > 0 {
		out := make([][]byte, 0, len(ch))
		var assoc []int
		for _, d := range j.defs {
			if d.typ != 0 || d.channel >= len(ch) {
				continue
			}
			out = append(out, ch[d.channel])
			assoc = append(assoc, d.assoc)
		}
		// Sort by association, keeping order among equals.
		for i := 1; i < len(out); i++ {
			for k := i; k > 0 && assoc[k] < assoc[k-1] && assoc[k] > 0; k-- {
				out[k], out[k-1] = out[k-1], out[k]
				assoc[k], assoc[k-1] = assoc[k-1], assoc[k]
			}
		}
		if len(out) > 0 {
			return out
		}
		return ch
	}
	need := map[ColorSpace]int{Gray: 1, RGB: 3, YCC: 3, CMYK: 4}[j.colour]
	if need > 0 && len(ch) == need+1 {
		return ch[:need]
	}
	return ch
}

// yccToRGB converts full-range sYCC to RGB in place.
func yccToRGB(ch [][]byte) {
	y, cb, cr := ch[0], ch[1], ch[2]
	for i := range y {
		yy, b, r := float64(y[i]), float64(cb[i])-128, float64(cr[i])-128
		y[i] = clamp8(yy + 1.402*r)
		cb[i] = clamp8(yy - 0.344136*b - 0.714136*r)
		cr[i] = clamp8(yy + 1.772*b)
	}
}

func clamp8(v float64) byte {
	return byte(max(0, min(255, math.Round(v))))
}
