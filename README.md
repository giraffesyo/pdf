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
glyphs := page.Glyphs          // text, origin, advance, direction, size; Baseline(), Quad()
```

`Page.Text` reconstructs reading order from direction-aware glyph baselines,
and word boundaries are recovered from glyph gaps and font metrics — so PDFs
that encode no space characters at all still come out readable. Rotated and
vertical runs retain their reading direction. `Page.TextIn` and
`Page.GlyphsIn` extract a rectangular region.

## Extraction options and diagnostics

`Extract` is the permissive, all-pages convenience API. `ExtractWithOptions`
adds passwords, page ranges, strict mode, configurable resource limits,
artifact filtering, layout strategies, named CMap resolution, document
extras, page images, and an optional OCR handoff:

```go
doc, err := pdf.ExtractWithOptions(ctx, readerAt, size, pdf.Options{
    Password: "secret",
    Pages:    []pdf.PageRange{{First: 2, Last: 5}},
    Strict:   true,
    Layout:   pdf.LayoutOptions{Mode: pdf.LayoutColumns},

    IgnoreArtifacts:    true,
    IncludeMetadata:    true,
    IncludeOutlines:    true,
    IncludeAnnotations: true,
    IncludeFormValues:  true,
    IncludeImages:      true,
})
```

Ligature presentation forms (`ﬁ`, `ﬂ`, `ﬀ`, `ﬃ`, `ﬄ`, `ﬅ`, `ﬆ`) fold to their
letter sequences by default, as `pdftotext` and pdf.js do, so extracted text
stays searchable; set `PreserveLigatures: true` to keep the raw codepoints.

Text painted twice at the same place — fake bold, drop shadows,
fill-then-stroke headings — is read once, as `pdftotext` and PDFBox read
it; `LayoutOptions.KeepDuplicateGlyphs` keeps the repeats, and
`Page.Glyphs` reports every painted glyph either way. Text drawn over
other text on the same baseline, such as an overlay or stamp, comes out
on a line of its own instead of interleaved letter by letter.

Permissive extraction retains recoverable output and records conditions that
may have made it incomplete in `Document.Warnings` and `Page.Warnings`.
Strict mode returns the partial document with a `*pdf.StrictError` at the first
such condition. `pdf.ErrPasswordRequired` is available for `errors.Is`.

Text that annotations paint over the page — filled form fields, free-text
comments, stamps — is part of the page's text, as a viewer shows it and as
`pdftotext` extracts it; text and choice fields are drawn from their values
when the form asks viewers to regenerate appearances. Set
`IgnoreAnnotationAppearances: true` for the page content alone.

For large files, `ExtractPages` invokes a callback one page at a time and does
not retain page glyphs in the returned document:

```go
summary, err := pdf.ExtractPages(ctx, readerAt, size, opts, func(page pdf.Page) error {
    consume(page)
    return nil
})
```

## Images and OCR

Scanned pages have no text to extract; they have an image. With
`IncludeImages`, every image a page paints — image XObjects and inline
images, through Form XObjects — is reported in `Page.Images` with where it
landed on the page and its data in the most directly usable form:

```go
for _, im := range page.Images {
    im.Bounds()            // page-space rectangle; Quad() for rotated placements
    im.ToPage(x, y)        // image pixel → page point, for positioning OCR output
    im.Filter              // "DCTDecode": Data is a JPEG file; "JPXDecode", "CCITTFaxDecode",
                           // "JBIG2Decode": still encoded; "": unpacked samples
    img, err := im.Decode() // image.Image for raw samples, JPEG, CCITT G3/G4, JBIG2
}
```

`Decode` handles unpacked samples in the Device, Cal, ICCBased, Indexed
and single-colorant Separation/DeviceN spaces and image masks at 1–16 bits
per component, DCTDecode through `image/jpeg`, and — natively, from the
ITU-T specifications — CCITT Group 3/4 fax and JBIG2 (generic, symbol and
text regions with arithmetic coding, which is what scanner pipelines
emit). JPXDecode and the rarer JBIG2 features (Huffman tables,
refinement, halftones) return `errors.ErrUnsupported`; their `Data` is
still handed over for an external decoder. `Limits.MaxImagePixels` bounds
what `Decode` will allocate.

The core remains dependency-free: no OCR engine is bundled. Applications
implement `pdf.OCR`, which receives the page's glyphs and images and
returns positioned glyphs that join the page's text — `Page.Text`,
`TextIn` and the layout modes then treat OCR words like typeset ones.
`Options.OCRPolicy` selects the pages: those with no text (the default),
those that paint an image (mixed typeset text and scanned figures), or
all of them. Where none of the three is the rule a document needs —
a form with a typed header over a scanned body has text, so the default
passes it by — `Options.OCRSelect` takes a predicate instead:

```go
OCRSelect: func(page pdf.Page) bool {
    return len(page.Glyphs) < 100 && page.ImageCount > 0
},
```

`Page.OCRGlyphs` counts the glyphs that came from OCR. Without
`IncludeImages`, image data is read only for the pages the policy selects
and is not retained afterwards; `Page.ImageCount` is reported either way,
so a page with no text can be told apart from a page with no images —
the first is a scan an OCR engine can read, the second has had its text
converted to vector outlines and needs a renderer. OCR runs concurrently
across pages under `Options.Concurrency`, so implementations — and an
`OCRSelect` predicate — are expected to be safe for concurrent use.

[`ocr/tesseract`](ocr/tesseract) is a reference implementation that shells
out to the Tesseract command-line program (which must be installed), feeds
it each image — JPEG as is, everything else through `Decode` as PNG — and
maps its word boxes onto the page:

```go
import "github.com/giraffesyo/pdf/ocr/tesseract"

