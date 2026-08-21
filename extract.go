// Package pdf extracts text and direction-aware glyph geometry from PDF files.
//
// Extract returns per-page glyph quads and baselines; Page.Text reconstructs
// plain text by clustering direction-compatible glyphs into lines and
// inserting word spacing derived from glyph gaps and font metrics.
//
// The package is pure Go with no external dependencies. It implements the
// PDF object layer (cross-reference tables and streams, object streams,
// stream filters, and password-aware standard-handler decryption) natively per ISO
// 32000, and on top of it a content-stream lexer, interpreter, and font
// decoder tuned for real-world files: it descends into Form XObjects
// (where e.g. Google Docs exports keep their text), honors /ToUnicode
// even alongside an /Encoding dictionary, tracks positions through every
// text operator, honors tagged-PDF replacement text, never loops on tokens
// straddling /Contents array segments, and reports bounded partial extraction
// caused by decompression bombs, stalled filter chains, or work limits.
package pdf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/giraffesyo/pdf/internal/object"
	"github.com/giraffesyo/pdf/internal/safeio"
)

// Glyph is one decoded glyph (or glyph cluster) with its position in
// unrotated page space.
//
// The geometry is stored compactly — a document's glyph slice is its
// largest allocation — and Baseline and Quad derive the full shapes.
// A Glyph carrying only Text, X, Y, Advance, and Size (for example one
// supplied by an OCR hook) is treated as upright horizontal text.
type Glyph struct {
	Text    string  // decoded text, non-empty
	X, Y    float64 // origin: where the baseline starts
	Advance float64 // displacement along the baseline in page units
	Size    float64 // effective font size

	// Direction is the unit direction the baseline runs in. Ascent and
	// Descent are the vectors from the baseline to the far and near
	// edges of the glyph box, preserving rotation, skew, and vertical
	// writing: horizontal text has Ascent spanning the font size along
	// the text-space y axis and a zero Descent; vertical text reaches
	// half the font size to either side of its baseline.
	Direction Point
	Ascent    Point
	Descent   Point
}

// Baseline returns the segment the glyph occupies along its writing
// direction: from the origin, |Advance| page units along Direction.
func (g Glyph) Baseline() Line {
	dir := g.direction()
	length := math.Abs(g.Advance)
	start := Point{X: g.X, Y: g.Y}
	return Line{Start: start, End: Point{X: start.X + dir.X*length, Y: start.Y + dir.Y*length}}
}

// Quad returns the glyph's region perimeter: the baseline offset by
// Descent, then the same edge returning along Ascent.
func (g Glyph) Quad() Quad {
	b := g.Baseline()
	ascent, descent := g.Ascent, g.Descent
	if ascent == (Point{}) && descent == (Point{}) {
		ascent = Point{Y: g.Size} // upright text: the box rises from the baseline
	}
	return Quad{
		{X: b.Start.X + descent.X, Y: b.Start.Y + descent.Y},
		{X: b.End.X + descent.X, Y: b.End.Y + descent.Y},
		{X: b.End.X + ascent.X, Y: b.End.Y + ascent.Y},
		{X: b.Start.X + ascent.X, Y: b.Start.Y + ascent.Y},
	}
}

// direction is Direction normalized, or the page x axis for glyphs that
// carry none.
func (g Glyph) direction() Point {
	if length := math.Hypot(g.Direction.X, g.Direction.Y); length > 1e-9 {
		return Point{X: g.Direction.X / length, Y: g.Direction.Y / length}
	}
	return Point{X: 1}
}

// Page holds the glyphs of one page in content order.
type Page struct {
	Number      int
	Glyphs      []Glyph
	MediaBox    Rect
	CropBox     Rect
	Rotation    int
	Annotations []Annotation
	Warnings    []Warning

	layout LayoutOptions
}

// Document is the extraction result for a whole file.
type Document struct {
	PageCount  int
	Pages      []Page
	Metadata   Metadata
	Outlines   []Outline
	FormFields []FormField
	Warnings   []Warning
}

// Extract parses the PDF in r and extracts every page's glyphs. Malformed
// pages yield whatever was decoded before the failure; a malformed file
// returns an error. The context is checked between pages and periodically
// while processing page operators and glyphs.
func Extract(ctx context.Context, r io.ReaderAt, size int64) (*Document, error) {
	return ExtractWithOptions(ctx, r, size, Options{})
}

