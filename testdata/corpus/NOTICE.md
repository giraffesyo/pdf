# Regression corpus

Real PDFs produced by real generators, used by `TestCorpusGolden` (which
pins `Document.Text` in `golden/`) and `BenchmarkCorpus`. Each file is
redistributable under the license noted. Most come from other projects
and public agencies; those marked original were generated here, from the
sources in `generate/`, to cover producers no redistributable file did.

| File | Pages | Embedded fonts | Source | License |
|---|---:|---|---|---|
| `runc-security-audit.pdf` | 16 | TrueType (`FontFile2`) | [opencontainers/runc](https://github.com/opencontainers/runc) `docs/Security-Audit.pdf` | Apache-2.0 |
| `alice-fr.pdf` | 7 | TrueType, CFF (`FontFile3/Type1C`) | [gohugoio/hugo-learn-theme](https://github.com/matcornic/hugo-theme-learn) exampleSite attachments, `Carroll_AliceAuPaysDesMerveilles.pdf` (public-domain text) | MIT |
| `adivorciar.pdf` | 4 | TrueType | hugo-learn-theme exampleSite attachments, `adivorciarsetoca00cape.pdf` (1932 play, public domain; OCR text layer) | MIT |
| `markitdown-test.pdf` | 1 | Type1 (`FontFile`) ×4 | [microsoft/markitdown](https://github.com/microsoft/markitdown) `packages/markitdown/tests/test_files/test.pdf` | MIT, © Microsoft Corporation |
| `downmark-synthetic.pdf` | 1 | none (Form XObject + ToUnicode) | [giraffesyo/downmark](https://github.com/giraffesyo/downmark) `testdata/synthetic.pdf` | MIT |
| `cisa-indesign.pdf` | 2 | Type1C, TrueType | CISA, [Physical Security Considerations for Temporary Facilities](https://www.cisa.gov/sites/default/files/publications/CISA-Fact-Sheet-Temporary-Facilities-20220817-508.pdf) (Adobe InDesign 17.3; incremental updates, hybrid xref, 19 KB page dictionaries, fake-bold headings) | Public domain (U.S. Government work, 17 U.S.C. § 105) |
| `cisa-pdfmaker.pdf` | 1 | TrueType, CID TrueType | CISA, [CISA Gateway fact sheet](https://www.cisa.gov/sites/default/files/2024-09/CISA%20Fact%20Sheet%20-%20CISA%20Gateway.20SEP2024%20508%20compliant.pdf) (Acrobat PDFMaker 24 for Word) | Public domain (U.S. Government work) |
| `irs-fw9.pdf` | 6 | Type1C | IRS, [Form W-9 (Rev. March 2024)](https://www.irs.gov/pub/irs-pdf/fw9.pdf) (Adobe LiveCycle Designer; XFA and AcroForm) | Public domain (U.S. Government work) |
| `chrome-print.pdf` | 2 | CID TrueType ×7 | Original to this project: `generate/page.html` printed by headless Chrome 153 (Skia/PDF) | MIT |
| `chrome-rtl.pdf` | 1 | CID TrueType | Original: `generate/rtl.html` printed by headless Chrome 153 (Arabic and Hebrew mixed with numbers and Latin) | MIT |
| `quartz-coretext.pdf` | 1 | TrueType (MacRoman, no ToUnicode) | Original: `generate/page.html` set by `generate/quartz.swift` through CoreText into a macOS 26.5 Quartz PDFContext | MIT |
| `ghostscript-ps2pdf.pdf` | 2 | standard 14 (not embedded) | Original: `generate/gs.ps` distilled by Ghostscript 10.07 `pdfwrite` (widthshow, ashow, rotated text, Symbol) | MIT |
| `ghostscript-rewrite.pdf` | 2 | CID TrueType ×7 | Original: `chrome-print.pdf` rewritten by Ghostscript 10.07 `pdfwrite -dPDFSETTINGS=/ebook` | MIT |

Embedded font subsets remain under their vendors' terms, as in any
distributed PDF. The generated files are reproduced from `generate/`:

    chrome --headless --no-pdf-header-footer --print-to-pdf=chrome-print.pdf generate/page.html
    chrome --headless --no-pdf-header-footer --print-to-pdf=chrome-rtl.pdf generate/rtl.html
    (cd generate && swift quartz.swift)
    gs -q -dNOPAUSE -dBATCH -sDEVICE=pdfwrite -sOutputFile=ghostscript-ps2pdf.pdf generate/gs.ps
    gs -q -dNOPAUSE -dBATCH -sDEVICE=pdfwrite -dPDFSETTINGS=/ebook -sOutputFile=ghostscript-rewrite.pdf chrome-print.pdf

Regenerated files differ byte for byte (producer versions, font subset
tags), so goldens must be re-accepted after regenerating.

Files that cannot be redistributed (for example arXiv papers) can be
benchmarked locally by pointing `PDF_CORPUS_DIR` at a directory of PDFs.

To accept an intentional output change:

    go test -run TestCorpusGolden -update
