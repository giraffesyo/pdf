// Package benchmarks compares github.com/giraffesyo/pdf against other
// pure-Go PDF text extractors over identical synthetic corpora. Run:
//
//	go test -bench . -benchmem ./...
//
// TestCompetitorComparison logs which libraries handle each corpus (some
// panic on inputs this package supports, such as xref streams or
// encryption); the benchmarks measure only the corpora a library handles.
package benchmarks

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	gpdf "github.com/giraffesyo/pdf"
	"github.com/giraffesyo/pdf/pdftest"
	ldpdf "github.com/ledongthuc/pdf"
	rscpdf "rsc.io/pdf"
)

// corpus is one synthetic document with the text it should yield.
type corpus struct {
	name string
	doc  []byte
	want string
}

func corpora() []corpus {
	res := "<< /Font << /F1 5 0 R >> >>"
	body := func(s string) string {
		return "BT /F1 12 Tf 72 700 Td (" + s + ") Tj ET"
	}
	return []corpus{
		{
			name: "simple",
			doc: pdftest.Build(1,
				pdftest.Catalog(2), pdftest.Pages(3),
				pdftest.Page(2, 4, res), pdftest.Stream("", body("simple text")),
				pdftest.Helvetica()),
			want: "simple text",
		},
		{
			name: "form_xobject",
			doc: func() []byte {
				form := pdftest.Stream(
					"/Type /XObject /Subtype /Form /BBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >>",
					body("inside form"))
				return pdftest.Build(1,
					pdftest.Catalog(2), pdftest.Pages(3),
					pdftest.Page(2, 4, "<< /XObject << /X1 6 0 R >> >>"),
					pdftest.Stream("", "q /X1 Do Q"), pdftest.Helvetica(), form)
			}(),
			want: "inside form",
		},
		{
			name: "flate_content",
			doc: pdftest.Build(1,
				pdftest.Catalog(2), pdftest.Pages(3),
				pdftest.Page(2, 4, res), pdftest.Flate("", body("flate content")),
				pdftest.Helvetica()),
			want: "flate content",
		},
		{
			name: "xref_stream",
			doc: pdftest.BuildXrefStream(1,
				pdftest.Catalog(2), pdftest.Pages(3),
				pdftest.Page(2, 4, res), pdftest.Stream("", body("xref stream")),
				pdftest.Helvetica()),
			want: "xref stream",
		},
		{
			name: "object_stream",
			doc: pdftest.BuildObjStm(1, []int{1, 2, 3, 5},
				pdftest.Catalog(2), pdftest.Pages(3),
				pdftest.Page(2, 4, res), pdftest.Stream("", body("object stream")),
				pdftest.Helvetica()),
			want: "object stream",
		},
		{
			name: "encrypted_rc4",
			doc: pdftest.BuildEncrypted(1, pdftest.EncryptSpec{R: 4},
				pdftest.Catalog(2), pdftest.Pages(3),
				pdftest.Page(2, 4, res), pdftest.Stream("", body("encrypted text")),
				pdftest.Helvetica()),
			want: "encrypted text",
		},
	}
}

// extractor names the libraries under test and how to drive each. Each
// returns extracted text or an error; panics are converted to errors.
type extractor struct {
	name string
	run  func(doc []byte) (string, error)
}

func extractors() []extractor {
	return []extractor{
		{"giraffesyo", runGiraffesyo},
		{"ledongthuc", runLedongthuc},
		{"rsc.io", runRsc},
	}
}

func runGiraffesyo(doc []byte) (string, error) {
	d, err := gpdf.Extract(context.Background(), bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return "", err
	}
	return d.Text(), nil
}

func runLedongthuc(doc []byte) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	r, err := ldpdf.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, terr := p.GetPlainText(nil)
		if terr != nil {
			return "", terr
		}
		b.WriteString(text)
	}
	return b.String(), nil
}

func runRsc(doc []byte) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	r, err := rscpdf.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		for _, t := range p.Content().Text {
			b.WriteString(t.S)
		}
	}
	return b.String(), nil
}

// TestCompetitorComparison logs a support matrix: whether each library
// extracts the expected text (ok), returns wrong text (MISMATCH), or fails
// (a trimmed error). It never fails the build — it documents coverage.
func TestCompetitorComparison(t *testing.T) {
	exs := extractors()
	t.Logf("%-16s %-12s %s", "corpus", "extractor", "result")
	for _, c := range corpora() {
		for _, e := range exs {
			got, err := e.run(c.doc)
			var result string
			switch {
			case err != nil:
				result = "ERR: " + trim(err.Error())
			case strings.TrimSpace(got) == c.want:
				result = "ok"
			default:
				result = fmt.Sprintf("MISMATCH %q", trim(got))
			}
			t.Logf("%-16s %-12s %s", c.name, e.name, result)
		}
	}
}

func trim(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 50 {
		return s[:50] + "…"
	}
	return s
}

// BenchmarkExtract measures each library over each corpus it handles.
func BenchmarkExtract(b *testing.B) {
	for _, e := range extractors() {
		for _, c := range corpora() {
			// Skip corpora a library cannot handle, so the benchmark
			// measures successful extraction only.
			if got, err := e.run(c.doc); err != nil || strings.TrimSpace(got) != c.want {
				continue
			}
			b.Run(e.name+"/"+c.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := e.run(c.doc); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
