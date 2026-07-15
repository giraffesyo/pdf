package pdf

import (
	"bytes"
	"strconv"

	"github.com/giraffesyo/pdf/internal/object"
	"github.com/giraffesyo/pdf/internal/safeio"
)

// This file lexes and executes raw content-stream bytes. The library's own
// Interpret is not used because it interprets each /Contents array element
// as an isolated stream (an array token split across elements spins its
// lexer forever at EOF) and it cannot skip inline-image (BI..EI) binary
// data. Lexing concatenated in-memory bytes cannot loop: the cursor only
// advances.

type opKind uint8

const (
	opNum opKind = iota
	opStr
	opName
	opBool
	opNull
	opArr
	opDict
	opProc
)

type operand struct {
	kind opKind
	num  float64
	str  []byte
	name string
	arr  []operand
}

const (
	maxStreamBytes = safeio.MaxStreamBytes
	maxOperandNest = 64
	maxStackDepth  = 4096
)

// readStreamBounded reads a stream's decoded bytes with a total-size cap
// and a stall guard (a filter reader that keeps returning (0, nil) would
// otherwise loop io.ReadAll forever). A non-stream value, or a stream
// whose filter chain errors, yields nil.
func readStreamBounded(v object.Value) []byte {
	return readStreamBoundedLimit(v, maxStreamBytes)
}

func readStreamBoundedLimit(v object.Value, limit int) []byte {
	if v.Kind() != object.Stream {
		return nil
	}
	rc, err := v.Reader()
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }() // read-only handle
	return safeio.ReadAllGuardedLimit(rc, limit)
}

// contentBytes returns the page's full content: /Contents may be a single
// stream or an array of streams that form one logical stream.
func contentBytes(contents object.Value) []byte {
	return contentBytesLimit(contents, maxStreamBytes)
}

func contentBytesLimit(contents object.Value, limit int) []byte {
	if limit <= 0 {
		return nil
	}
	if contents.Kind() == object.Array {
		var data []byte
		for i := 0; i < contents.Len() && len(data) < limit; i++ {
			remaining := limit - len(data)
			data = append(data, readStreamBoundedLimit(contents.Index(i), remaining)...)
			if len(data) < limit {
				data = append(data, '\n')
			}
		}
		return data
	}
	return readStreamBoundedLimit(contents, limit)
}

type contentLexer struct {
	data []byte
	i    int
}

// interpretContent executes the operator stream: operands accumulate on a
// stack; each operator keyword invokes do and clears the stack.
func interpretContent(data []byte, do func(op []byte, args []operand)) {
	lx := &contentLexer{data: data}
	var stack []operand
	for lx.i < len(lx.data) {
		lx.skipSpace()
		if lx.i >= len(lx.data) {
			return
		}
		c := lx.data[lx.i]
		switch {
		case c == '/', c == '(', c == '<', c == '[', c == '{',
			c >= '0' && c <= '9', c == '+', c == '-', c == '.':
			if v, ok := lx.readOperand(0); ok {
				if len(stack) < maxStackDepth {
					stack = append(stack, v)
				}
			}
		case c == ']' || c == '>' || c == ')' || c == '}':
			lx.i++ // stray closer in malformed content
		default:
			kw := lx.readKeyword()
			switch string(kw) {
			case "":
				lx.i++
			case "true", "false":
				stack = append(stack, operand{kind: opBool})
			case "null":
				stack = append(stack, operand{kind: opNull})
			case "BI":
				lx.skipInlineImage()
				stack = stack[:0]
			default:
				do(kw, stack)
				stack = stack[:0]
			}
		}
	}
}

func (lx *contentLexer) skipSpace() {
	for lx.i < len(lx.data) {
		c := lx.data[lx.i]
		if c == '%' { // comment to end of line
			for lx.i < len(lx.data) && lx.data[lx.i] != '\n' && lx.data[lx.i] != '\r' {
				lx.i++
			}
			continue
		}
		if !isPDFSpace(c) {
			return
		}
		lx.i++
	}
}

func isPDFSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == 0
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// readOperand parses one object starting at the cursor. Returns ok=false
// only for tokens that produce no value (e.g. a lone ">>").
func (lx *contentLexer) readOperand(depth int) (operand, bool) {
	if lx.i >= len(lx.data) {
		return operand{}, false
	}
	if depth > maxOperandNest {
		lx.i++
		return operand{}, false
	}
	switch lx.data[lx.i] {
	case '/':
		return operand{kind: opName, name: lx.readName()}, true
	case '(':
		return operand{kind: opStr, str: lx.readLiteralString()}, true
	case '<':
		if lx.i+1 < len(lx.data) && lx.data[lx.i+1] == '<' {
			return lx.readDict(depth)
		}
		return operand{kind: opStr, str: lx.readHexString()}, true
	case '[':
		return lx.readArray(depth)
	case '{':
		return lx.readProc(), true
	default:
		return lx.readNumber()
	}
}

func (lx *contentLexer) readName() string {
	lx.i++ // consume '/'
	start := lx.i
	for lx.i < len(lx.data) && !isPDFSpace(lx.data[lx.i]) && !isDelim(lx.data[lx.i]) {
		lx.i++
	}
	raw := lx.data[start:lx.i]
	// Decode #xx escapes if present.
	if bytes.IndexByte(raw, '#') >= 0 {
		b := make([]byte, 0, len(raw))
		for j := 0; j < len(raw); j++ {
			if raw[j] == '#' && j+2 < len(raw) {
				if hi, ok1 := hexVal(raw[j+1]); ok1 {
					if lo, ok2 := hexVal(raw[j+2]); ok2 {
						b = append(b, hi<<4|lo)
						j += 2
						continue
					}
				}
			}
			b = append(b, raw[j])
		}
		return string(b)
	}
	return string(raw)
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func (lx *contentLexer) readLiteralString() []byte {
	lx.i++ // consume '('
	start := lx.i
	escaped := false
	depth := 1
	for lx.i < len(lx.data) {
		switch lx.data[lx.i] {
		case '\\':
			escaped = true
			lx.i++
			if lx.i < len(lx.data) {
				lx.i++ // escaped delimiters do not affect nesting
			}
		case '(':
			depth++
			lx.i++
		case ')':
			depth--
			if depth == 0 {
				raw := lx.data[start:lx.i]
				lx.i++
				if escaped {
					return decodeLiteralString(raw)
				}
				return raw
			}
			lx.i++
		default:
			lx.i++
		}
	}
	raw := lx.data[start:]
	if escaped {
		return decodeLiteralString(raw)
	}
	return raw
}

func decodeLiteralString(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			out = append(out, raw[i])
			continue
		}
		i++
		if i >= len(raw) {
			break
		}
		e := raw[i]
		switch e {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case '\n': // line continuation
		case '\r':
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
		default:
			if e >= '0' && e <= '7' { // 1-3 octal digits
				v := int(e - '0')
				for range 2 {
					if i+1 >= len(raw) || raw[i+1] < '0' || raw[i+1] > '7' {
						break
					}
					v = v*8 + int(raw[i+1]-'0')
					i++
				}
				out = append(out, byte(v))
			} else {
				out = append(out, e)
			}
		}
	}
	return out
}

func (lx *contentLexer) readHexString() []byte {
	lx.i++ // consume '<'
	encodedLen := len(lx.data) - lx.i
	if end := bytes.IndexByte(lx.data[lx.i:], '>'); end >= 0 {
		encodedLen = end
	}
	out := make([]byte, 0, min((encodedLen+1)/2, 256))
	var hi byte
	haveHi := false
	for lx.i < len(lx.data) && lx.data[lx.i] != '>' {
		if v, ok := hexVal(lx.data[lx.i]); ok {
			if haveHi {
				out = append(out, hi<<4|v)
				haveHi = false
			} else {
				hi = v
				haveHi = true
			}
		}
		lx.i++
	}
	if lx.i < len(lx.data) {
		lx.i++ // consume '>'
	}
	if haveHi {
		out = append(out, hi<<4)
	}
	return out
}

