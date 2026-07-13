// Package pdf extracts text from PDF files, with glyph positions.
//
// Extract returns per-page positioned glyphs; Page.Text reconstructs
// plain text by clustering glyphs into lines and inserting word spacing
// derived from glyph gaps and font metrics.
//
// The package is pure Go with no external dependencies. It implements the
// PDF object layer (cross-reference tables and streams, object streams,
// stream filters, and standard-handler decryption) natively per ISO
// 32000, and on top of it a content-stream lexer, interpreter, and font
// decoder tuned for real-world files: it descends into Form XObjects
// (where e.g. Google Docs exports keep their text), honors /ToUnicode
// even alongside an /Encoding dictionary, tracks positions through every
// text operator, never loops on tokens straddling /Contents array
// segments, and defends against decompression bombs, stalled filter
// chains, and cyclic page trees.
package pdf

import (
	"context"
	"errors"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/giraffesyo/pdf/internal/object"
)

// Glyph is one decoded glyph (or glyph cluster) with its position in
// unrotated page space.
type Glyph struct {
	Text    string  // decoded text, non-empty
	X, Y    float64 // origin
	Advance float64 // horizontal advance
	Size    float64 // effective font size
}

// Page holds the glyphs of one page in content order.
type Page struct {
	Glyphs []Glyph
}

// Document is the extraction result for a whole file.
type Document struct {
	Pages []Page
}

// Extract parses the PDF in r and extracts every page's glyphs. Malformed
// pages yield whatever was decoded before the failure; a malformed file
// returns an error. The context is checked between pages.
func Extract(ctx context.Context, r io.ReaderAt, size int64) (*Document, error) {
	reader, err := object.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	// Reject cyclic/oversized page trees before walking them.
	n, err := countPages(reader)
	if err != nil {
		return nil, err
	}
	// NumPages reads /Count, which a malformed file may make negative or
	// huge; countPages bounds the real leaf count, so clamp between them.
	if np := reader.NumPages(); np >= 0 && np < n {
		n = np
	}
	doc := &Document{Pages: make([]Page, 0, n)}
	for i := 1; i <= n; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page := reader.Page(i)
		if page.IsNull() {
			continue
		}
		doc.Pages = append(doc.Pages, Page{Glyphs: extractPage(page)})
	}
	return doc, nil
}

// Text reconstructs the whole document's plain text: pages separated by
// blank lines, empty pages skipped.
func (d *Document) Text() string {
	var pages []string
	for _, p := range d.Pages {
		if t := p.Text(); t != "" {
			pages = append(pages, t)
		}
	}
	return strings.Join(pages, "\n\n")
}

// Text reconstructs a page's plain text from glyph positions: glyphs are
// clustered into lines by Y, ordered by X, and spaces are inserted where
// the horizontal gap between glyphs is too wide to be kerning.
func (p Page) Text() string {
	glyphs := p.Glyphs
	if len(glyphs) == 0 {
		return ""
	}

	// Cluster into lines by Y (PDF Y grows upward). The tolerance is
	// generous enough to pull superscripts/subscripts into their line but
	// far smaller than typical line leading (~1.2em).
	sorted := make([]Glyph, len(glyphs))
	copy(sorted, glyphs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Y > sorted[j].Y })
	var lines [][]Glyph
	lineY := 0.0
	for _, g := range sorted {
		tol := 0.55 * g.Size
		if tol <= 0 {
			tol = 5
		}
		if len(lines) == 0 || lineY-g.Y > tol {
			lines = append(lines, nil)
			lineY = g.Y
		}
		lines[len(lines)-1] = append(lines[len(lines)-1], g)
	}

	var b strings.Builder
	for li, line := range lines {
		if li > 0 {
			b.WriteByte('\n')
		}
		sort.SliceStable(line, func(i, j int) bool { return line[i].X < line[j].X })
		prevEnd := 0.0
		for gi, g := range line {
			if gi > 0 {
				gap := g.X - prevEnd
				thresh := 0.17 * g.Size
				if thresh <= 0 {
					thresh = 1
				}
				if gap > thresh && !strings.HasSuffix(b.String(), " ") && !strings.HasPrefix(g.Text, " ") {
					b.WriteByte(' ')
				}
			}
			b.WriteString(g.Text)
			prevEnd = g.X + g.Advance
		}
	}
	return b.String()
}