doc, err := pdf.ExtractWithOptions(ctx, readerAt, size, pdf.Options{
    OCR: &tesseract.Engine{Languages: []string{"eng"}},
})
```

The same shape fits a cloud OCR API or a vision model: decode or forward
`Image.Data`, and place the results with `Image.ToPage`. The request also
carries the original `io.ReaderAt` for implementations that render the
whole page with an external renderer — the only route for text that was
converted to vector outlines.

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
| Inline images (BI/ID/EI) | derails the lexer | lexed exactly, reported as images |

Everything is implemented here, from ISO 32000 directly, with **no
external dependencies**: the object layer (cross-reference tables and
streams, object streams, hybrid references, stream filters, and
standard-handler decryption), the standard font encodings and Adobe Glyph
List, and everything from the content stream down — lexer, interpreter,
text state machine, CTM/Form-XObject handling, variable-width ToUnicode and
encoding CMaps, horizontal/vertical CID metrics, embedded TrueType/CFF/Type1
fallbacks, and tagged-PDF `/ActualText`.

Undecodable glyphs (no ToUnicode, no standard encoding) are **dropped, not
emitted as garbage**, so image-only or outlined-text PDFs yield empty
output you can detect, instead of plausible-looking noise.

Ligature presentation forms (`ﬁ`, `ﬂ`, `ﬀ`, `ﬃ`, `ﬄ`, `ﬅ`, `ﬆ`) — which
TeX-generated PDFs reach through `/Differences` glyph names — fold to their
letter sequences, as `pdftotext` and pdf.js do, so extracted text stays
searchable.

## Limitations

- No bundled OCR: scanned pages and text converted to vector outlines have
  no text to extract unless the caller supplies an `OCR` implementation
  (see [Images and OCR](#images-and-ocr)). The package does not rasterize
  pages, so outlined text needs an external renderer.
- Layout reconstruction is heuristic. Direction-aware, content-order, and
  column modes are available, but highly irregular tables may still require
  application-specific analysis of glyph quads.
- `Identity-H` and `Identity-V` CMaps are built in. Other named predefined
  CMaps are loaded through `Options.CMapResolver`; embedded CMap streams and
  `usecmap` inheritance are parsed natively.
- `Image.Decode` does not decode JPXDecode (JPEG 2000) or the Huffman,
  refinement and halftone parts of JBIG2; their encoded data is still
  reported. Soft masks and colour-key masking are not applied.

## Benchmarks

The `benchmarks/` directory is a separate module (so its dependencies stay
out of this one) that compares extraction against
[ledongthuc/pdf](https://github.com/ledongthuc/pdf) and
[rsc.io/pdf](https://pkg.go.dev/rsc.io/pdf) over identical synthetic
corpora. The corpus checks both whether the expected text is recovered and
whether extraction completes safely. A comparator that returns incorrect
text, errors, or panics is excluded from the performance run for that
fixture.

Results below were measured on 2026-09-23 at commit `f238684`, using Go
`1.27.0` on macOS `26.5.1` (`darwin/arm64`, Apple M5 Pro). Values are the
median of five runs of
`go test -bench . -benchmem -run '^$' -count=5 ./...`; rerun on your own
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
| simple | 9,358 | 14,614 |
| Form XObject | 11,007 | incorrect text |
| Flate content | 10,450 | 19,881 |
| xref stream | 9,736 | 13,759 |
| object stream | 10,545 | 22,254 |
| RC4-encrypted | 30,332 | unsupported |

Memory (`B/op`) and allocations (`allocs/op`):

| Corpus | this package B/op | ledongthuc/pdf B/op | this package allocs/op | ledongthuc/pdf allocs/op |
|---|---:|---:|---:|---:|
| simple | 41,472 | 62,960 | 172 | 368 |
| Form XObject | 44,728 | incorrect text | 218 | incorrect text |
| Flate content | 43,272 | 107,888 | 184 | 386 |
| xref stream | 42,472 | 62,904 | 176 | 366 |
| object stream | 44,856 | 100,360 | 201 | 542 |
| RC4-encrypted | 72,768 | unsupported | 333 | unsupported |

To reproduce the support matrix and performance measurements:

```
cd benchmarks
go test -run TestCompetitorComparison -v ./...   # support matrix
go test -bench . -benchmem -run '^$' -count=5 ./... # results above (medians)
```

The corpus is synthetic and deliberately small: it measures parser and
extractor behavior, not throughput on a representative production document
set. It does show that this package recovers the expected text from every
fixture, including Form XObjects, xref streams, object streams, and
encrypted files.

## Concurrency

`Extract` reconstructs the pages of a long document in parallel, on
cloned readers that share the cross-reference table and decryption key
but resolve objects independently, and `Document.Text` reconstructs their
text the same way. Output does not depend on it: pages, glyphs, and
warnings come out in the same order either way. `Options.Concurrency`
sets the number of workers — zero, the default, picks one per processor
up to a cap and falls back to sequential extraction for short documents,
and one extracts sequentially. Strict extraction and `CMapResolver`
always run sequentially: strict mode stops at the first warning, and a
resolver need not be safe for concurrent use. `OCR` implementations and
an `OCRSelect` predicate are the exception — they are called
concurrently, since OCR dominates the cost of a scanned document — so set
`Concurrency` to one for an engine that cannot be shared.

## Regression corpus

`testdata/corpus/` holds real documents — LaTeX, Word, and scanning-pipeline
output with embedded Type1, TrueType, and CFF fonts over multiple pages —
under redistributable licenses (see `testdata/corpus/NOTICE.md`).
`TestCorpusGolden` pins `Document.Text` for each file in
`testdata/corpus/golden/`, so a decoding or layout change is a reviewable
diff; accept an intentional change with
`go test -run TestCorpusGolden -update`. `TestCorpusAllocationBudget`
pins bytes and allocations per extraction for each file — stable across
machines, unlike timing — in `testdata/corpus/golden/budget.json`, and
fails when a file exceeds its budget by more than 15%
(`go test -run TestCorpusAllocationBudget -update` accepts a deliberate
change). `BenchmarkCorpus` measures `Extract` plus `Document.Text` over the
same files, and CI fails a pull request that regresses it against its base. Documents that cannot be
committed can be benchmarked locally with
`PDF_CORPUS_DIR=/path/to/pdfs go test -bench '^BenchmarkCorpus$' -run '^$' .`.

## pdftest

`github.com/giraffesyo/pdf/pdftest` builds minimal synthetic PDFs for
tests (in the spirit of `httptest`): document skeletons, composite fonts,
ToUnicode CMaps, Form XObjects, segmented content streams. This package's
own regression suite is built on it — every failure pattern above is
reproduced synthetically, with fuzzing on top.

## License

MIT. Embeds the [Adobe Glyph List](https://github.com/adobe-type-tools/agl-aglfn)
(Apache-2.0) for glyph-name decoding.
