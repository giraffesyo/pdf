package pdf

import (
	"math"
	"slices"
	"strings"
)

// layoutGlyph refers to a page glyph by pointer rather than copying it: a
// Glyph is 160 bytes, and copying every one into per-line slices doubled
// the allocation volume of text reconstruction. The pointer targets the
// caller's slice, which is not resized while layout runs.
type layoutGlyph struct {
	*Glyph
	index int
}

type layoutLine struct {
	glyphs []layoutGlyph
	dir    Point
	normal Point
	offset float64
	first  int
}

func reconstructPositionText(page Page, columns bool) string {
	if len(page.Glyphs) == 0 {
		return ""
	}
	lines := buildLayoutLines(page.Glyphs)
	if columns {
		lines = orderColumns(lines, page.CropBox)
	} else {
		slices.SortStableFunc(lines, compareLayoutLines)
	}

	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		writeLayoutLine(&b, line)
	}
	return b.String()
}

func buildLayoutLines(glyphs []Glyph) []layoutLine {
	const (
		directionBuckets = 72 // five-degree buckets; exact matching remains below
		offsetCell       = 4.0
		maxLineTolerance = 64.0
	)
	var lines []layoutLine
	lineIndex := make([]map[int][]int, directionBuckets)
	for i, glyph := range glyphs {
		dir := glyphDirection(glyph)
		normal := Point{X: -dir.Y, Y: dir.X}
		start, _ := glyphBaseline(glyph, dir)
		offset := dotPoint(start, normal)
		tol := min(0.55*glyph.Size, maxLineTolerance)
		if tol <= 0 {
			tol = 5
		}
		best := -1
		bestDistance := math.MaxFloat64
		bucket := layoutDirectionBucket(dir, directionBuckets)
		offsetBucket := int(math.Floor(offset / offsetCell))
		offsetRadius := int(math.Ceil(tol/offsetCell)) + 1
		for directionDelta := -3; directionDelta <= 3; directionDelta++ {
			directionBucket := (bucket + directionDelta + directionBuckets) % directionBuckets
			for offsetDelta := -offsetRadius; offsetDelta <= offsetRadius; offsetDelta++ {
				for _, j := range lineIndex[directionBucket][offsetBucket+offsetDelta] {
					if dotPoint(dir, lines[j].dir) < 0.985 {
						continue
					}
					distance := math.Abs(offset - lines[j].offset)
					if distance <= tol && distance < bestDistance {
						best, bestDistance = j, distance
					}
				}
			}
		}
		item := layoutGlyph{Glyph: &glyphs[i], index: i}
		if best < 0 {
			lines = append(lines, layoutLine{
				glyphs: []layoutGlyph{item},
				dir:    dir,
				normal: normal,
				offset: offset,
				first:  i,
			})
			if lineIndex[bucket] == nil {
				lineIndex[bucket] = map[int][]int{}
			}
			key := int(math.Floor(offset / offsetCell))
			lineIndex[bucket][key] = append(lineIndex[bucket][key], len(lines)-1)
			continue
		}
		lines[best].glyphs = append(lines[best].glyphs, item)
	}
	for i := range lines {
		line := &lines[i]
		slices.SortStableFunc(line.glyphs, func(a, b layoutGlyph) int {
			ap := glyphProjection(*a.Glyph, line.dir, false)
			bp := glyphProjection(*b.Glyph, line.dir, false)
			switch {
			case ap < bp:
				return -1
			case ap > bp:
				return 1
			default:
				return a.index - b.index
			}
		})
	}
	return lines
}

func layoutDirectionBucket(dir Point, buckets int) int {
	angle := math.Atan2(dir.Y, dir.X)
	bucket := int(math.Floor((angle + math.Pi) / (2 * math.Pi) * float64(buckets)))
	if bucket >= buckets {
		return 0
	}
	return bucket
}

func compareLayoutLines(a, b layoutLine) int {
	if dotPoint(a.dir, b.dir) >= 0.985 {
		switch {
		case a.offset > b.offset:
			return -1
		case a.offset < b.offset:
			return 1
		}
	}
	return a.first - b.first
}

func writeLayoutLine(b *strings.Builder, line layoutLine) {
	endsSpace := false
	prevEnd := 0.0
	for i, glyph := range line.glyphs {
		start := glyphProjection(*glyph.Glyph, line.dir, false)
		if i > 0 {
			gap := start - prevEnd
			threshold := 0.17 * glyph.Size
			if threshold <= 0 {
				threshold = 1
			}
			startsSpace := strings.HasPrefix(glyph.Text, " ")
			if gap > threshold && !endsSpace && !startsSpace {
				b.WriteByte(' ')
			}
		}
		b.WriteString(glyph.Text)
		endsSpace = strings.HasSuffix(glyph.Text, " ")
		prevEnd = glyphProjection(*glyph.Glyph, line.dir, true)
	}
}

