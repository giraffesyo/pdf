package jbig2

import (
	"errors"
	"fmt"
)

// Symbol dictionary segments (T.88 §6.5, §7.4.3) with arithmetic coding.

// symbolDict is the decoded result of a symbol dictionary segment: its
// exported symbols, plus the retained arithmetic contexts when the
// segment asked for them (7.4.3.1.1 "bitmap coding context retained").
type symbolDict struct {
	exported []*bitmap
	cx       []mqCx // generic contexts; nil unless retained
	gr       []mqCx // refinement contexts; nil unless retained with SDREFAGG
}

// maxSymbols bounds the number of symbols a dictionary may declare; real
// pages have a few thousand distinct glyphs, and the per-symbol pixel
// budget bounds the rest.
const maxSymbols = 1 << 20

// decodeSymbolDict parses and decodes a type 0 segment (7.4.3) whose
// referred-to segments have been resolved to inputs.
func (d *decoder) decodeSymbolDict(data []byte, inputDicts []*symbolDict) (*symbolDict, error) {
	r := &reader{data: data}
	flags, err := r.u16()
	if err != nil {
		return nil, err
	}
	sdHuff := flags&1 != 0
	sdRefAgg := flags>>1&1 != 0
	ctxUsed := flags>>8&1 != 0
	ctxRetained := flags>>9&1 != 0
	template := uint8(flags >> 10 & 3)
	rTemplate := uint8(flags >> 12 & 1)
	if sdHuff {
		return nil, fmt.Errorf("huffman-coded symbol dictionary: %w", errors.ErrUnsupported)
	}
	gp := genericParams{template: template}
	nAT := 1
	if template == 0 {
		nAT = 4
	}
	for i := range nAT {
		if gp.at[i][0], err = r.i8(); err != nil {
			return nil, err
		}
		if gp.at[i][1], err = r.i8(); err != nil {
			return nil, err
		}
	}
	var rAT [2][2]int
	if sdRefAgg && rTemplate == 0 {
		if rAT, err = readRefineAT(r); err != nil {
			return nil, err
		}
	}
	numEx, err := r.u32()
	if err != nil {
		return nil, err
	}
	numNew, err := r.u32()
	if err != nil {
		return nil, err
	}
	if numEx > maxSymbols || numNew > maxSymbols {
		return nil, fmt.Errorf("symbol dictionary declares %d exported / %d new symbols", numEx, numNew)
	}

	// 6.5.8.2: the input symbols are the exports of the referred-to
	// dictionaries, in order.
	var input []*bitmap
	for _, sd := range inputDicts {
		input = append(input, sd.exported...)
	}
	if len(input)+int(numNew) > maxSymbols {
		return nil, fmt.Errorf("symbol dictionary too large (%d symbols)", len(input)+int(numNew))
	}

	var cx, gr []mqCx
	if ctxUsed {
		// Reuse the contexts of the last referred-to dictionary that
		// retained them (7.4.3.2.3 step 2).
		for i := len(inputDicts) - 1; i >= 0 && cx == nil; i-- {
			if inputDicts[i].cx != nil {
				cx = append([]mqCx(nil), inputDicts[i].cx...)
				if inputDicts[i].gr != nil {
					gr = append([]mqCx(nil), inputDicts[i].gr...)
				}
			}
		}
		if cx == nil {
			return nil, errors.New("symbol dictionary reuses contexts no referred-to dictionary retained")
		}
	} else {
		cx = make([]mqCx, 1<<16)
	}
	// 6.5.8.2: aggregate and refined symbols are coded as text regions
	// over the input symbols and the new ones decoded so far, with one set
	// of contexts for the whole dictionary and IDs wide enough for every
	// symbol it will hold.
	var tc *textCtx
	if sdRefAgg {
		tc = newTextCtx(symCodeLen(len(input) + int(numNew)))
		if gr == nil {
			gr = make([]mqCx, refineContexts)
		}
		tc.gr = gr
	}
	var iaai intCtx

	mq := newMQDecoder(r.rest())
	var iadh, iadw, iaex intCtx
	// all is the input symbols followed by the new ones as they are
	// decoded: what aggregate symbols refer to, and what export flags
	// select from.
	all := make([]*bitmap, len(input), len(input)+int(numNew))
	copy(all, input)
	newSyms := 0
	hcHeight := 0
	// 6.5.5 step 4: height classes until all new symbols are decoded. A
	// class normally holds at least one symbol; empty classes are
	// tolerated but capped so exhausted data cannot spin forever.
	for classes := 0; newSyms < int(numNew); classes++ {
		if classes > 2*int(numNew)+2 {
			return nil, errors.New("too many empty height classes")
		}
		dh, ok := mq.decodeInt(&iadh)
		if !ok {
			return nil, errors.New("OOB height class delta")
		}
		hcHeight += int(dh)
		if hcHeight < 0 || hcHeight > MaxPixels {
			return nil, fmt.Errorf("invalid symbol height %d", hcHeight)
		}
		symWidth := 0
		for {
			dw, ok := mq.decodeInt(&iadw)
			if !ok {
				break // OOB ends the height class
			}
			if newSyms >= int(numNew) {
				return nil, errors.New("symbol dictionary decodes more symbols than declared")
			}
			symWidth += int(dw)
			if symWidth < 0 || symWidth > MaxPixels {
				return nil, fmt.Errorf("invalid symbol width %d", symWidth)
			}
			if err := d.charge(symWidth, hcHeight); err != nil {
				return nil, err
			}
			var bm *bitmap
			if sdRefAgg {
				bm, err = d.decodeAggregate(mq, &iaai, tc, all, symWidth, hcHeight, rTemplate, rAT)
			} else {
				gp.w, gp.h = symWidth, hcHeight
				bm, err = decodeGeneric(&gp, mq, cx)
			}
			if err != nil {
				return nil, err
			}
			all = append(all, bm)
			newSyms++
		}
	}

	// 6.5.10: export flags as alternating runs of non-exported/exported
	// symbols over the concatenation of input and new symbols.
	exported := make([]*bitmap, 0, numEx)
	cur := false
	// Each non-zero run advances i; zero-length runs only flip the flag,
	// so hostile data cannot loop forever within this iteration bound.
	for i, iter := 0, 0; i < len(all); iter++ {
		if iter > 2*len(all)+4 {
			return nil, errors.New("symbol export flags do not terminate")
		}
		run, ok := mq.decodeInt(&iaex)
		if !ok || run < 0 || int(run) > len(all)-i {
			return nil, errors.New("invalid symbol export run length")
		}
		if cur {
			exported = append(exported, all[i:i+int(run)]...)
		}
		i += int(run)
		cur = !cur
	}
	if len(exported) > int(numEx) {
		return nil, fmt.Errorf("symbol dictionary exports %d symbols, declared %d", len(exported), numEx)
	}
	sd := &symbolDict{exported: exported}
	if ctxRetained {
		sd.cx, sd.gr = cx, gr
	}
	return sd, nil
}