// ExtractWithOptions extracts selected pages and document extras according to
// opts. In strict mode it returns the partial document together with the first
// warning as a *StrictError.
func ExtractWithOptions(ctx context.Context, r io.ReaderAt, size int64, opts Options) (*Document, error) {
	return extractDocument(ctx, r, size, opts, nil, true)
}

// ExtractPages extracts pages one at a time and calls yield without retaining
// them in the returned Document. Document metadata, page count, form fields,
// outlines, and accumulated warnings are still returned.
func ExtractPages(
	ctx context.Context,
	r io.ReaderAt,
	size int64,
	opts Options,
	yield func(Page) error,
) (*Document, error) {
	if yield == nil {
		return nil, errors.New("pdf: nil page callback")
	}
	return extractDocument(ctx, r, size, opts, yield, false)
}

func extractDocument(
	ctx context.Context,
	r io.ReaderAt,
	size int64,
	opts Options,
	yield func(Page) error,
	retainPages bool,
) (*Document, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	limits := opts.Limits.normalized()
	reader, err := object.NewReaderWithPassword(r, size, []byte(opts.Password))
	if err != nil {
		return nil, err
	}
	pageNodes, err := listPages(reader)
	if err != nil {
		return nil, err
	}
	doc := &Document{PageCount: len(pageNodes)}
	var pageNumbers map[int]int
	if opts.IncludeOutlines || opts.IncludeAnnotations || opts.IncludeFormValues {
		pageNumbers = make(map[int]int, len(pageNodes))
		for i, pageNode := range pageNodes {
			if number, ok := pageNode.ObjectNumber(); ok {
				pageNumbers[number] = i + 1
			}
		}
	}
	if retainPages {
		doc.Pages = make([]Page, 0, len(pageNodes))
	}
	addDocumentWarning := func(code WarningCode, warningErr error) error {
		warning := Warning{Code: code, Err: warningErr}
		doc.Warnings = append(doc.Warnings, warning)
		if opts.Strict {
			return &StrictError{Warning: warning}
		}
		return nil
	}
	if opts.IncludeMetadata {
		doc.Metadata, err = extractMetadata(reader, limits.MaxStreamBytes)
		if err != nil {
			code := WarningStream
			if errors.Is(err, safeio.ErrLimitExceeded) {
				code = WarningStreamLimit
			}
			if strictErr := addDocumentWarning(code, err); strictErr != nil {
				return doc, strictErr
			}
		}
	}
	if opts.IncludeOutlines {
		doc.Outlines, err = extractOutlines(reader, pageNumbers)
		if err != nil {
			if strictErr := addDocumentWarning(WarningMalformedDocument, err); strictErr != nil {
				return doc, strictErr
			}
		}
	}
	if opts.IncludeFormValues {
		doc.FormFields, err = extractFormFields(reader, pageNumbers)
		if err != nil {
			if strictErr := addDocumentWarning(WarningMalformedDocument, err); strictErr != nil {
				return doc, strictErr
			}
		}
	}
	fontCache := map[int]*fontInfo{}
	glyphs := &glyphBuffer{} // chunks recycled across pages
	for i, pageNode := range pageNodes {
		pageNumber := i + 1
		if !selectedPage(pageNumber, opts.Pages, len(pageNodes)) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return doc, err
		}
		page := newPage(pageNode, pageNumber, opts.Layout)
		if opts.IncludeAnnotations {
			page.Annotations, err = extractAnnotations(pageNode, pageNumbers)
			if err != nil {
				warning := Warning{Page: pageNumber, Code: WarningMalformedPage, Err: err}
				page.Warnings = append(page.Warnings, warning)
				doc.Warnings = append(doc.Warnings, warning)
				if opts.Strict {
					if retainPages {
						doc.Pages = append(doc.Pages, page)
					}
					return doc, &StrictError{Warning: warning}
				}
			}
		}
		w := &walker{
			ctx:             ctx,
			page:            pageNumber,
			strict:          opts.Strict,
			limits:          limits,
			resolver:        opts.CMapResolver,
			ignoreArtifacts: opts.IgnoreArtifacts,
			foldLigatures:   !opts.PreserveLigatures,
			fonts:           fontCache,
			glyphs:          glyphs,
		}
		res := object.Inherited(pageNode, "Resources")
		err = w.walkStream(pageNode.Key("Contents"), res, gstate{ctm: identity, hscale: 1})
		if errors.Is(err, errStopPage) {
			err = nil
		}
		page.Glyphs = glyphs.take()
		page.Warnings = append(page.Warnings, w.warnings...)
		doc.Warnings = append(doc.Warnings, w.warnings...)

		if err == nil && opts.OCR != nil && len(page.Glyphs) == 0 {
			page.Glyphs, err = opts.OCR.ExtractPage(ctx, OCRRequest{
				Reader:     r,
				Size:       size,
				PageNumber: pageNumber,
				Page:       page,
			})
			if err != nil {
				warning := Warning{Page: pageNumber, Code: WarningOCR, Err: err}
				page.Warnings = append(page.Warnings, warning)
				doc.Warnings = append(doc.Warnings, warning)
				if opts.Strict {
					err = &StrictError{Warning: warning}
				} else {
					err = nil
				}
			}
		}

		if retainPages {
			doc.Pages = append(doc.Pages, page)
		}
		if yield != nil {
			if yieldErr := yield(page); yieldErr != nil {
				return doc, yieldErr
			}
		}
		if err != nil {
			return doc, err
		}
	}
	return doc, nil
}

