# pdf

[![Go Reference](https://pkg.go.dev/badge/github.com/giraffesyo/pdf.svg)](https://pkg.go.dev/github.com/giraffesyo/pdf)
[![CI](https://github.com/giraffesyo/pdf/actions/workflows/ci.yml/badge.svg?branch=canary)](https://github.com/giraffesyo/pdf/actions/workflows/ci.yml)

Robust PDF text extraction in pure Go — with glyph positions, hardened
against real-world (and hostile) files.

**Zero dependencies.** Built entirely on Go's standard library.

```go
import "github.com/giraffesyo/pdf"

doc, err := pdf.Extract(ctx, readerAt, size)
if err != nil { ... }

text := doc.Text()             // whole document, plain text
page := doc.Pages[0]
line := page.Text()            // per-page reconstruction
glyphs := page.Glyphs          // []Glyph{Text, X, Y, Advance, Size}
```

`Page.Text` reconstructs reading order from glyph positions: lines are
clustered by baseline, and word boundaries are recovered from glyph gaps
and font metrics — so PDFs that encode no space characters at all still
come out readable.

## Why another PDF text extractor

This package grew out of converting large corpora of real-world documents
with [ledongthuc/pdf](https://github.com/ledongthuc/pdf), whose built-in
text extraction breaks on files that are common in the wild:

| Real-world file | ledongthuc/pdf | this package |
|---|---|---|
| Google Docs exports (text inside Form XObjects) | extracts nothing | full text |
| Word exports (Identity-H fonts + ToUnicode CMaps) | U+FFFD soup | decoded |
| Type3 fonts with ToUnicode but an /Encoding dict | control-char garbage | decoded |
| Td/T*-positioned text (most PDFs) | positions lost | positioned |
| Array token split across /Contents segments | lexer hangs forever | handled |
| Decompression bombs, stalled filter chains | unbounded CPU/memory | hard budgets |
| Cyclic page trees | stack overflow (crash) | rejected upfront |
| Inline images (BI/ID/EI) | derails the lexer | skipped cleanly |

Everything is implemented here, from ISO 32000 directly, with **no
external dependencies**: the object layer (cross-reference tables and
streams, object streams, hybrid references, stream filters, and
standard-handler decryption), the standard font encodings and Adobe Glyph
List, and everything from the content stream down — lexer, interpreter,
text state machine, CTM/Form-XObject handling, ToUnicode parsing, CID
widths.

Undecodable glyphs (no ToUnicode, no standard encoding) are **dropped, not
emitted as garbage**, so image-only or outlined-text PDFs yield empty
output you can detect, instead of plausible-looking noise.

Ligature presentation forms (`ﬁ`, `ﬂ`, `ﬀ`, `ﬃ`, `ﬄ`, `ﬅ`, `ﬆ`) — which
TeX-generated PDFs reach through `/Differences` glyph names — fold to their
letter sequences, as `pdftotext` and pdf.js do, so extracted text stays
searchable.

## Limitations

- No OCR: scanned pages and text converted to vector outlines have no text
  to extract.
- No layout analysis beyond line/word reconstruction: complex multi-column
  layouts may interleave.
- Encrypted files open only when the empty user or owner password unlocks
  them (RC4, AES-128, AES-256); password-protected files return an error.
- Image filters (DCT/JPX/CCITT/JBIG2) are not decoded — text extraction
  never needs them.

## Benchmarks

The `benchmarks/` directory is a separate module (so its dependencies stay
out of this one) that compares extraction against
[ledongthuc/pdf](https://github.com/ledongthuc/pdf) and
[rsc.io/pdf](https://pkg.go.dev/rsc.io/pdf) over identical synthetic
corpora. The corpus checks both whether the expected text is recovered and
whether extraction completes safely. A comparator that returns incorrect
text, errors, or panics is excluded from the performance run for that
fixture.

Results below were measured on 2026-07-15 from the current worktree based
on commit `4d13aa9`, using Go `1.26.4` on macOS `26.5.1` (`darwin/arm64`,
Apple M5 Pro). Values are one run of
`go test -bench . -benchmem -run '^$' -count=1 ./...`; rerun on your own
hardware before making a performance decision.

| Corpus | this package | ledongthuc/pdf | rsc.io/pdf |
|---|---:|---:|---:|
| simple | ok | ok | incorrect text |
| Form XObject | ok | incorrect text | incorrect text |
| Flate-compressed content | ok | ok | incorrect text |
| xref stream | ok | ok | incorrect text |
| object stream | ok | ok | incorrect text |
| RC4-encrypted | ok | unsupported | unsupported |

Latency (`ns/op`):

| Corpus | this package | ledongthuc/pdf |
|---|---:|---:|
| simple | 12,548 | 29,796 |
| Form XObject | 15,342 | incorrect text |
| Flate content | 20,080 | 41,339 |
| xref stream | 13,168 | 26,940 |
| object stream | 19,330 | 47,061 |
| RC4-encrypted | 61,169 | unsupported |

Memory (`B/op`) and allocations (`allocs/op`):

| Corpus | this package B/op | ledongthuc/pdf B/op | this package allocs/op | ledongthuc/pdf allocs/op |
|---|---:|---:|---:|---:|
| simple | 53,120 | 62,963 | 153 | 368 |
| Form XObject | 57,480 | incorrect text | 198 | incorrect text |
| Flate content | 98,553 | 107,891 | 171 | 386 |
| xref stream | 54,008 | 62,882 | 157 | 365 |
| object stream | 54,530 | 100,340 | 176 | 541 |
| RC4-encrypted | 85,727 | unsupported | 315 | unsupported |

To reproduce the support matrix and performance measurements:

```
cd benchmarks
go test -run TestCompetitorComparison -v ./...   # support matrix
go test -bench . -benchmem -run '^$' -count=1 ./... # raw results above
# For a less noisy local comparison, repeat and analyze the output:
go test -bench . -benchmem -run '^$' -count=5 ./...
```

The corpus is synthetic and deliberately small: it measures parser and
extractor behavior, not throughput on a representative production document
set. It does show that this package recovers the expected text from every
fixture, including Form XObjects, xref streams, object streams, and
encrypted files.

## pdftest

`github.com/giraffesyo/pdf/pdftest` builds minimal synthetic PDFs for
tests (in the spirit of `httptest`): document skeletons, composite fonts,
ToUnicode CMaps, Form XObjects, segmented content streams. This package's
own regression suite is built on it — every failure pattern above is
reproduced synthetically, with fuzzing on top.

## License

MIT. Embeds the [Adobe Glyph List](https://github.com/adobe-type-tools/agl-aglfn)
(Apache-2.0) for glyph-name decoding.
