# pdf

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

It still uses ledongthuc/pdf's object/xref parsing and standard font
encodings underneath (aliased internally); everything from the content
stream down — lexer, interpreter, text state machine, CTM/Form-XObject
handling, ToUnicode parsing, CID widths — is implemented here. Replacing
the remaining object layer with a native implementation is on the roadmap.

Undecodable glyphs (no ToUnicode, no standard encoding) are **dropped, not
emitted as garbage**, so image-only or outlined-text PDFs yield empty
output you can detect, instead of plausible-looking noise.

## Limitations

- No OCR: scanned pages and text converted to vector outlines have no text
  to extract.
- No layout analysis beyond line/word reconstruction: complex multi-column
  layouts may interleave.
- Encrypted files are not decrypted.

## pdftest

`github.com/giraffesyo/pdf/pdftest` builds minimal synthetic PDFs for
tests (in the spirit of `httptest`): document skeletons, composite fonts,
ToUnicode CMaps, Form XObjects, segmented content streams. This package's
own regression suite is built on it — every failure pattern above is
reproduced synthetically, with fuzzing on top.

## License

MIT. Uses [ledongthuc/pdf](https://github.com/ledongthuc/pdf) (BSD-3-Clause).