// decodeAggregate decodes one symbol of a dictionary with SDREFAGG set
// (6.5.8.2): REFAGGNINST instances of earlier symbols drawn as a text
// region, or, for a single instance, one earlier symbol refined.
func (d *decoder) decodeAggregate(mq *mqDecoder, iaai *intCtx, tc *textCtx, syms []*bitmap, w, h int, rTemplate uint8, rAT [2][2]int) (*bitmap, error) {
	n, ok := mq.decodeInt(iaai)
	if !ok || n <= 0 || n > maxInstances {
		return nil, fmt.Errorf("invalid aggregate instance count %d", n)
	}
	if n > 1 {
		t := textRegion{
			numInst:   uint32(n),
			refine:    true,
			rTemplate: rTemplate,
			rAT:       rAT,
			refCorner: refTopLeft,
			combOp:    opOr,
		}
		bm, err := newBitmap(w, h)
		if err != nil {
			return nil, err
		}
		if err := d.decodeTextInstances(&t, bm, syms, mq, tc); err != nil {
			return nil, err
		}
		return bm, nil
	}
	id := mq.decodeIAID(tc.iaid, tc.codeLen)
	if int(id) >= len(syms) {
		return nil, fmt.Errorf("refined symbol ID %d out of range (%d symbols)", id, len(syms))
	}
	rdx, ok1 := mq.decodeInt(&tc.iardx)
	rdy, ok2 := mq.decodeInt(&tc.iardy)
	if !ok1 || !ok2 {
		return nil, errors.New("OOB refinement offset")
	}
	p := refineParams{w: w, h: h, template: rTemplate, at: rAT, ref: syms[id], dx: int(rdx), dy: int(rdy)}
	return decodeRefinement(&p, mq, tc.gr)
}
