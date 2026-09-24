package jbig2

// The arithmetic integer decoding procedures of Annex A, over the MQ
// decoder of Annex E (internal/codec/mq, shared with JPEG 2000).

import "github.com/giraffesyo/pdf/internal/codec/mq"

// mqCx and mqDecoder are the shared MQ decoder under the names this
// package's decoding procedures were written with.
type mqCx = mq.Context

type mqDecoder struct{ *mq.Decoder }

func newMQDecoder(data []byte) *mqDecoder { return &mqDecoder{mq.NewDecoder(data)} }

func (d *mqDecoder) decode(cx *mqCx) uint32 { return d.Decode(cx) }

// intClasses are the value widths and offsets of Table A.1, indexed by
// the number of leading 1 bits in the prefix.
var intClasses = [6]struct{ n, offset uint32 }{{2, 0}, {4, 4}, {6, 20}, {8, 84}, {12, 340}, {32, 4436}}

// intCtx is the 512-entry context set of one arithmetic integer decoding
// procedure (IADH, IADW, ... of Annex A).
type intCtx [512]mqCx

// decodeInt implements Annex A.2. ok is false for OOB.
func (d *mqDecoder) decodeInt(cx *intCtx) (v int32, ok bool) {
	prev := uint32(1)
	bit := func() uint32 {
		b := d.decode(&cx[prev])
		if prev < 256 {
			prev = prev<<1 | b
		} else {
			prev = (((prev << 1) | b) & 511) | 256
		}
		return b
	}
	s := bit()
	// The prefix of Table A.1: up to five 1 bits, terminated by a 0 (or
	// the fifth 1), select the value width and offset.
	k := 0
	for k < len(intClasses)-1 && bit() == 1 {
		k++
	}
	n, offset := intClasses[k].n, intClasses[k].offset
	var val uint32
	for range n {
		val = val<<1 | bit()
	}
	val += offset
	if s == 1 {
		if val == 0 {
			return 0, false // OOB
		}
		// Clamp so the negation cannot overflow int32 on hostile input.
		if val > 1<<31-1 {
			val = 1<<31 - 1
		}
		return -int32(val), true
	}
	if val > 1<<31-1 {
		val = 1<<31 - 1
	}
	return int32(val), true
}

// decodeIAID implements Annex A.3: a symCodeLen-bit symbol ID read with a
// binary tree of contexts. cx must have 1<<(symCodeLen+1) entries.
func (d *mqDecoder) decodeIAID(cx []mqCx, symCodeLen uint) uint32 {
	prev := uint32(1)
	for range symCodeLen {
		prev = prev<<1 | d.decode(&cx[prev])
	}
	return prev - 1<<symCodeLen
}
