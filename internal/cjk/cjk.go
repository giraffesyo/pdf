// Package cjk maps the CIDs of Adobe's Chinese, Japanese, and Korean
// character collections to Unicode, for fonts that give no other way to
// read their text: no ToUnicode map, and a font program with no cmap.
//
// The tables are derived from Adobe's Unicode CMaps (github.com/
// adobe-type-tools/cmap-resources, BSD-3-Clause; see data/NOTICE) by
// internal/gen, and decoded on first use, one collection at a time.
package cjk

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/binary"
	"io"
	"slices"
	"sync"
)

//go:embed data/*.bin
var data embed.FS

// run is a span of consecutive CIDs whose characters are consecutive
// code points.
type run struct {
	cid    uint32
	r      rune
	length uint32
}

type table struct {
	once sync.Once
	runs []run
}

var tables = map[string]*table{
	"Japan1": {}, "GB1": {}, "CNS1": {}, "Korea1": {}, "KR": {},
}

// Unicode returns the character of cid in the Adobe collection named by
// ordering — Japan1, GB1, CNS1, Korea1, or KR — and whether it has one.
// CIDs for vertical, rotated, and proportional variants of a character
// have none; their horizontal forms do.
func Unicode(ordering string, cid uint32) (rune, bool) {
	t, ok := tables[ordering]
	if !ok {
		return 0, false
	}
	t.once.Do(func() { t.runs = load(ordering) })
	i, found := slices.BinarySearchFunc(t.runs, cid, func(r run, cid uint32) int {
		switch {
		case r.cid+r.length <= cid:
			return -1
		case r.cid > cid:
			return 1
		}
		return 0
	})
	if !found {
		return 0, false
	}
	return t.runs[i].r + rune(cid-t.runs[i].cid), true //nolint:gosec // within a run's length
}

// Known reports whether ordering names a collection Unicode covers.
func Known(ordering string) bool {
	_, ok := tables[ordering]
	return ok
}

func load(ordering string) []run {
	f, err := data.Open("data/" + ordering + ".bin")
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil
	}
	var runs []run
	var cid uint32
	var r rune
	for rd := bytes.NewReader(raw); rd.Len() > 0; {
		gap, err1 := binary.ReadUvarint(rd)
		step, err2 := binary.ReadVarint(rd)
		length, err3 := binary.ReadUvarint(rd)
		if err1 != nil || err2 != nil || err3 != nil {
			return runs
		}
		cid += uint32(gap)                                               //nolint:gosec // generated data
		r += rune(step)                                                  //nolint:gosec // generated data
		runs = append(runs, run{cid: cid, r: r, length: uint32(length)}) //nolint:gosec // generated data
		cid += uint32(length)                                            //nolint:gosec // generated data
		r += rune(length)                                                //nolint:gosec // generated data
	}
	return runs
}