func newPage(v object.Value, number int, layout LayoutOptions) Page {
	media := rectFromValue(object.Inherited(v, "MediaBox"))
	crop := rectFromValue(object.Inherited(v, "CropBox"))
	if crop == (Rect{}) {
		crop = media
	}
	rotation := int(intOr(object.Inherited(v, "Rotate"), 0)) % 360
	if rotation < 0 {
		rotation += 360
	}
	return Page{
		Number:   number,
		MediaBox: media,
		CropBox:  crop,
		Rotation: rotation,
		layout:   layout,
	}
}

func rectFromValue(v object.Value) Rect {
	if v.Kind() != object.Array || v.Len() < 4 {
		return Rect{}
	}
	x0, _ := v.Index(0).Float64()
	y0, _ := v.Index(1).Float64()
	x1, _ := v.Index(2).Float64()
	y1, _ := v.Index(3).Float64()
	return Rect{
		MinX: min(x0, x1),
		MinY: min(y0, y1),
		MaxX: max(x0, x1),
		MaxY: max(y0, y1),
	}
}

// Text reconstructs the whole document's plain text: pages separated by
// blank lines, empty pages skipped.
func (d *Document) Text() string {
	var b strings.Builder
	for _, p := range d.Pages {
		if t := p.Text(); t != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(t)
		}
	}
	return b.String()
}

// Text reconstructs a page's plain text using glyph baselines and the layout
// selected at extraction time. Rotated and vertical runs retain their reading
// direction, and spaces are inferred from geometric gaps.
func (p Page) Text() string {
	return p.TextWithOptions(p.layout)
}

// TextWithOptions reconstructs page text using layout rather than the layout
// selected during extraction.
func (p Page) TextWithOptions(layout LayoutOptions) string {
	if layout.Mode == LayoutContentOrder {
		return p.contentOrderText()
	}
	return reconstructPositionText(p, layout.Mode == LayoutColumns)
}

func (p Page) contentOrderText() string {
	if len(p.Glyphs) == 0 {
		return ""
	}
	var b strings.Builder
	prev := p.Glyphs[0]
	b.WriteString(prev.Text)
	endsSpace := strings.HasSuffix(prev.Text, " ")
	for _, g := range p.Glyphs[1:] {
		lineTol := 0.55 * max(g.Size, prev.Size)
		if lineTol <= 0 {
			lineTol = 5
		}
		switch {
		case math.Abs(g.Y-prev.Y) > lineTol:
			b.WriteByte('\n')
		case g.X-(prev.X+prev.Advance) > 0.17*max(g.Size, 1):
			if !endsSpace && !strings.HasPrefix(g.Text, " ") {
				b.WriteByte(' ')
			}
		}
		b.WriteString(g.Text)
		endsSpace = strings.HasSuffix(g.Text, " ")
		prev = g
	}
	return b.String()
}

func listPages(r *object.Reader) ([]object.Value, error) {
	const (
		maxDepth = 64
		maxNodes = 50000
	)
	nodes := 0
	hint := r.NumPages()
	if hint < 0 || hint > maxNodes {
		hint = 0
	}
	pages := make([]object.Value, 0, hint)
	var walk func(v object.Value, depth int) error
	walk = func(v object.Value, depth int) error {
		if depth > maxDepth {
			return errors.New("pdf: page tree too deep (possible reference cycle)")
		}
		if nodes++; nodes > maxNodes {
			return errors.New("pdf: page tree too large")
		}
		switch v.Key("Type").Name() {
		case "Pages":
			kids := v.Key("Kids")
			for i := range kids.Len() {
				if err := walk(kids.Index(i), depth+1); err != nil {
					return err
				}
			}
			return nil
		case "Page":
			pages = append(pages, v)
			return nil
		}
		return errors.New("pdf: invalid page-tree node")
	}
	err := walk(r.Trailer().Key("Root").Key("Pages"), 0)
	if err == nil {
		err = r.Err()
	}
	return pages, err
}

