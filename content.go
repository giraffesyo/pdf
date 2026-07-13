package pdf

import (
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
	if v.Kind() != object.Stream {
		return nil
	}
	rc, err := v.Reader()
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }() // read-only handle
	return safeio.ReadAllGuarded(rc)
}

// contentBytes returns the page's full content: /Contents may be a single
// stream or an array of streams that form one logical stream.
func contentBytes(contents object.Value) []byte {
	if contents.Kind() == object.Array {
		var data []byte
		for i := 0; i < contents.Len() && len(data) < maxStreamBytes; i++ {
			data = append(data, readStreamBounded(contents.Index(i))...)
			data = append(data, '\n')
		}
		return data
	}
	return readStreamBounded(contents)
}

type contentLexer struct {
	data []byte
	i    int
}

// interpretContent executes the operator stream: operands accumulate on a
// stack; each operator keyword invokes do and clears the stack.
func interpretContent(data []byte, do func(op string, args []operand)) {
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
			switch kw {
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
	if depth > maxOperandNest || lx.i >= len(lx.data) {
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
	name := string(lx.data[start:lx.i])
	// Decode #xx escapes if present.
	if idx := indexByte(name, '#'); idx >= 0 {
		var b []byte
		for j := 0; j < len(name); j++ {
			if name[j] == '#' && j+2 < len(name) {
				if hi, ok1 := hexVal(name[j+1]); ok1 {
					if lo, ok2 := hexVal(name[j+2]); ok2 {
						b = append(b, hi<<4|lo)
						j += 2
						continue
					}
				}
			}
			b = append(b, name[j])
		}
		name = string(b)
	}
	return name
}

func indexByte(s string, c byte) int {
	for i := range len(s) {
		if s[i] == c {
			return i
		}
	}
	return -1
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
	var out []byte
	depth := 1
	for lx.i < len(lx.data) {
		c := lx.data[lx.i]
		switch c {
		case '\\':
			lx.i++
			if lx.i >= len(lx.data) {
				return out
			}
			e := lx.data[lx.i]
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
				if lx.i+1 < len(lx.data) && lx.data[lx.i+1] == '\n' {
					lx.i++
				}
			default:
				if e >= '0' && e <= '7' { // 1-3 octal digits
					v := int(e - '0')
					for k := 0; k < 2 && lx.i+1 < len(lx.data); k++ {
						nx := lx.data[lx.i+1]
						if nx < '0' || nx > '7' {
							break
						}
						v = v*8 + int(nx-'0')
						lx.i++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
			lx.i++
		case '(':
			depth++
			out = append(out, c)
			lx.i++
		case ')':
			depth--
			lx.i++
			if depth == 0 {
				return out
			}
			out = append(out, c)
		default:
			out = append(out, c)
			lx.i++
		}
	}
	return out
}

func (lx *contentLexer) readHexString() []byte {
	lx.i++ // consume '<'
	var digits []byte
	for lx.i < len(lx.data) && lx.data[lx.i] != '>' {
		if _, ok := hexVal(lx.data[lx.i]); ok {
			digits = append(digits, lx.data[lx.i])
		}
		lx.i++
	}
	lx.i++ // consume '>'
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	for j := range out {
		hi, _ := hexVal(digits[2*j])
		lo, _ := hexVal(digits[2*j+1])
		out[j] = hi<<4 | lo
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

func (lx *contentLexer) readKeyword() string {
	start := lx.i
	for lx.i < len(lx.data) && !isPDFSpace(lx.data[lx.i]) && !isDelim(lx.data[lx.i]) {
		lx.i++
	}
	if lx.i == start {
		return ""
	}
	return string(lx.data[start:lx.i])
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
		} else if kw := lx.readKeyword(); kw == "ID" {
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