func glyphDirection(g Glyph) Point {
	start, end := g.Baseline.Start, g.Baseline.End
	dx, dy := end.X-start.X, end.Y-start.Y
	if length := math.Hypot(dx, dy); length > 1e-9 {
		return Point{X: dx / length, Y: dy / length}
	}
	if length := math.Hypot(g.Direction.X, g.Direction.Y); length > 1e-9 {
		return Point{X: g.Direction.X / length, Y: g.Direction.Y / length}
	}
	return Point{X: 1}
}

func glyphBaseline(g Glyph, dir Point) (Point, Point) {
	start, end := g.Baseline.Start, g.Baseline.End
	if start != (Point{}) || end != (Point{}) {
		return start, end
	}
	start = Point{X: g.X, Y: g.Y}
	end = Point{X: g.X + dir.X*g.Advance, Y: g.Y + dir.Y*g.Advance}
	return start, end
}

func glyphProjection(g Glyph, dir Point, end bool) float64 {
	start, finish := glyphBaseline(g, dir)
	if end {
		return dotPoint(finish, dir)
	}
	return dotPoint(start, dir)
}

func dotPoint(a, b Point) float64 { return a.X*b.X + a.Y*b.Y }

func orderColumns(lines []layoutLine, box Rect) []layoutLine {
	var fragments []layoutLine
	for _, line := range lines {
		if math.Abs(line.dir.Y) > 0.25 || len(line.glyphs) < 2 {
			fragments = append(fragments, line)
			continue
		}
		start := 0
		for i := 1; i < len(line.glyphs); i++ {
			prev := line.glyphs[i-1]
			current := line.glyphs[i]
			gap := glyphProjection(*current.Glyph, line.dir, false) -
				glyphProjection(*prev.Glyph, line.dir, true)
			size := max(prev.Size, current.Size)
			threshold := max(4*max(size, 1), 0.04*(box.MaxX-box.MinX))
			if gap > threshold {
				fragments = append(fragments, lineFragment(line, start, i))
				start = i
			}
		}
		fragments = append(fragments, lineFragment(line, start, len(line.glyphs)))
	}

	type column struct {
		lines []layoutLine
		x     float64
	}
	type horizontalLine struct {
		line layoutLine
		x    float64
		tol  float64
	}
	var horizontal []horizontalLine
	var other []layoutLine
	for _, line := range fragments {
		if math.Abs(line.dir.Y) > 0.25 || len(line.glyphs) == 0 {
			other = append(other, line)
			continue
		}
		x := line.glyphs[0].X
		tol := 2 * max(line.glyphs[0].Size, 1)
		horizontal = append(horizontal, horizontalLine{line: line, x: x, tol: tol})
	}
	slices.SortStableFunc(horizontal, func(a, b horizontalLine) int {
		switch {
		case a.x < b.x:
			return -1
		case a.x > b.x:
			return 1
		default:
			return a.line.first - b.line.first
		}
	})
	var columns []column
	for _, item := range horizontal {
		if len(columns) == 0 || math.Abs(columns[len(columns)-1].x-item.x) > item.tol {
			columns = append(columns, column{x: item.x, lines: []layoutLine{item.line}})
		} else {
			last := len(columns) - 1
			columns[last].lines = append(columns[last].lines, item.line)
		}
	}
	ordered := make([]layoutLine, 0, len(fragments))
	for _, column := range columns {
		slices.SortStableFunc(column.lines, compareLayoutLines)
		ordered = append(ordered, column.lines...)
	}
	slices.SortStableFunc(other, compareLayoutLines)
	return append(ordered, other...)
}

func lineFragment(line layoutLine, start, end int) layoutLine {
	line.glyphs = line.glyphs[start:end]
	line.first = line.glyphs[0].index
	return line
}

// GlyphsIn returns glyphs whose quadrilateral intersects region.
func (p Page) GlyphsIn(region Rect) []Glyph {
	out := make([]Glyph, 0, len(p.Glyphs))
	for _, glyph := range p.Glyphs {
		if rectsIntersect(glyphRect(glyph), region) {
			out = append(out, glyph)
		}
	}
	return out
}

// TextIn reconstructs text from glyphs intersecting region.
func (p Page) TextIn(region Rect, layout LayoutOptions) string {
	p.Glyphs = p.GlyphsIn(region)
	return p.TextWithOptions(layout)
}

func glyphRect(g Glyph) Rect {
	if g.Quad != (Quad{}) {
		rect := Rect{
			MinX: math.MaxFloat64,
			MinY: math.MaxFloat64,
			MaxX: -math.MaxFloat64,
			MaxY: -math.MaxFloat64,
		}
		for _, point := range g.Quad {
			rect.MinX = min(rect.MinX, point.X)
			rect.MinY = min(rect.MinY, point.Y)
			rect.MaxX = max(rect.MaxX, point.X)
			rect.MaxY = max(rect.MaxY, point.Y)
		}
		return rect
	}
	return Rect{MinX: g.X, MinY: g.Y, MaxX: g.X + g.Advance, MaxY: g.Y + g.Size}
}

func rectsIntersect(a, b Rect) bool {
	return a.MaxX >= b.MinX && b.MaxX >= a.MinX && a.MaxY >= b.MinY && b.MaxY >= a.MinY
}