func (lx *contentLexer) readArray(depth int) (operand, bool) {
	lx.i++ // consume '['
	arr := operand{kind: opArr}
	for lx.i < len(lx.data) {
		lx.skipSpace()
		if lx.i >= len(lx.data) {
			break
		}
		if lx.data[lx.i] == ']' {
			lx.i++
			break
		}
		before := lx.i
		if v, ok := lx.readOperand(depth + 1); ok && len(arr.arr) < maxStackDepth {
			arr.arr = append(arr.arr, v)
		}
		if lx.i == before {
			lx.i++ // always advance
		}
	}
	return arr, true
}

// readDict consumes a << ... >> dictionary, discarding its contents.
func (lx *contentLexer) readDict(depth int) (operand, bool) {
	lx.i += 2 // consume '<<'
	for lx.i < len(lx.data) {
		lx.skipSpace()
		if lx.i+1 < len(lx.data) && lx.data[lx.i] == '>' && lx.data[lx.i+1] == '>' {
			lx.i += 2
			return operand{kind: opDict}, true
		}
		if lx.i >= len(lx.data) {
			break
		}
		before := lx.i
		if lx.data[lx.i] == '/' || lx.data[lx.i] == '(' || lx.data[lx.i] == '<' ||
			lx.data[lx.i] == '[' || lx.data[lx.i] == '{' {
			lx.readOperand(depth + 1)
		} else {
			lx.readKeyword()
		}
		if lx.i == before {
			lx.i++
		}
	}
	return operand{kind: opDict}, true
}

// readProc consumes a { ... } PostScript procedure (Type 4 functions).
func (lx *contentLexer) readProc() operand {
	depth := 0
	for lx.i < len(lx.data) {
		switch lx.data[lx.i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				lx.i++
				return operand{kind: opProc}
			}
		case '(':
			lx.readLiteralString()
			continue
		}
		lx.i++
	}
	return operand{kind: opProc}
}

func (lx *contentLexer) readNumber() (operand, bool) {
	start := lx.i
	for lx.i < len(lx.data) && !isPDFSpace(lx.data[lx.i]) && !isDelim(lx.data[lx.i]) {
		lx.i++
	}
	tok := string(lx.data[start:lx.i])
	if lx.i == start {
		lx.i++
		return operand{}, false
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return operand{}, false
	}
	return operand{kind: opNum, num: f}, true
}

func (lx *contentLexer) readKeyword() []byte {
	start := lx.i
	for lx.i < len(lx.data) && !isPDFSpace(lx.data[lx.i]) && !isDelim(lx.data[lx.i]) {
		lx.i++
	}
	if lx.i == start {
		return nil
	}
	return lx.data[start:lx.i]
}

// skipInlineImage consumes everything from after BI through the EI marker:
// a dictionary, the ID keyword, then raw binary data no lexer can parse.
func (lx *contentLexer) skipInlineImage() {
	// Consume dictionary entries until the ID keyword.
	for lx.i < len(lx.data) {
		lx.skipSpace()
		if lx.i >= len(lx.data) {
			return
		}
		before := lx.i
		if lx.data[lx.i] == '/' || lx.data[lx.i] == '(' || lx.data[lx.i] == '<' || lx.data[lx.i] == '[' {
			lx.readOperand(0)
		} else if kw := lx.readKeyword(); len(kw) == 2 && kw[0] == 'I' && kw[1] == 'D' {
			lx.i++ // the single whitespace byte after ID
			break
		}
		if lx.i == before {
			lx.i++
		}
	}
	// Scan for whitespace-delimited EI.
	for ; lx.i+1 < len(lx.data); lx.i++ {
		if lx.data[lx.i] != 'E' || lx.data[lx.i+1] != 'I' {
			continue
		}
		wsBefore := lx.i == 0 || isPDFSpace(lx.data[lx.i-1])
		after := lx.i + 2
		wsAfter := after >= len(lx.data) || isPDFSpace(lx.data[after]) || isDelim(lx.data[after])
		if wsBefore && wsAfter {
			lx.i = after
			return
		}
	}
	lx.i = len(lx.data)
}
