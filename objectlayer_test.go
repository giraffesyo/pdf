package pdf

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/giraffesyo/pdf/pdftest"
)

// End-to-end Extract tests over the document structures the native object
// layer added: xref streams, object streams, hybrid references, Flate
// content, and encrypted files.

func extractText(t *testing.T, doc []byte) string {
	t.Helper()
	d, err := Extract(context.Background(), bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return d.Text()
}

func TestExtractXrefStream(t *testing.T) {
	doc := pdftest.BuildXrefStream(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", "BT /F1 12 Tf 72 700 Td (xref stream page) Tj ET"),
		pdftest.Helvetica(),
	)
	if got := extractText(t, doc); got != "xref stream page" {
		t.Errorf("text = %q", got)
	}
}

func TestExtractObjectStream(t *testing.T) {
	doc := pdftest.BuildObjStm(1, []int{1, 2, 3, 5},
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", "BT /F1 12 Tf 72 700 Td (packed in objstm) Tj ET"),
		pdftest.Helvetica(),
	)
	if got := extractText(t, doc); got != "packed in objstm" {
		t.Errorf("text = %q", got)
	}
}

func TestExtractHybridXref(t *testing.T) {
	doc := pdftest.BuildHybrid(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", "BT /F1 12 Tf 72 700 Td (hybrid xref) Tj ET"),
		pdftest.Helvetica(),
	)
	if got := extractText(t, doc); got != "hybrid xref" {
		t.Errorf("text = %q", got)
	}
}

func TestExtractFlateContent(t *testing.T) {
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Flate("", "BT /F1 12 Tf 72 700 Td (compressed content) Tj ET"),
		pdftest.Helvetica(),
	)
	if got := extractText(t, doc); got != "compressed content" {
		t.Errorf("text = %q", got)
	}
}

func TestExtractEncrypted(t *testing.T) {
	specs := map[string]pdftest.EncryptSpec{
		"RC4-R2": {R: 2},
		"RC4-R3": {R: 3},
		"RC4-R4": {R: 4},
		"AESV2":  {R: 4, AES: true},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			doc := pdftest.BuildEncrypted(1, spec,
				pdftest.Catalog(2),
				pdftest.Pages(3),
				pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
				pdftest.Stream("", "BT /F1 12 Tf 72 700 Td (decrypted text) Tj ET"),
				pdftest.Helvetica(),
			)
			if got := extractText(t, doc); got != "decrypted text" {
				t.Errorf("%s: text = %q, want %q", name, got, "decrypted text")
			}
		})
	}
}

func TestExtractEncryptedPasswordRequired(t *testing.T) {
	doc := pdftest.BuildEncrypted(1, pdftest.EncryptSpec{R: 4},
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< >>"),
		pdftest.Stream("", "BT (x) Tj ET"),
	)
	corrupt := append([]byte{}, doc...)
	if k := bytes.Index(corrupt, []byte("/U <")); k >= 0 {
		corrupt[k+4] = 'A' // break the /U hash so no empty password works
	}
	if _, err := Extract(context.Background(), bytes.NewReader(corrupt), int64(len(corrupt))); err == nil {
		t.Error("expected an error for a password-protected document")
	}
}

// TestPageTreeWithoutType: page-tree nodes missing /Type are read by
// their shape, as poppler reads them — a node with /Kids is intermediate,
// any other dictionary a page.
func TestPageTreeWithoutType(t *testing.T) {
	doc := pdftest.Build(1,
		pdftest.Catalog(2),
		"<< /Kids [3 0 R] /Count 1 >>",
		"<< /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		pdftest.Stream("", "BT /F1 12 Tf 72 700 Td (untyped tree) Tj ET"),
		pdftest.Helvetica(),
	)
	if got := extractText(t, doc); got != "untyped tree" {
		t.Errorf("text = %q", got)
	}
}

// TestRepairMisplacedOffsets: a cross-reference table that parses but
// points at the wrong objects — here the entries for the page tree and
// its page are swapped — is rebuilt from the objects in the file. The
// repair is reported, so strict mode stops.
func TestRepairMisplacedOffsets(t *testing.T) {
	doc := simpleDoc("BT /F1 12 Tf 72 700 Td (found anyway) Tj ET")
	table := bytes.Index(doc, []byte("xref\n"))
	entry := func(num int) []byte { // the 20-byte entry for object num
		start := table + len("xref\n0 7\n") + 20*num
		return doc[start : start+20]
	}
	two, three := bytes.Clone(entry(2)), bytes.Clone(entry(3))
	copy(entry(2), three)
	copy(entry(3), two)

	d, err := Extract(context.Background(), bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got := d.Text(); got != "found anyway" {
		t.Errorf("text = %q", got)
	}
	if len(d.Warnings) != 1 || d.Warnings[0].Code != WarningMalformedDocument {
		t.Errorf("warnings = %v, want one malformed_document", d.Warnings)
	}
	var strict *StrictError
	if _, err := extractOptions(t, doc, Options{Strict: true}); !errors.As(err, &strict) {
		t.Errorf("strict error = %v, want *StrictError", err)
	}
}

// TestRebuildIndexesObjectStreams: when a PDF 1.5 file's cross-reference
// stream is lost, the rebuild must find the objects packed in its object
// streams — here the catalog, page tree, page, and font.
func TestRebuildIndexesObjectStreams(t *testing.T) {
	doc := pdftest.BuildObjStm(1, []int{1, 2, 3, 5},
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", "BT /F1 12 Tf 72 700 Td (packed and damaged) Tj ET"),
		pdftest.Helvetica(),
	)
	at := bytes.LastIndex(doc, []byte("startxref"))
	doc = append(doc[:at:at], []byte("startxref\n999999999\n%%EOF\n")...)
	if got := extractText(t, doc); got != "packed and damaged" {
		t.Errorf("text = %q", got)
	}
}
