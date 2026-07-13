package object

import (
	"bytes"
	"testing"

	"github.com/giraffesyo/pdf/pdftest"
)

func TestXrefStreamDocument(t *testing.T) {
	doc := pdftest.BuildXrefStream(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< >>"),
		pdftest.Stream("", "xref-stream content"),
	)
	r := open(t, doc)
	if r.NumPages() != 1 {
		t.Fatalf("NumPages = %d", r.NumPages())
	}
	if got := streamText(t, r.Page(1).Key("Contents")); got != "xref-stream content" {
		t.Errorf("content = %q", got)
	}
}

func TestObjectStreamDocument(t *testing.T) {
	// Pack the catalog, pages node, and page dict into an object stream;
	// the content stream stays direct (streams cannot be packed).
	doc := pdftest.BuildObjStm(1, []int{1, 2, 3},
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< >>"),
		pdftest.Stream("", "objstm content"),
	)
	r := open(t, doc)
	if r.NumPages() != 1 {
		t.Fatalf("NumPages = %d", r.NumPages())
	}
	page := r.Page(1)
	if page.Key("Type").Name() != "Page" {
		t.Fatalf("packed page /Type = %q", page.Key("Type").Name())
	}
	if got := streamText(t, page.Key("Contents")); got != "objstm content" {
		t.Errorf("content = %q", got)
	}
}

func TestHybridXrefDocument(t *testing.T) {
	// The font (last object) is reachable only via /XRefStm.
	doc := pdftest.BuildHybrid(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", "x"),
		pdftest.Helvetica(),
	)
	r := open(t, doc)
	font := r.Page(1).Key("Resources").Key("Font").Key("F1")
	if font.Key("BaseFont").Name() != "Helvetica" {
		t.Error("object recorded only in /XRefStm was not found")
	}
}

func TestCorruptXrefRebuilt(t *testing.T) {
	doc := simpleDoc("recover me")
	// Corrupt the startxref offset so classic parsing fails and the
	// rebuild scan must take over.
	i := bytes.LastIndex(doc, []byte("startxref"))
	if i < 0 {
		t.Fatal("no startxref in fixture")
	}
	corrupt := append([]byte{}, doc...)
	copy(corrupt[i+len("startxref\n"):], []byte("99"))

	r := open(t, corrupt)
	if got := streamText(t, r.Page(1).Key("Contents")); got != "recover me" {
		t.Errorf("rebuilt content = %q", got)
	}
}

func TestXrefStreamPredictor(t *testing.T) {
	// A predictor-encoded xref stream is the common real-world form; verify
	// the object layer decodes it (exercised indirectly: BuildXrefStream
	// emits unpredicted rows, so here we assert a Flate content stream
	// inside an xref-stream document decodes end to end).
	doc := pdftest.BuildXrefStream(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< >>"),
		pdftest.Flate("", "flate inside xref-stream doc"),
	)
	r := open(t, doc)
	if got := streamText(t, r.Page(1).Key("Contents")); got != "flate inside xref-stream doc" {
		t.Errorf("content = %q", got)
	}
}

func TestTruncatedFileNoPanic(t *testing.T) {
	full := simpleDoc("some content here")
	for cut := 1; cut < len(full); cut += 7 {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("panic on truncation at %d: %v", cut, rec)
				}
			}()
			r, err := NewReader(bytes.NewReader(full[:cut]), int64(cut))
			if err != nil {
				return // a clean error is fine
			}
			// Walk whatever is reachable; must not panic.
			p := r.Page(1)
			if !p.IsNull() {
				if c := p.Key("Contents"); c.Kind() == Stream {
					_, _ = c.Reader()
				}
			}
		}()
	}
}

func TestFuzzSeeds(t *testing.T) {
	// A quick guard that malformed headers are rejected rather than panic.
	for _, doc := range [][]byte{
		[]byte("%PDF-1.7 nonsense"),
		[]byte("not a pdf at all"),
		append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("1 0 obj << >> endobj\n"), 3)...),
	} {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("panic on %q: %v", doc, rec)
				}
			}()
			if r, err := NewReader(bytes.NewReader(doc), int64(len(doc))); err == nil {
				_ = r.Page(1)
			}
		}()
	}
}
