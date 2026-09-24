// Package mq is the MQ adaptive binary arithmetic decoder shared by JBIG2
// (ITU-T T.88 Annex E) and JPEG 2000 (ITU-T T.800 Annex C): the same
// coder, probability table and byte-stuffing convention.
package mq

// entry is one row of the Qe probability-estimation table (T.88 Table
// E.1, T.800 Table C.2).
type entry struct {
	qe   uint32
	nmps uint8
	nlps uint8
	sw   bool
}

var table = [47]entry{
	{0x5601, 1, 1, true}, {0x3401, 2, 6, false}, {0x1801, 3, 9, false}, {0x0AC1, 4, 12, false}, {0x0521, 5, 29, false}, {0x0221, 38, 33, false},
	{0x5601, 7, 6, true}, {0x5401, 8, 14, false}, {0x4801, 9, 14, false}, {0x3801, 10, 14, false}, {0x3001, 11, 17, false}, {0x2401, 12, 18, false},
	{0x1C01, 13, 20, false}, {0x1601, 29, 21, false}, {0x5601, 15, 14, true}, {0x5401, 16, 14, false}, {0x5101, 17, 15, false}, {0x4801, 18, 16, false},
	{0x3801, 19, 17, false}, {0x3401, 20, 18, false}, {0x3001, 21, 19, false}, {0x2801, 22, 19, false}, {0x2401, 23, 20, false}, {0x2201, 24, 21, false},
	{0x1C01, 25, 22, false}, {0x1801, 26, 23, false}, {0x1601, 27, 24, false}, {0x1401, 28, 25, false}, {0x1201, 29, 26, false}, {0x1101, 30, 27, false},
	{0x0AC1, 31, 28, false}, {0x09C1, 32, 29, false}, {0x08A1, 33, 30, false}, {0x0521, 34, 31, false}, {0x0441, 35, 32, false}, {0x02A1, 36, 33, false},
	{0x0221, 37, 34, false}, {0x0141, 38, 35, false}, {0x0111, 39, 36, false}, {0x0085, 40, 37, false}, {0x0049, 41, 38, false}, {0x0025, 42, 39, false},
	{0x0015, 43, 40, false}, {0x0009, 44, 41, false}, {0x0005, 45, 42, false}, {0x0001, 45, 43, false}, {0x5601, 46, 46, false},
}

// States is the number of probability states; State values run below it.
const States = len(table)

// Probability reports state's row of the table: the LPS probability Qe,
// the next states after an MPS and an LPS, and whether an LPS exchanges
// the MPS sense. Encoders written for tests use it.
func Probability(state uint8) (qe uint32, nmps, nlps uint8, switchMPS bool) {
	e := table[min(int(state), States-1)]
	return e.qe, e.nmps, e.nlps, e.sw
}

// Context is one adaptive context: its probability state and current
// more probable symbol. The zero value is state 0 with MPS 0, the usual
// initial context; JPEG 2000 starts a few contexts elsewhere.
type Context struct {
	State uint8
	MPS   uint8
}

// Decoder holds the registers of the software-conventions decoder (T.88
// E.3.2 ff). C is kept as one 32-bit register whose upper half is Chigh.
// Reading past the end of the data yields 0xFF bytes, which the decoder
// treats as a marker, so exhausted input never stops a decode: callers
// bound every loop by declared dimensions and counts.
type Decoder struct {
	data []byte
	bp   int
	c    uint32
	a    uint32
	ct   int
}

// NewDecoder starts decoding data (INITDEC).
func NewDecoder(data []byte) *Decoder {
	d := &Decoder{}
	d.Reset(data)
	return d
}

// Reset restarts the decoder on new data, as JPEG 2000 does at every
// terminated coding pass, without allocating.
func (d *Decoder) Reset(data []byte) {
	*d = Decoder{data: data}
	d.c = uint32(d.byteAt(0)) << 16
	d.byteIn()
	d.c <<= 7
	d.ct -= 7
	d.a = 0x8000
}

func (d *Decoder) byteAt(i int) byte {
	if i < len(d.data) {
		return d.data[i]
	}
	return 0xFF
}

// byteIn implements BYTEIN (T.88 Figure E.19): a 0xFF followed by a byte
// above 0x8F is a marker and is not consumed; otherwise a stuffed bit is
// skipped after 0xFF.
func (d *Decoder) byteIn() {
	if d.byteAt(d.bp) == 0xFF {
		if d.byteAt(d.bp+1) > 0x8F {
			d.c += 0xFF00
			d.ct = 8
		} else {
			d.bp++
			d.c += uint32(d.byteAt(d.bp)) << 9
			d.ct = 7
		}
	} else {
		d.bp++
		d.c += uint32(d.byteAt(d.bp)) << 8
		d.ct = 8
	}
}

// Decode returns the next decision, 0 or 1, for context cx (DECODE,
// T.88 Figure E.17, with the exchanges and renormalisation inlined).
func (d *Decoder) Decode(cx *Context) uint32 {
	q := &table[cx.State]
	qe := q.qe
	d.a -= qe
	var bit uint32
	if (d.c >> 16) < qe {
		// LPS exchange
		if d.a < qe {
			d.a = qe
			bit = uint32(cx.MPS)
			cx.State = q.nmps
		} else {
			d.a = qe
			bit = uint32(1 - cx.MPS)
			if q.sw {
				cx.MPS = 1 - cx.MPS
			}
			cx.State = q.nlps
		}
	} else {
		d.c -= qe << 16
		if d.a&0x8000 != 0 {
			return uint32(cx.MPS)
		}
		// MPS exchange
		if d.a < qe {
			bit = uint32(1 - cx.MPS)
			if q.sw {
				cx.MPS = 1 - cx.MPS
			}
			cx.State = q.nlps
		} else {
			bit = uint32(cx.MPS)
			cx.State = q.nmps
		}
	}
	// RENORMD
	for {
		if d.ct == 0 {
			d.byteIn()
		}
		d.a <<= 1
		d.c <<= 1
		d.ct--
		if d.a&0x8000 != 0 {
			break
		}
	}
	return bit
}