// countPages validates the document's page tree and returns its leaf page
// count, rejecting cyclic or oversized trees before the page walk visits
// them.
func countPages(r *object.Reader) (int, error) {
	const (
		maxDepth = 64
		maxNodes = 50000
	)
	nodes := 0
	var walk func(v object.Value, depth int) (int, error)
	walk = func(v object.Value, depth int) (int, error) {
		if depth > maxDepth {
			return 0, errors.New("pdf: page tree too deep (possible reference cycle)")
		}
		if nodes++; nodes > maxNodes {
			return 0, errors.New("pdf: page tree too large")
		}
		switch v.Key("Type").Name() {
		case "Pages":
			kids := v.Key("Kids")
			total := 0
			for i := range kids.Len() {
				n, err := walk(kids.Index(i), depth+1)
				if err != nil {
					return 0, err
				}
				total += n
			}
			return total, nil
		case "Page":
			return 1, nil
		}
		return 0, nil
	}
	return walk(r.Trailer().Key("Root").Key("Pages"), 0)
}

// extractPage walks a page's content streams, including nested Form
// XObjects. Malformed streams yield whatever was decoded before the
// failure.
func extractPage(p object.Value) []Glyph {
	w := &walker{}
	res := object.Inherited(p, "Resources")
	w.walkStream(p.Key("Contents"), res, gstate{ctm: identity, hscale: 1})
	return w.glyphs
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

func translate(tx, ty float64) matrix { return matrix{1, 0, 0, 1, tx, ty} }

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

// Work budgets per page. Compressed content streams can inflate to
// hundreds of megabytes of operators (accidental or hostile decompression
// bombs); without a hard stop a single page could consume gigabytes of
// memory and minutes of CPU. Real dense pages stay orders of magnitude
// below these limits. Vars, not consts, so tests can lower them.
var (
	maxOpsPerPage    = 2_000_000
	maxGlyphsPerPage = 500_000
)

// errBudgetExceeded aborts a walk through nested Interpret callbacks; it
// unwinds every walkStream level and is swallowed at the top.
var errBudgetExceeded = errors.New("pdf: page work budget exceeded")

type walker struct {
	glyphs []Glyph
	depth  int
	ops    int
}

func (w *walker) walkStream(strm, resources object.Value, gs gstate) {
	// The per-page work budget aborts by panicking errBudgetExceeded; it
	// must keep unwinding through nested Form XObjects to the top-level
	// call, where it is swallowed. (The object layer returns errors rather
	// than panicking, so nothing else reaches here.)
	defer func() {
		//nolint:errorlint // identity comparison on a recovered sentinel, never wrapped
		if r := recover(); r != nil && r == errBudgetExceeded && w.depth > 0 {
			panic(r)
		}
	}()

	fonts := map[string]*fontInfo{}
	var gsStack []gstate
	tm, tlm := identity, identity

	show := func(raw string) {
		f := gs.font
		if f == nil {
			return
		}
		for _, d := range f.decode(raw) {
			trm := mul(tm, gs.ctm)
			adv := (d.width/1000*gs.fontSize + gs.charSp) * gs.hscale
			if d.space {
				adv += gs.wordSp * gs.hscale
			}
			if text := sanitize(d.text); text != "" {
				w.glyphs = append(w.glyphs, Glyph{
					Text:    text,
					X:       trm[4],
					Y:       trm[5] + gs.rise*scaleY(trm),
					Advance: adv * scaleX(trm),
					Size:    math.Abs(gs.fontSize) * scaleY(trm),
				})
			}
			tm = mul(translate(adv, 0), tm)
		}
	}

	interpretContent(contentBytes(strm), func(op string, args []operand) {
		if w.ops++; w.ops > maxOpsPerPage || len(w.glyphs) > maxGlyphsPerPage {
			panic(errBudgetExceeded)
		}
		switch op {
		case "q":
			gsStack = append(gsStack, gs)
		case "Q":
			if len(gsStack) > 0 {
				gs = gsStack[len(gsStack)-1]
				gsStack = gsStack[:len(gsStack)-1]
			}
		case "cm":
			if len(args) == 6 {
				gs.ctm = mul(matrixFromOperands(args), gs.ctm)
			}
		case "BT", "ET":
			tm, tlm = identity, identity
		case "Tf":
			if len(args) == 2 {
				gs.font = loadFont(fonts, resources, args[0].name)
				gs.fontSize = args[1].num
			}
		case "Td":
			if len(args) == 2 {
				tlm = mul(translate(args[0].num, args[1].num), tlm)
				tm = tlm
			}
		case "TD":
			if len(args) == 2 {
				gs.leading = -args[1].num
				tlm = mul(translate(args[0].num, args[1].num), tlm)
				tm = tlm
			}
		case "Tm":
			if len(args) == 6 {
				tlm = matrixFromOperands(args)
				tm = tlm
			}
		case "T*":
			tlm = mul(translate(0, -gs.leading), tlm)
			tm = tlm
		case "TL":
			if len(args) == 1 {
				gs.leading = args[0].num
			}
		case "Tc":
			if len(args) == 1 {
				gs.charSp = args[0].num
			}
		case "Tw":
			if len(args) == 1 {
				gs.wordSp = args[0].num
			}
		case "Tz":
			if len(args) == 1 {
				gs.hscale = args[0].num / 100
			}
		case "Ts":
			if len(args) == 1 {
				gs.rise = args[0].num
			}
		case "Tj":
			if len(args) == 1 {
				show(string(args[0].str))
			}
		case "'":
			if len(args) == 1 {
				tlm = mul(translate(0, -gs.leading), tlm)
				tm = tlm
				show(string(args[0].str))
			}
		case "\"":
			if len(args) == 3 {
				gs.wordSp = args[0].num
				gs.charSp = args[1].num
				tlm = mul(translate(0, -gs.leading), tlm)
				tm = tlm
				show(string(args[2].str))
			}
		case "TJ":
			if len(args) == 1 && args[0].kind == opArr {
				for _, el := range args[0].arr {
					switch el.kind {
					case opStr:
						show(string(el.str))
					case opNum:
						adv := -el.num / 1000 * gs.fontSize * gs.hscale
						tm = mul(translate(adv, 0), tm)
					}
				}
			}
		case "Do":
			if len(args) != 1 || w.depth >= maxFormDepth {
				break
			}
			xobj := resources.Key("XObject").Key(args[0].name)
			if xobj.Kind() != object.Stream || xobj.Key("Subtype").Name() != "Form" {
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
			w.walkStream(xobj, subRes, sub)
			w.depth--
		}
	})
}

func matrixFromOperands(args []operand) matrix {
	var m matrix
	for i := range m {
		m[i] = args[i].num
	}
	return m
}

// sanitize strips glyphs that carry no textual meaning in Markdown output
// — unmapped glyphs (U+FFFD), C0/C1 control characters, private-use icons
// (icon fonts) — and normalizes non-breaking spaces to plain spaces (some
// generators join every word with NBSP glyphs).
func sanitize(s string) string {
	clean := true
	for _, r := range s {
		if isJunkRune(r) || r == ' ' {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r == ' ':
			out = append(out, ' ')
		case isJunkRune(r):
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

func isJunkRune(r rune) bool {
	return r == '�' || r < 0x20 || (r >= 0x7F && r <= 0x9F) || (r >= 0xE000 && r <= 0xF8FF)
}