type matrix [6]float64 // a b c d e f

var identity = matrix{1, 0, 0, 1, 0, 0}

// mul returns m × n (PDF row-vector convention: applying m first, then n).
func mul(m, n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// translated returns translate(tx, ty) × m without multiplying the four
// unchanged linear terms.
func translated(m matrix, tx, ty float64) matrix {
	m[4] += tx*m[0] + ty*m[2]
	m[5] += tx*m[1] + ty*m[3]
	return m
}

func scaleX(m matrix) float64 { return math.Hypot(m[0], m[1]) }
func scaleY(m matrix) float64 { return math.Hypot(m[2], m[3]) }

// gstate is the graphics state saved/restored by q/Q. The text matrix is
// not part of it (it lives only between BT/ET).
type gstate struct {
	ctm      matrix
	font     *fontInfo
	fontSize float64
	charSp   float64
	wordSp   float64
	leading  float64
	rise     float64
	hscale   float64
}

const maxFormDepth = 8
const maxWarningsPerPage = 256

// Work budgets per page. Compressed content streams can inflate to
// hundreds of megabytes of operators (accidental or hostile decompression
// bombs); without a hard stop a single page could consume gigabytes of
// memory and minutes of CPU. Real dense pages stay orders of magnitude
// below these limits. Vars, not consts, so tests can lower them.
var (
	maxOpsPerPage    = 2_000_000
	maxGlyphsPerPage = 500_000
)

var errStopPage = errors.New("pdf: stop extracting page")

type walker struct {
	ctx             context.Context
	page            int
	strict          bool
	limits          Limits
	resolver        CMapResolver
	ignoreArtifacts bool
	foldLigatures   bool
	fonts           map[int]*fontInfo // document-wide, by object number; see loadFont

	glyphs   *glyphBuffer
	warnings []Warning
	depth    int
	ops      int
}

// glyphBuffer accumulates a page's glyphs in fixed-size chunks and hands
// them over as one exactly sized slice. The glyph slice is an
// extraction's largest allocation; growing it by appending copied several
// times the final bytes into ever-larger slices, and any capacity guess
// overshoots or undershoots as pages vary. With chunks a page costs its
// final glyph bytes once, the chunks are recycled across the pages of a
// document, and nothing is retained beyond what the page uses.
type glyphBuffer struct {
	chunks [][]Glyph // every chunk but the last is full
	n      int       // glyphs held
	free   [][]Glyph // recycled chunks
}

// glyphChunk is 24 KiB of glyphs: comfortably below the runtime's
// large-object threshold, so chunks come from the small-object allocator.
const glyphChunk = 256

func (b *glyphBuffer) len() int { return b.n }

func (b *glyphBuffer) add(g Glyph) {
	if b.n%glyphChunk == 0 {
		var c []Glyph
		if k := len(b.free); k > 0 {
			c, b.free = b.free[k-1][:0], b.free[:k-1]
		} else {
			c = make([]Glyph, 0, glyphChunk)
		}
		b.chunks = append(b.chunks, c)
	}
	last := &b.chunks[len(b.chunks)-1]
	*last = append(*last, g)
	b.n++
}

// truncate drops the glyphs from index n on.
func (b *glyphBuffer) truncate(n int) {
	for b.n > n {
		last := &b.chunks[len(b.chunks)-1]
		drop := min(len(*last), b.n-n)
		*last = (*last)[:len(*last)-drop]
		b.n -= drop
		if len(*last) == 0 {
			b.free = append(b.free, *last)
			b.chunks = b.chunks[:len(b.chunks)-1]
		}
	}
}

// tail copies out the glyphs from index n on.
func (b *glyphBuffer) tail(n int) []Glyph {
	if n >= b.n {
		return nil
	}
	out := make([]Glyph, 0, b.n-n)
	for i := n / glyphChunk; i < len(b.chunks); i++ {
		c := b.chunks[i]
		if i == n/glyphChunk {
			c = c[n%glyphChunk:]
		}
		out = append(out, c...)
	}
	return out
}

// take returns the glyphs as one exact slice and empties the buffer,
// keeping the chunks for the next page.
func (b *glyphBuffer) take() []Glyph {
	if b.n == 0 {
		return nil
	}
	out := make([]Glyph, 0, b.n)
	for _, c := range b.chunks {
		out = append(out, c...)
		b.free = append(b.free, c[:0])
	}
	b.chunks, b.n = b.chunks[:0], 0
	return out
}

func (w *walker) warning(code WarningCode, err error) error {
	if !w.strict {
		switch {
		case len(w.warnings) >= maxWarningsPerPage:
			return nil
		case len(w.warnings) == maxWarningsPerPage-1:
			code = WarningWorkLimit
			err = errors.New("additional page warnings suppressed")
		}
	}
	warning := Warning{Page: w.page, Code: code, Err: err}
	w.warnings = append(w.warnings, warning)
	if w.strict {
		return &StrictError{Warning: warning}
	}
	return nil
}

func (w *walker) stopForLimit(err error) error {
	if strictErr := w.warning(WarningWorkLimit, err); strictErr != nil {
		return strictErr
	}
	return errStopPage
}

func (w *walker) walkStream(strm, resources object.Value, gs gstate) error {
	if strm.IsNull() {
		return nil
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	fonts := map[string]*fontInfo{}
	warnedFonts := map[*fontInfo]bool{}
	warnedEmbedded := map[*fontInfo]bool{}
	var gsStack []gstate
	var marked []markedContent
	var decodedBuf []decoded
	malformedOperators := map[string]bool{}
	tm, tlm := identity, identity

	show := func(raw []byte) error {
		f := gs.font
		if f == nil {
			return nil
		}
		decodedBuf = f.appendDecoded(decodedBuf[:0], raw)
		if f.embeddedErr != nil && !warnedEmbedded[f] {
			// Reported here rather than at Tf: the program is parsed only
			// once a code falls through to it, which is also when its
			// absence matters.
			warnedEmbedded[f] = true
			if err := w.warning(WarningUnsupported, f.embeddedErr); err != nil {
				return err
			}
		}
		trm := mul(tm, gs.ctm)
		for _, d := range decodedBuf {
			if w.glyphs.len() >= w.limits.MaxGlyphsPerPage {
				return w.stopForLimit(errors.New("glyph count exceeds per-page limit"))
			}
			if w.glyphs.len()&1023 == 0 {
				if err := w.ctx.Err(); err != nil {
					return err
				}
			}
			if d.vertical {
				adv := d.vm.w1/1000*gs.fontSize + gs.charSp
				if d.space {
					adv += gs.wordSp
				}
				origin := translated(trm, d.vm.vx/1000*gs.fontSize, d.vm.vy/1000*gs.fontSize+gs.rise)
				if text := sanitizeText(d.text, w.foldLigatures); text != "" {
					w.glyphs.add(positionedVerticalGlyph(text, origin, gs.fontSize, adv))
				}
				tm = translated(tm, 0, adv)
				trm = translated(trm, 0, adv)
				continue
			}
			adv := (d.width/1000*gs.fontSize + gs.charSp) * gs.hscale
			if d.space {
				adv += gs.wordSp * gs.hscale
			}
			if text := sanitizeText(d.text, w.foldLigatures); text != "" {
				w.glyphs.add(positionedGlyph(text, trm, gs.fontSize, gs.rise, adv))
			}
			tm = translated(tm, adv, 0)
			trm = translated(trm, adv, 0)
		}
		return nil
	}

	data, streamErrs := contentBytesLimitError(strm, w.limits.MaxStreamBytes)
	for _, streamErr := range streamErrs {
		code := WarningStream
		if errors.Is(streamErr, safeio.ErrLimitExceeded) {
			code = WarningStreamLimit
		}
		if err := w.warning(code, streamErr); err != nil {
			return err
		}
	}

	malformedOperator := func(op string) error {
		if malformedOperators[op] {
			return nil
		}
		malformedOperators[op] = true
		return w.warning(
			WarningMalformedPage,
			fmt.Errorf("content operator %q has invalid operands or state", op),
		)
	}

	err := interpretContentError(data, func(op []byte, args []operand) error {
		w.ops++
		if w.ops > w.limits.MaxOperatorsPerPage {
			return w.stopForLimit(errors.New("operator count exceeds per-page limit"))
		}
		if w.ops&1023 == 0 {
			if err := w.ctx.Err(); err != nil {
				return err
			}
		}
		switch string(op) {
		case "q":
			if len(args) != 0 {
				return malformedOperator("q")
			}
			gsStack = append(gsStack, gs)
		case "Q":
			if len(args) != 0 || len(gsStack) == 0 {
				return malformedOperator("Q")
			}
			if len(gsStack) > 0 {
				gs = gsStack[len(gsStack)-1]
				gsStack = gsStack[:len(gsStack)-1]
			}
		case "cm":
			if !operandsAre(args, opNum, opNum, opNum, opNum, opNum, opNum) {
				return malformedOperator("cm")
			}
			gs.ctm = mul(matrixFromOperands(args), gs.ctm)
		case "BT", "ET":
			if len(args) != 0 {
				return malformedOperator(string(op))
			}
			tm, tlm = identity, identity
		case "Tf":
			if !operandsAre(args, opName, opNum) {
				return malformedOperator("Tf")
			}
			gs.font = loadFont(fonts, w.fonts, resources, args[0].name, w.resolver, w.limits.MaxStreamBytes)
			gs.fontSize = args[1].num
			if gs.font != nil && !warnedFonts[gs.font] {
				warnedFonts[gs.font] = true
				for _, fontErr := range gs.font.warnings {
					if err := w.warning(WarningUnsupported, fontErr); err != nil {
						return err
					}
				}
			}
		case "Td":
			if !operandsAre(args, opNum, opNum) {
				return malformedOperator("Td")
			}
			tlm = translated(tlm, args[0].num, args[1].num)
			tm = tlm
		case "TD":
			if !operandsAre(args, opNum, opNum) {
				return malformedOperator("TD")
			}
			gs.leading = -args[1].num
			tlm = translated(tlm, args[0].num, args[1].num)
			tm = tlm
		case "Tm":
			if !operandsAre(args, opNum, opNum, opNum, opNum, opNum, opNum) {
				return malformedOperator("Tm")
			}
			tlm = matrixFromOperands(args)
			tm = tlm
		case "T*":
			if len(args) != 0 {
				return malformedOperator("T*")
			}
			tlm = translated(tlm, 0, -gs.leading)
			tm = tlm
		case "TL":
			if !operandsAre(args, opNum) {
				return malformedOperator("TL")
			}
			gs.leading = args[0].num
		case "Tc":
			if !operandsAre(args, opNum) {
				return malformedOperator("Tc")
			}
			gs.charSp = args[0].num
		case "Tw":
			if !operandsAre(args, opNum) {
				return malformedOperator("Tw")
			}
			gs.wordSp = args[0].num
		case "Tz":
			if !operandsAre(args, opNum) {
				return malformedOperator("Tz")
			}
			gs.hscale = args[0].num / 100
		case "Ts":
			if !operandsAre(args, opNum) {
				return malformedOperator("Ts")
			}
			gs.rise = args[0].num
		case "Tj":
			if !operandsAre(args, opStr) {
				return malformedOperator("Tj")
			}
			return show(args[0].str)
		case "'":
			if !operandsAre(args, opStr) {
				return malformedOperator("'")
			}
			tlm = translated(tlm, 0, -gs.leading)
			tm = tlm
			return show(args[0].str)
		case "\"":
			if !operandsAre(args, opNum, opNum, opStr) {
				return malformedOperator("\"")
			}
			gs.wordSp = args[0].num
			gs.charSp = args[1].num
			tlm = translated(tlm, 0, -gs.leading)
			tm = tlm
			return show(args[2].str)
		case "TJ":
			if !validTextArray(args) {
				return malformedOperator("TJ")
			}
			for _, el := range args[0].arr {
				switch el.kind {
				case opStr:
					if err := show(el.str); err != nil {
						return err
					}
				case opNum:
					if gs.font != nil && gs.font.vertical {
						adv := -el.num / 1000 * gs.fontSize
						tm = translated(tm, 0, adv)
					} else {
						adv := -el.num / 1000 * gs.fontSize * gs.hscale
						tm = translated(tm, adv, 0)
					}
				}
			}
		case "Do":
			if !operandsAre(args, opName) {
				return malformedOperator("Do")
			}
			xobj := resources.Key("XObject").Key(args[0].name)
			if xobj.Kind() != object.Stream || xobj.Key("Subtype").Name() != "Form" {
				break
			}
			if w.depth >= w.limits.MaxFormDepth {
				if err := w.warning(WarningWorkLimit, errors.New("form XObject nesting exceeds limit")); err != nil {
					return err
				}
				break
			}
			sub := gs
			if m := xobj.Key("Matrix"); m.Kind() == object.Array && m.Len() == 6 {
				var fm matrix
				for i := range fm {
					fm[i], _ = m.Index(i).Float64()
				}
				sub.ctm = mul(fm, gs.ctm)
			}
			subRes := xobj.Key("Resources")
			if subRes.Kind() != object.Dict {
				subRes = resources
			}
			w.depth++
			err := w.walkStream(xobj, subRes, sub)
			w.depth--
			if err != nil {
				return err
			}
		case "BMC":
			if !operandsAre(args, opName) {
				return malformedOperator("BMC")
			}
			marked = append(marked, newMarkedContent(args[0].name, operand{}, resources, w.glyphs.len(), tm, gs))
		case "BDC":
			if len(args) != 2 || args[0].kind != opName ||
				args[1].kind != opName && args[1].kind != opDict {
				return malformedOperator("BDC")
			}
			marked = append(marked, newMarkedContent(args[0].name, args[1], resources, w.glyphs.len(), tm, gs))
		case "EMC":
			if len(args) != 0 {
				return malformedOperator("EMC")
			}
			if len(marked) == 0 {
				return errors.New("marked-content EMC without BMC or BDC")
			}
			mark := marked[len(marked)-1]
			marked = marked[:len(marked)-1]
			w.finishMarkedContent(mark)
		}
		return nil
	})
	if err == nil {
		if len(marked) > 0 {
			for i := len(marked) - 1; i >= 0; i-- {
				w.finishMarkedContent(marked[i])
			}
			return w.warning(WarningMalformedPage, errors.New("unterminated marked-content sequence"))
		}
		return nil
	}
	if errors.Is(err, errStopPage) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var strictErr *StrictError
	if errors.As(err, &strictErr) {
		return err
	}
	return w.warning(WarningMalformedPage, err)
}

type markedContent struct {
	glyphStart int
	actualText string
	hasActual  bool
	artifact   bool
	tm         matrix
	gs         gstate
}

func newMarkedContent(
	tag string,
	property operand,
	resources object.Value,
	glyphStart int,
	tm matrix,
	gs gstate,
) markedContent {
	mark := markedContent{
		glyphStart: glyphStart,
		artifact:   tag == "Artifact",
		tm:         tm,
		gs:         gs,
	}
	switch property.kind {
	case opDict:
		if actual, ok := property.dict["ActualText"]; ok && actual.kind == opStr {
			mark.actualText = decodeTextString(actual.str)
			mark.hasActual = true
		}
		if typ, ok := property.dict["Type"]; ok && typ.kind == opName && typ.name == "Artifact" {
			mark.artifact = true
		}
	case opName:
		value := resources.Key("Properties").Key(property.name)
		if value.Kind() == object.Dict {
			actual := value.Key("ActualText")
			if actual.Kind() == object.String {
				mark.actualText = decodeTextString([]byte(actual.RawString()))
				mark.hasActual = true
			}
			if value.Key("Type").Name() == "Artifact" {
				mark.artifact = true
			}
		}
	}
	return mark
}

func (w *walker) finishMarkedContent(mark markedContent) {
	if mark.glyphStart > w.glyphs.len() {
		return
	}
	if mark.artifact && w.ignoreArtifacts {
		w.glyphs.truncate(mark.glyphStart)
		return
	}
	if !mark.hasActual {
		return
	}
	replaced := w.glyphs.tail(mark.glyphStart)
	w.glyphs.truncate(mark.glyphStart)
	if mark.actualText == "" {
		return
	}
	if len(replaced) == 0 {
		trm := mul(mark.tm, mark.gs.ctm)
		size := mark.gs.fontSize
		if size == 0 {
			size = 1
		}
		advance := float64(len([]rune(mark.actualText))) * size * 0.5
		w.glyphs.add(positionedGlyph(mark.actualText, trm, size, mark.gs.rise, advance))
		return
	}
	// The replacement spans from the first glyph's origin to the last
	// glyph's baseline end.
	glyph := replaced[0]
	glyph.Text = mark.actualText
	end := replaced[len(replaced)-1].Baseline().End
	dx, dy := end.X-glyph.X, end.Y-glyph.Y
	glyph.Advance = math.Hypot(dx, dy)
	if glyph.Advance > 0 {
		glyph.Direction = Point{X: dx / glyph.Advance, Y: dy / glyph.Advance}
	}
	w.glyphs.add(glyph)
}

func positionedGlyph(text string, trm matrix, fontSize, rise, advance float64) Glyph {
	xLen := scaleX(trm)
	yLen := scaleY(trm)
	xDir := Point{X: 1}
	if xLen > 0 {
		xDir = Point{X: trm[0] / xLen, Y: trm[1] / xLen}
	}
	riseX := rise * trm[2]
	riseY := rise * trm[3]
	start := Point{X: trm[4] + riseX, Y: trm[5] + riseY}
	end := Point{
		X: start.X + advance*trm[0],
		Y: start.Y + advance*trm[1],
	}
	if baselineLength := math.Hypot(end.X-start.X, end.Y-start.Y); baselineLength > 0 {
		xDir = Point{X: (end.X - start.X) / baselineLength, Y: (end.Y - start.Y) / baselineLength}
	}
	return Glyph{
		Text:      text,
		X:         start.X,
		Y:         start.Y,
		Advance:   advance * xLen,
		Size:      math.Abs(fontSize) * yLen,
		Direction: xDir,
		Ascent:    Point{X: fontSize * trm[2], Y: fontSize * trm[3]},
	}
}

func positionedVerticalGlyph(text string, trm matrix, fontSize, advance float64) Glyph {
	dx := advance * trm[2]
	dy := advance * trm[3]
	length := math.Hypot(dx, dy)
	direction := Point{Y: -1}
	if length > 0 {
		direction = Point{X: dx / length, Y: dy / length}
	}
	halfX := fontSize * trm[0] / 2
	halfY := fontSize * trm[1] / 2
	return Glyph{
		Text:      text,
		X:         trm[4],
		Y:         trm[5],
		Advance:   length,
		Size:      math.Abs(fontSize) * scaleX(trm),
		Direction: direction,
		Ascent:    Point{X: halfX, Y: halfY},
		Descent:   Point{X: -halfX, Y: -halfY},
	}
}

func matrixFromOperands(args []operand) matrix {
	var m matrix
	for i := range m {
		m[i] = args[i].num
	}
	return m
}

func operandsAre(args []operand, kinds ...opKind) bool {
	if len(args) != len(kinds) {
		return false
	}
	for i, kind := range kinds {
		if args[i].kind != kind {
			return false
		}
	}
	return true
}

func validTextArray(args []operand) bool {
	if !operandsAre(args, opArr) {
		return false
	}
	for _, item := range args[0].arr {
		if item.kind != opStr && item.kind != opNum {
			return false
		}
	}
	return true
}

// sanitize normalizes decoded glyph text using the default ligature-folding
// behavior. sanitizeText exposes the policy switch used by Options.
func sanitize(s string) string { return sanitizeText(s, true) }

// sanitizeText normalizes decoded glyph text for output. It strips glyphs that
// carry no textual meaning — unmapped glyphs (U+FFFD), C0/C1 control
// characters, private-use icons (icon fonts) — turns non-breaking spaces
// into plain spaces (some generators join every word with NBSP glyphs), and,
// when foldLigatures is set, folds the Latin ligature presentation forms
// U+FB00–U+FB06 (ﬀ ﬁ ﬂ ﬃ ﬄ ﬅ ﬆ) to their letter sequences. The Adobe Glyph
// List maps the glyph names /ff /fi /fl /ffi /ffl — which every TeX font's
// /Differences array uses — to those codepoints, so without folding "file"
// extracts as "ﬁle" and is invisible to substring search and to tokenizers
// (SQLite FTS5's unicode61 among them) that skip compatibility
// decomposition; poppler, pdf.js and MuPDF fold the same range.
func sanitizeText(s string, foldLigatures bool) string {
	clean := true
	for _, r := range s {
		if isJunkRune(r) || r == '\u00a0' || foldLigatures && isLigatureRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s)) // every rewrite is no longer than its source
	for _, r := range s {
		switch {
		case r == '\u00a0':
			b.WriteByte(' ')
		case isJunkRune(r):
		case foldLigatures && isLigatureRune(r):
			b.WriteString(ligatureLetters[r-0xFB00])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isJunkRune(r rune) bool {
	return r == '�' || r < 0x20 || (r >= 0x7F && r <= 0x9F) || (r >= 0xE000 && r <= 0xF8FF)
}

func isLigatureRune(r rune) bool { return r >= 0xFB00 && r <= 0xFB06 }

// ligatureLetters spells U+FB00–U+FB06 in order. U+FB05 (long s t) folds
// to "st" like U+FB06, as MuPDF does, since "ſ" is no more searchable than
// the ligature itself.
var ligatureLetters = [...]string{"ff", "fi", "fl", "ffi", "ffl", "st", "st"}
