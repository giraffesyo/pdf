package pdf

import (
	"math"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// Reading order across columns.
//
// A page's baselines, top to bottom, interleave the lines of side-by-side
// columns. readingOrder groups consecutive rows into bands that share a
// gutter — an empty vertical strip that no row in the band crosses — and
// reads a band that holds prose on both sides of its gutters column by
// column. A row that crosses the gutter, such as a title, a figure caption
// spanning the page, or text below the columns, ends the band, so the
// page reads top to bottom around it. A band of short fragments, such as
// a table, reads row by row unless every gutter is to be honoured.

const (
	// minGutterEm is the narrowest gutter, in font sizes: wider than the
	// word spacing of justified text, narrower than column gutters, which
	// run from about 1.8 em in journals to 3 em in newsletters.
	minGutterEm = 1.0
	// A column counts as prose when its fragments average this many
	// words, are mostly letters, and it is this many font sizes wide:
	// table cells hold a word or two, or numbers, in narrower columns.
	minProseWords   = 3.0
	minProseLetters = 0.6
	minProseWidthEm = 12
	// A paragraph's lines fill its column: at least half of them reach
	// this fraction of the column's width, where table cells vary.
	minProseFill = 0.7
	// maxBandGapPitches closes a band at a vertical gap this many times
	// its usual line pitch: both columns breaking at once marks a new
	// region of the page.
	maxBandGapPitches = 2.5
	// maxBandGapLines ends a band at a vertical gap this many font sizes
	// tall, which no column's lines fill.
	maxBandGapLines = 4
	// minColumnEm is the narrowest column, in font sizes: narrower strips
	// hold list bullets, numbers, or line numbers beside their text.
	minColumnEm = 4
	// minInterleave is the share of adjacent rows in a band of columns
	// that switch column or span several.
	minInterleave = 0.75
	// minBandRows is the fewest rows that establish a gutter.
	minBandRows = 3
	// maxTailGapPitches ends a band where one column runs on alone and
	// meets a gap this many of its line pitches: a paragraph break at
	// least, beyond which the page need not be that column's.
	maxTailGapPitches = 1.3
	// bandLookahead is how many later rows may start a longer band.
	bandLookahead = 3
)

// A row is one horizontal baseline of the page, split into fragments at
// gaps a gutter could occupy.
type row struct {
	line      layoutLine
	fragments []fragment
	offset    float64 // distance along the page's normal; larger is higher
	size      float64
}

type fragment struct {
	start, end int // glyph range within the row's line
	minX, maxX float64
	words      int
	letters    int // letters among the fragment's characters
	chars      int // characters other than spaces
}

type interval struct{ lo, hi float64 }

// readingOrder orders a page's lines for reading: top to bottom, and
// column by column within bands of side-by-side prose. With allGutters,
// every band with a gutter reads column by column, tables included.
// The result aliases sc, which the caller must not return to its pool
// until it is done with the lines.
func readingOrder(lines []layoutLine, allGutters bool, sc *orderScratch) []layoutLine {
	rows, fragments, other := sc.rows[:0], sc.fragments[:0], sc.other[:0]
	defer func() { sc.rows, sc.fragments, sc.other = rows, fragments, other }()
	for _, line := range lines {
		if len(line.glyphs) == 0 || line.dir.X < 0.97 {
			// Rotated or vertical text, placed where its reading starts.
			other = append(other, placed{line, lineStart(line)})
			continue
		}
		var r row
		r, fragments = newRow(line, fragments)
		rows = append(rows, r)
	}
	slices.SortStableFunc(rows, func(a, b row) int { return compareLayoutLines(a.line, b.line) })

	// Rotated and vertical lines take their place by where they start,
	// between the bands around them — a figure's axis labels stay with the
	// figure, and margin text running up the page comes where it begins.
	slices.SortStableFunc(other, func(a, b placed) int {
		switch {
		case a.top > b.top:
			return -1
		case a.top < b.top:
			return 1
		}
		return compareLayoutLines(a.line, b.line)
	})
	ordered := sc.ordered[:0]
	defer func() { sc.ordered = ordered }()
	next := 0
	band := &sc.band
	band.explicit = allGutters
	kept := sc.kept[:0]
	defer func() { sc.kept = kept }()
	for start := 0; start < len(rows); {
		for next < len(other) && other[next].top > rows[start].offset {
			ordered = append(ordered, other[next].line)
			next++
		}
		end, gutters := band.grow(rows, start)
		gutters = append(kept[:0], gutters...) // the lookahead reuses band
		kept = gutters
		// A band can start at a row that merely sits between the columns —
		// a centred author line — and settle on a gap that is no gutter.
		// If a band of columns starting a row or two later is larger, the
		// rows before it read alone.
		for later := start + 1; later < min(end, start+1+bandLookahead); later++ {
			laterEnd, laterGutters := band.grow(rows, later)
			if laterEnd-later > end-start && len(laterGutters) > 0 &&
				(allGutters || sideBySide(rows[later:laterEnd], laterGutters)) {
				end, gutters = later, nil
				break
			}
		}
		ordered = appendBand(ordered, rows[start:end], gutters, allGutters)
		start = end
	}
	for _, p := range other[next:] {
		ordered = append(ordered, p.line)
	}
	return ordered
}

// placed is a rotated or vertical line and the height it starts at.
type placed struct {
	line layoutLine
	top  float64
}

// orderScratch is the working memory readingOrder reuses from page to
// page, pooled: rows and their fragments, the lines it orders, and the
// band state.
type orderScratch struct {
	rows      []row
	fragments []fragment
	other     []placed
	ordered   []layoutLine
	kept      []interval
	band      bandState
}

var orderScratchPool = sync.Pool{New: func() any { return new(orderScratch) }}

// maxPooledLines bounds the scratch a pool keeps: a page of a few
// hundred lines is common, one of tens of thousands is not worth holding.
const maxPooledLines = 1 << 14

func putOrderScratch(sc *orderScratch) {
	if cap(sc.rows) > maxPooledLines || cap(sc.fragments) > 2*maxPooledLines {
		return
	}
	for i := range sc.ordered {
		sc.ordered[i] = layoutLine{} // drop the page's glyphs
	}
	for i := range sc.rows {
		sc.rows[i] = row{}
	}
	for i := range sc.other {
		sc.other[i] = placed{}
	}
	orderScratchPool.Put(sc)
}

// lineStart is the height at which a line's text begins: its first
// glyph's, in reading order along the line.
func lineStart(line layoutLine) float64 {
	return line.glyphs[0].Y
}

// newRow splits line into fragments, appended to the shared backing
// slice, which it returns.
func newRow(line layoutLine, backing []fragment) (row, []fragment) {
	r := row{line: line, offset: line.offset}
	first := len(backing)
	sizes := 0.0
	begin := 0
	for i, g := range line.glyphs {
		sizes += g.Size
		if i == 0 {
			continue
		}
		prev := line.glyphs[i-1]
		gap := g.X - (prev.X + math.Abs(prev.Advance))
		if gap >= minGutterEm*max(prev.Size, g.Size, 1) {
			backing = append(backing, newFragment(line, begin, i))
			begin = i
		}
	}
	backing = append(backing, newFragment(line, begin, len(line.glyphs)))
	r.fragments = backing[first:len(backing):len(backing)]
	r.size = sizes / float64(len(line.glyphs))
	return r, backing
}

func newFragment(line layoutLine, start, end int) fragment {
	f := fragment{start: start, end: end, minX: math.Inf(1), maxX: math.Inf(-1), words: 1}
	for i := start; i < end; i++ {
		g := line.glyphs[i]
		for _, ch := range g.Text {
			if unicode.IsLetter(ch) {
				f.letters++
			}
			if !unicode.IsSpace(ch) {
				f.chars++
			}
		}
		f.minX = min(f.minX, g.X)
		f.maxX = max(f.maxX, g.X+math.Abs(g.Advance))
		if i > start && strings.TrimSpace(g.Text) == "" && strings.TrimSpace(line.glyphs[i-1].Text) != "" {
			f.words++
		}
	}
	// Words separated by gaps rather than space glyphs count too.
	for i := start + 1; i < end; i++ {
		prev, g := line.glyphs[i-1], line.glyphs[i]
		if strings.TrimSpace(prev.Text) != "" && strings.TrimSpace(g.Text) != "" &&
			g.X-(prev.X+math.Abs(prev.Advance)) > wordGap(max(g.Size, 1)) {
			f.words++
		}
	}
	return f
}

// bandState is the scratch a band's growth reuses: the merged extent of
// the band's text, its gutters, and the line pitches seen, sorted.
type bandState struct {
	coverage []interval
	gutters  []interval
	kept     []interval
	pitches  []float64
	size     float64

	columnPitches []float64 // gaps between rows of the same column, sorted
	lastOffset    []float64 // per column, the offset of its latest row

	// explicit is set when the caller asked for columns: two rows are
	// enough to establish a gutter, and narrow columns count.
	explicit bool
}

// grow extends a band from rows[start] while the rows keep a common
// gutter, and returns its end and the gutters, which alias b's scratch
// until the next call.
func (b *bandState) grow(rows []row, start int) (int, []interval) {
	b.coverage, b.pitches, b.kept, b.size = b.coverage[:0], b.pitches[:0], b.kept[:0], 0
	b.columnPitches, b.lastOffset = b.columnPitches[:0], b.lastOffset[:0]
	b.add(rows[start])
	prevColumn := -1 // column of the previous row, -1 when unknown or several
	columns := 0     // distinct single columns seen, or 2 once a row spans several
	end := start + 1
	for ; end < len(rows); end++ {
		r := rows[end]
		gap := rows[end-1].offset - r.offset
		if len(b.pitches) > 0 && gap > maxBandGapPitches*b.pitches[len(b.pitches)/2] {
			break
		}
		if gap > maxBandGapLines*max(b.size, r.size) {
			break // a hole across every column: a new region of the page
		}
		column := -1
		if len(b.kept) > 0 {
			column = rowColumn(r, b.kept)
		}
		if column >= 0 && column == prevColumn && columns >= 2 && len(b.columnPitches) >= 2 {
			// One column running on alone after the others ended: a
			// paragraph-sized gap there ends the band, so what follows —
			// a table below the columns — reads after all of them.
			if b.lastOffset[column]-r.offset > maxTailGapPitches*b.columnPitches[len(b.columnPitches)/2] {
				break
			}
		}
		b.add(r)
		if !b.findGutters() {
			break
		}
		b.kept = append(b.kept[:0], b.gutters...)
		// A gap under half a line is a raised or offset baseline — a
		// superscript, a neighbouring column's staggered line — not the
		// band's pitch, and would make every real line gap look wide.
		if gap >= 0.5*max(b.size, r.size) {
			at, _ := slices.BinarySearch(b.pitches, gap)
			b.pitches = slices.Insert(b.pitches, at, gap)
		}
		switch {
		case column < 0:
			columns = 2
		case column != prevColumn && prevColumn >= 0:
			columns = max(columns, 2)
		default:
			columns = max(columns, 1)
		}
		if column >= 0 {
			for len(b.lastOffset) <= column {
				b.lastOffset = append(b.lastOffset, math.NaN())
			}
			if last := b.lastOffset[column]; !math.IsNaN(last) {
				at, _ := slices.BinarySearch(b.columnPitches, last-r.offset)
				b.columnPitches = slices.Insert(b.columnPitches, at, last-r.offset)
			}
			b.lastOffset[column] = r.offset
		}
		prevColumn = column
	}
	minRows := minBandRows
	if b.explicit {
		minRows = 2
	}
	if end-start < minRows {
		return end, nil // two rows can line up a gap by chance
	}
	return end, b.kept
}

// add merges a row's fragments into the band's coverage.
func (b *bandState) add(r row) {
	b.size = max(b.size, r.size)
	for _, f := range r.fragments {
		span := interval{f.minX, f.maxX}
		i, _ := slices.BinarySearchFunc(b.coverage, span.lo, func(c interval, lo float64) int {
			switch {
			case c.hi < lo:
				return -1
			case c.lo > lo:
				return 1
			}
			return 0
		})
		// Merge with every interval span overlaps, from i on.
		j := i
		for j < len(b.coverage) && b.coverage[j].lo <= span.hi {
			span.lo = min(span.lo, b.coverage[j].lo)
			span.hi = max(span.hi, b.coverage[j].hi)
			j++
		}
		b.coverage = slices.Replace(b.coverage, i, j, span)
	}
}

// findGutters sets the band's gutters — the gaps in its coverage wide
// enough to be one, between columns wide enough to be text — and reports
// whether there are any. A strip of list bullets or line numbers beside
// the text is too narrow to be a column, and its gap is no gutter.
func (b *bandState) findGutters() bool {
	b.gutters = b.gutters[:0]
	minGap := minGutterEm * max(b.size, 1)
	minColumn := minColumnEm * max(b.size, 1)
	columnStart := b.coverage[0].lo
	for k := 1; k < len(b.coverage); k++ {
		lo, hi := b.coverage[k-1].hi, b.coverage[k].lo
		if hi-lo < minGap {
			continue
		}
		if lo-columnStart < minColumn && !b.explicit {
			continue // the column to its left is a marker strip
		}
		b.gutters = append(b.gutters, interval{lo, hi})
		columnStart = hi
	}
	// The last column, too, must be wide enough.
	if n := len(b.gutters); n > 0 && !b.explicit && b.coverage[len(b.coverage)-1].hi-b.gutters[n-1].hi < minColumn {
		b.gutters = b.gutters[:n-1]
	}
	return len(b.gutters) > 0
}

// appendBand appends a band's lines in reading order: column by column
// when it holds side-by-side prose, row by row otherwise.
func appendBand(out []layoutLine, rows []row, gutters []interval, allGutters bool) []layoutLine {
	columns := len(gutters) + 1
	if columns > 1 && !allGutters && !sideBySide(rows, gutters) {
		columns = 1
	}
	if columns == 1 {
		for _, r := range rows {
			out = append(out, r.line)
		}
		return out
	}
	order := make([]int, columns)
	for c := range order {
		order[c] = c
	}
	if bandIsRightToLeft(rows) {
		slices.Reverse(order)
	}
	for _, c := range order {
		for _, r := range rows {
			// A column's fragments on one row join into one line.
			first, last := -1, -1
			for _, f := range r.fragments {
				if columnOf(f, gutters) == c {
					if first < 0 {
						first = f.start
					}
					last = f.end
				}
			}
			if first >= 0 {
				out = append(out, lineFragment(r.line, first, last))
			}
		}
	}
	return out
}

// sideBySide reports whether a band's columns are read one after another:
// at least one holds prose, whose lines must not be interleaved with what
// stands beside them — another column, or a figure's labels. A table's
// columns hold cells, and it reads row by row.
//
// Columns also interleave: moving down the band, rows keep switching
// column or span several, as side-by-side lines do whether or not their
// baselines align. Paragraphs that merely alternate sides — right-aligned
// and left-aligned blocks, one below another — switch only between runs.
func sideBySide(rows []row, gutters []interval) bool {
	if proseColumns(rows, gutters) == 0 {
		return false
	}
	// Runs in one column at either end — a column that starts lower or
	// runs on longer than its neighbour — do not count against it.
	first, last := 0, len(rows)-1
	for first < last && rowColumn(rows[first], gutters) >= 0 &&
		rowColumn(rows[first], gutters) == rowColumn(rows[first+1], gutters) {
		first++
	}
	for last > first && rowColumn(rows[last], gutters) >= 0 &&
		rowColumn(rows[last], gutters) == rowColumn(rows[last-1], gutters) {
		last--
	}
	if last-first < 1 {
		return false // one column alone
	}
	interleaved := 0
	for i := first + 1; i <= last; i++ {
		c, prev := rowColumn(rows[i], gutters), rowColumn(rows[i-1], gutters)
		if c < 0 || prev < 0 || c != prev {
			interleaved++
		}
	}
	return float64(interleaved) >= minInterleave*float64(last-first)
}

// rowColumn returns the column a row's text lies in, or -1 when it spans
// several.
func rowColumn(r row, gutters []interval) int {
	c := columnOf(r.fragments[0], gutters)
	for _, f := range r.fragments[1:] {
		if columnOf(f, gutters) != c {
			return -1
		}
	}
	return c
}

// proseColumns counts the band's columns that hold prose rather than
// table cells.
func proseColumns(rows []row, gutters []interval) int {
	type stats struct {
		fragments, words, letters, chars int
		minX, maxX, size                 float64
		widths                           []float64
	}
	columns := make([]stats, len(gutters)+1)
	for c := range columns {
		columns[c].minX, columns[c].maxX = math.Inf(1), math.Inf(-1)
	}
	for _, r := range rows {
		for _, f := range r.fragments {
			c := &columns[columnOf(f, gutters)]
			c.fragments++
			c.words += f.words
			c.letters += f.letters
			c.chars += f.chars
			c.minX, c.maxX = min(c.minX, f.minX), max(c.maxX, f.maxX)
			c.size = max(c.size, r.size)
			c.widths = append(c.widths, f.maxX-f.minX)
		}
	}
	prose := 0
	for _, c := range columns {
		if c.fragments < minBandRows || c.chars == 0 {
			continue
		}
		width := c.maxX - c.minX
		full := 0
		for _, w := range c.widths {
			if w >= minProseFill*width {
				full++
			}
		}
		if float64(c.words)/float64(c.fragments) >= minProseWords &&
			float64(c.letters)/float64(c.chars) >= minProseLetters &&
			width >= minProseWidthEm*max(c.size, 1) &&
			2*full >= c.fragments {
			prose++
		}
	}
	return prose
}

// columnOf returns the column a fragment falls in: the number of gutters
// to its left.
func columnOf(f fragment, gutters []interval) int {
	c := 0
	for _, g := range gutters {
		if f.minX >= g.hi {
			c++
		}
	}
	return c
}

// bandIsRightToLeft reports whether most of a band's letters are
// right-to-left, whose columns read from the right.
func bandIsRightToLeft(rows []row) bool {
	rtl, ltr := 0, 0
	for _, r := range rows {
		for _, g := range r.line.glyphs {
			for _, ch := range g.Text {
				switch {
				case isRightToLeft(ch):
					rtl++
				case ch >= 'A':
					ltr++
				}
			}
		}
	}
	return rtl > ltr
}
