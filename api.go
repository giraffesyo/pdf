package pdf

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/giraffesyo/pdf/internal/crypt"
	"github.com/giraffesyo/pdf/internal/safeio"
)

// ErrPasswordRequired reports that an encrypted document could not be
// unlocked with the supplied password.
var ErrPasswordRequired = crypt.ErrPasswordRequired

// WarningCode identifies a recoverable extraction problem.
type WarningCode string

const (
	// WarningMalformedDocument reports malformed document-level structures
	// such as outlines or form-field trees.
	WarningMalformedDocument WarningCode = "malformed_document"
	// WarningMalformedPage reports malformed page content or page extras.
	WarningMalformedPage WarningCode = "malformed_page"
	// WarningStream reports a stream decoding or filter failure.
	WarningStream WarningCode = "stream_error"
	// WarningStreamLimit reports decoded data truncated at a configured limit.
	WarningStreamLimit WarningCode = "stream_limit"
	// WarningWorkLimit reports an operator, glyph, or nesting budget.
	WarningWorkLimit WarningCode = "work_limit"
	// WarningUnsupported reports a recoverable unsupported PDF feature.
	WarningUnsupported WarningCode = "unsupported"
	// WarningOCR reports an external OCR implementation failure.
	WarningOCR WarningCode = "ocr"
)

// Warning describes a condition that may have made an extraction incomplete.
// Page is one-based, or zero for a document-level warning.
type Warning struct {
	Page int
	Code WarningCode
	Err  error
}

func (w Warning) Error() string {
	if w.Page > 0 {
		return fmt.Sprintf("pdf: page %d: %s: %v", w.Page, w.Code, w.Err)
	}
	return fmt.Sprintf("pdf: %s: %v", w.Code, w.Err)
}

func (w Warning) Unwrap() error { return w.Err }

// StrictError is returned when strict extraction encounters a condition that
// permissive extraction would record as a Warning.
type StrictError struct {
	Warning Warning
}

func (e *StrictError) Error() string { return e.Warning.Error() }
func (e *StrictError) Unwrap() error { return e.Warning.Err }

// PageRange selects an inclusive, one-based page range. A Last value of zero
// means the final page.
type PageRange struct {
	First int
	Last  int
}

// Limits bounds work performed on untrusted documents. Zero fields use the
// package defaults.
type Limits struct {
	MaxStreamBytes      int
	MaxOperatorsPerPage int
	MaxGlyphsPerPage    int
	MaxFormDepth        int
}

func (l Limits) normalized() Limits {
	if l.MaxStreamBytes == 0 {
		l.MaxStreamBytes = safeio.MaxStreamBytes
	}
	if l.MaxOperatorsPerPage == 0 {
		l.MaxOperatorsPerPage = maxOpsPerPage
	}
	if l.MaxGlyphsPerPage == 0 {
		l.MaxGlyphsPerPage = maxGlyphsPerPage
	}
	if l.MaxFormDepth == 0 {
		l.MaxFormDepth = maxFormDepth
	}
	return l
}

func (l Limits) validate() error {
	switch {
	case l.MaxStreamBytes < 0:
		return errors.New("pdf: MaxStreamBytes must not be negative")
	case l.MaxOperatorsPerPage < 0:
		return errors.New("pdf: MaxOperatorsPerPage must not be negative")
	case l.MaxGlyphsPerPage < 0:
		return errors.New("pdf: MaxGlyphsPerPage must not be negative")
	case l.MaxFormDepth < 0:
		return errors.New("pdf: MaxFormDepth must not be negative")
	default:
		return nil
	}
}

// LayoutMode selects how Page.Text reconstructs text.
type LayoutMode uint8

const (
	// LayoutPosition groups glyphs using their geometric baselines.
	LayoutPosition LayoutMode = iota
	// LayoutContentOrder preserves the order in which glyphs occur in content
	// streams, inserting line breaks only when the baseline changes.
	LayoutContentOrder
	// LayoutColumns separates wide horizontal gaps into column regions and
	// reads each column from top to bottom.
	LayoutColumns
)

// LayoutOptions controls plain-text reconstruction.
type LayoutOptions struct {
	Mode LayoutMode
}

