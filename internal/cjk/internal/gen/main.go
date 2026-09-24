// Command gen builds the CID→Unicode tables of package cjk from Adobe's
// Unicode CMaps (github.com/adobe-type-tools/cmap-resources, BSD-3-Clause):
//
//	go run ./internal/cjk/internal/gen <dir holding Uni*-UTF32-H>
//
// Each UTF-32 CMap maps Unicode to the collection's CIDs; inverted, with
// the lowest code point kept where several map to one CID — the unified
// ideograph over its compatibility forms — it gives each CID its
// character. A table is stored as runs of CIDs whose code points ascend
// with them: the gap from the previous run's CIDs, the signed step from
// its code points, and the run's length, as varints, gzip-compressed.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

var collections = map[string]string{
	"Japan1": "UniJIS-UTF32-H",
	"GB1":    "UniGB-UTF32-H",
	"CNS1":   "UniCNS-UTF32-H",
	"Korea1": "UniKS-UTF32-H",
	"KR":     "UniAKR-UTF32-H",
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: gen <dir holding the Uni*-UTF32-H CMaps>")
	}
	for ordering, name := range collections {
		table, err := invert(filepath.Join(os.Args[1], name))
		if err != nil {
			log.Fatal(err)
		}
		encoded, err := encode(table)
		if err != nil {
			log.Fatal(err)
		}
		out := filepath.Join("internal", "cjk", "data", ordering+".bin")
		if err := os.WriteFile(out, encoded, 0o600); err != nil { //nolint:gosec // a fixed path in the repository
			log.Fatal(err)
		}
		fmt.Printf("%s: %d CIDs\n", ordering, len(table))
	}
}

// invert reads a UTF-32 CMap's cidrange and cidchar mappings into CID →
// lowest code point.
func invert(path string) (map[uint32]rune, error) {
	f, err := os.Open(path) //nolint:gosec // the CMap directory is the command's argument
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	table := map[uint32]rune{}
	set := func(cid uint32, r rune) {
		if old, ok := table[cid]; !ok || r < old {
			table[cid] = r
		}
	}
	sc := bufio.NewScanner(f)
	section := ""
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		if last := fields[len(fields)-1]; strings.HasPrefix(last, "begin") || strings.HasPrefix(last, "end") {
			section = last
			continue
		}
		switch {
		case section == "begincidrange" && len(fields) == 3:
			lo, err1 := hexRune(fields[0])
			hi, err2 := hexRune(fields[1])
			cid, err3 := decimal(fields[2])
			if err := errors.Join(err1, err2, err3); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			for r := lo; r <= hi; r++ {
				set(cid+uint32(r-lo), r) //nolint:gosec // r ≥ lo
			}
		case section == "begincidchar" && len(fields) == 2:
			r, err1 := hexRune(fields[0])
			cid, err2 := decimal(fields[1])
			if err := errors.Join(err1, err2); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			set(cid, r)
		}
	}
	return table, sc.Err()
}

func hexRune(s string) (rune, error) {
	n, err := strconv.ParseInt(strings.Trim(s, "<>"), 16, 32)
	if err != nil {
		return 0, fmt.Errorf("bad code %q: %w", s, err)
	}
	return rune(n), nil
}

func decimal(s string) (uint32, error) {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("bad CID %q: %w", s, err)
	}
	return uint32(n), nil
}

func encode(table map[uint32]rune) ([]byte, error) {
	cids := make([]uint32, 0, len(table))
	for cid := range table {
		cids = append(cids, cid)
	}
	slices.Sort(cids)
	var raw []byte
	var prevCID uint32
	var prevRune rune
	for i := 0; i < len(cids); {
		j := i + 1
		for j < len(cids) && cids[j] == cids[j-1]+1 && table[cids[j]] == table[cids[j-1]]+1 {
			j++
		}
		raw = binary.AppendUvarint(raw, uint64(cids[i]-prevCID))
		raw = binary.AppendVarint(raw, int64(table[cids[i]]-prevRune))
		raw = binary.AppendUvarint(raw, uint64(j-i)) //nolint:gosec // a positive run length
		prevCID, prevRune = cids[j-1]+1, table[cids[j-1]]+1
		i = j
	}
	var b bytes.Buffer
	zw, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
