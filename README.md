# pdf

[![Go Reference](https://pkg.go.dev/badge/github.com/giraffesyo/pdf.svg)](https://pkg.go.dev/github.com/giraffesyo/pdf)
[![CI](https://github.com/giraffesyo/pdf/actions/workflows/ci.yml/badge.svg?branch=canary)](https://github.com/giraffesyo/pdf/actions/workflows/ci.yml)

Robust PDF text extraction in pure Go — with glyph positions, hardened
against real-world (and hostile) files.

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
corpora:

```
cd benchmarks
go test -run TestCompetitorComparison -v ./...   # support matrix
go test -bench . -benchmem -run '^$' ./...        # ns/op, B/op, allocs/op
```

The support matrix shows this package extracting text from every corpus —
including Form XObjects, xref streams, object streams, and encrypted files
— where the others return empty text or fail.

## pdftest

`github.com/giraffesyo/pdf/pdftest` builds minimal synthetic PDFs for
tests (in the spirit of `httptest`): document skeletons, composite fonts,
ToUnicode CMaps, Form XObjects, segmented content streams. This package's
own regression suite is built on it — every failure pattern above is
reproduced synthetically, with fuzzing on top.

## License

MIT. Embeds the [Adobe Glyph List](https://github.com/adobe-type-tools/agl-aglfn)
(Apache-2.0) for glyph-name decoding.