// Point is a coordinate in unrotated PDF page space.
type Point struct {
	X float64
	Y float64
}

// Rect is an axis-aligned rectangle in unrotated PDF page space.
type Rect struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

// Line describes a directed line segment.
type Line struct {
	Start Point
	End   Point
}

// Quad describes a glyph-region perimeter. The first edge follows the
// baseline direction and the opposite edge returns toward its origin.
type Quad [4]Point

// Metadata contains common document information dictionary and XMP fields.
// Dates retain their original PDF string representation.
type Metadata struct {
	Title        string
	Author       string
	Subject      string
	Keywords     string
	Creator      string
	Producer     string
	CreationDate string
	ModifiedDate string
	Trapped      string
	XMP          []byte
	Custom       map[string]string
}

// Destination identifies a named destination or a destination on a page.
type Destination struct {
	Name string
	Page int
	X    float64
	Y    float64
	Zoom float64
}

// Outline is one bookmark in the document outline tree.
type Outline struct {
	Title       string
	Destination Destination
	Children    []Outline
}

// Annotation contains text-bearing annotation and link information.
type Annotation struct {
	Subtype     string
	Rect        Rect
	Contents    string
	Title       string
	URL         string
	Destination Destination
}

// FormField is one terminal AcroForm field.
type FormField struct {
	Name          string
	AlternateName string
	Type          string
	Value         string
	Page          int
}

// CMapResolver supplies a named predefined CMap that is not built into the
// package. The returned bytes use normal PDF CMap syntax.
type CMapResolver func(name string) ([]byte, error)

// OCRRequest is passed to an external OCR implementation for a page whose
// content streams produced no text. Reader and Size identify the original PDF.
type OCRRequest struct {
	Reader     io.ReaderAt
	Size       int64
	PageNumber int
	Page       Page
}

// OCR extracts text for an image-only page. Implementations may invoke an
// external renderer or OCR engine; the core package remains dependency-free.
type OCR interface {
	ExtractPage(context.Context, OCRRequest) ([]Glyph, error)
}

// OCRFunc adapts a function to OCR.
type OCRFunc func(context.Context, OCRRequest) ([]Glyph, error)

// ExtractPage calls f.
func (f OCRFunc) ExtractPage(ctx context.Context, req OCRRequest) ([]Glyph, error) {
	return f(ctx, req)
}

// Options controls extraction. Its zero value preserves Extract's permissive,
// all-pages behavior.
type Options struct {
	Password string
	Pages    []PageRange
	Strict   bool
	Limits   Limits
	Layout   LayoutOptions

	IgnoreArtifacts    bool
	IncludeMetadata    bool
	IncludeOutlines    bool
	IncludeAnnotations bool
	IncludeFormValues  bool

	// PreserveLigatures keeps the Unicode presentation forms U+FB00–U+FB06
	// (ﬀ ﬁ ﬂ ﬃ ﬄ ﬅ ﬆ) in glyph text. By default they fold to their letter
	// sequences (ff, fi, fl, ffi, ffl, st) — the form searchers, tokenizers
	// and other extractors expect — whether the glyph reached the ligature
	// codepoint through a glyph name (Type1 /Differences, the standard
	// encodings, an embedded font program) or through a ToUnicode CMap.
	PreserveLigatures bool

	CMapResolver CMapResolver
	OCR          OCR
}

func (o Options) validate() error {
	if err := o.Limits.validate(); err != nil {
		return err
	}
	for _, p := range o.Pages {
		if p.First < 1 {
			return errors.New("pdf: page range First must be at least 1")
		}
		if p.Last < 0 || p.Last > 0 && p.Last < p.First {
			return errors.New("pdf: page range Last must be zero or at least First")
		}
	}
	switch o.Layout.Mode {
	case LayoutPosition, LayoutContentOrder, LayoutColumns:
	default:
		return errors.New("pdf: unknown layout mode")
	}
	return nil
}

func selectedPage(n int, ranges []PageRange, total int) bool {
	if len(ranges) == 0 {
		return true
	}
	for _, p := range ranges {
		last := p.Last
		if last == 0 || last > total {
			last = total
		}
		if n >= p.First && n <= last {
			return true
		}
	}
	return false
}
