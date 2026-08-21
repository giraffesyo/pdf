package pdf

import (
	"bytes"
	"errors"
	"fmt"
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
	dict map[string]operand
}

const (
	maxOperandNest = 64
	maxStackDepth  = 4096
)

// readStreamBounded reads a stream's decoded bytes with a total-size cap
// and a stall guard (a filter reader that keeps returning (0, nil) would
// otherwise loop io.ReadAll forever). It returns any decoded prefix together
// with a non-nil error when decoding fails or the limit is exceeded.
func readStreamBoundedLimitError(v object.Value, limit int) ([]byte, error) {
	if v.Kind() != object.Stream {
		return nil, errors.New("content value is not a stream")
	}
	rc, err := v.Reader()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }() // read-only handle
	return safeio.ReadAllGuardedLimitError(rc, limit)
}

// contentBytes returns the page's full content: /Contents may be a single
// stream or an array of streams that form one logical stream.
func contentBytesLimit(contents object.Value, limit int) []byte {
	data, _ := contentBytesLimitError(contents, limit)
	return data
}

func contentBytesLimitError(contents object.Value, limit int) ([]byte, []error) {
	if limit <= 0 {
		return nil, nil
	}
	if contents.Kind() == object.Array {
		var data []byte
		var errs []error
		processed := 0
		for i := 0; i < contents.Len() && len(data) < limit; i++ {
			processed = i + 1
			remaining := limit - len(data)
			part, err := readStreamBoundedLimitError(contents.Index(i), remaining)
			data = append(data, part...)
			if err != nil {
				errs = append(errs, fmt.Errorf("content stream %d: %w", i+1, err))
			}
			if len(data) < limit {
				data = append(data, '\n')
			}
		}
		if processed < contents.Len() {
			errs = appendLimitError(errs)
		}
		return data, errs
	}
	data, err := readStreamBoundedLimitError(contents, limit)
	if err != nil {
		return data, []error{err}
	}
	return data, nil
}

func appendLimitError(errs []error) []error {
	for _, err := range errs {
		if errors.Is(err, safeio.ErrLimitExceeded) {
			return errs
		}
	}
	return append(errs, safeio.ErrLimitExceeded)
}

type contentLexer struct {
	data []byte
	i    int
	err  error

	// arrays holds the element buffers of top-level array operands from
	// operators that have already run, for reuse. TJ arrays are long and
	// frequent, and growing a fresh slice for each one was the lexer's
	// main allocation. Only operand-stack arrays are pooled: arrays nested
	// in a dictionary may outlive the operator (BDC properties), and every
	// consumer reads a stack array before the operator returns.
	arrays [][]operand
}

// recycle returns the array buffers of a completed operator's operands
// to the pool.
func (lx *contentLexer) recycle(stack []operand) {
	for _, op := range stack {
		if op.kind == opArr && cap(op.arr) > 0 {
			lx.arrays = append(lx.arrays, op.arr[:0])
		}
	}
}

func (lx *contentLexer) setErr(err error) {
	if lx.err == nil {
		lx.err = err
	}
}

// interpretContent executes the operator stream: operands accumulate on a
// stack; each operator keyword invokes do and clears the stack.
func interpretContent(data []byte, do func(op []byte, args []operand)) {
	_ = interpretContentError(data, func(op []byte, args []operand) error {
		do(op, args)
		return nil
	})
}

func interpretContentError(data []byte, do func(op []byte, args []operand) error) error {
	lx := &contentLexer{data: data}
	var stack []operand
	for lx.i < len(lx.data) {
		lx.skipSpace()
		if lx.i >= len(lx.data) {
			return lx.err
		}
		c := lx.data[lx.i]
		switch {
		case c == '/', c == '(', c == '<', c == '[', c == '{',
			c >= '0' && c <= '9', c == '+', c == '-', c == '.':
			if v, ok := lx.readOperand(0); ok {
				if len(stack) < maxStackDepth {
					stack = append(stack, v)
				} else {
					lx.setErr(errors.New("content operand stack exceeds limit"))
				}
			}
		case c == ']' || c == '>' || c == ')' || c == '}':
			lx.setErr(fmt.Errorf("stray content delimiter %q", c))
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
				lx.recycle(stack)
				stack = stack[:0]
			default:
				if err := do(kw, stack); err != nil {
					return err
				}
				lx.recycle(stack)
				stack = stack[:0]
			}
		}
	}
	return lx.err
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
		lx.setErr(errors.New("content operand nesting exceeds limit"))
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
	lx.setErr(errors.New("unterminated literal string in content stream"))
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
	} else {
		lx.setErr(errors.New("unterminated hexadecimal string in content stream"))
	}
	if haveHi {
		out = append(out, hi<<4)
	}
	return out
}

func (lx *contentLexer) readArray(depth int) (operand, bool) {
	lx.i++ // consume '['
	arr := operand{kind: opArr}
	if depth == 0 {
		if n := len(lx.arrays); n > 0 {
			arr.arr = lx.arrays[n-1]
			lx.arrays = lx.arrays[:n-1]
		}
	}
	closed := false
	for lx.i < len(lx.data) {
		lx.skipSpace()
		if lx.i >= len(lx.data) {
			break
		}
		if lx.data[lx.i] == ']' {
			lx.i++
			closed = true
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
	if !closed {
		lx.setErr(errors.New("unterminated array in content stream"))
	}
	return arr, true
}

func (lx *contentLexer) readDict(depth int) (operand, bool) {
	lx.i += 2 // consume '<<'
	out := operand{kind: opDict, dict: map[string]operand{}}
	for lx.i < len(lx.data) {
		lx.skipSpace()
		if lx.i+1 < len(lx.data) && lx.data[lx.i] == '>' && lx.data[lx.i+1] == '>' {
			lx.i += 2
			return out, true
		}
		if lx.i >= len(lx.data) {
			break
		}
		before := lx.i
		switch lx.data[lx.i] {
		case '/':
			key := lx.readName()
			lx.skipSpace()
			if lx.i < len(lx.data) && (lx.data[lx.i] == '/' || lx.data[lx.i] == '(' ||
				lx.data[lx.i] == '<' || lx.data[lx.i] == '[' || lx.data[lx.i] == '{' ||
				lx.data[lx.i] == '+' || lx.data[lx.i] == '-' || lx.data[lx.i] == '.' ||
				lx.data[lx.i] >= '0' && lx.data[lx.i] <= '9') {
				if value, ok := lx.readOperand(depth + 1); ok {
					out.dict[key] = value
				}
			} else if kw := lx.readKeyword(); len(kw) > 0 {
				switch string(kw) {
				case "true", "false":
					out.dict[key] = operand{kind: opBool}
				case "null":
					out.dict[key] = operand{kind: opNull}
				}
			}
		case '(', '<', '[', '{':
			_, _ = lx.readOperand(depth + 1)
		default:
			lx.readKeyword()
		}
		if lx.i == before {
			lx.i++
		}
	}
	lx.setErr(errors.New("unterminated dictionary in content stream"))
	return out, true
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
	lx.setErr(errors.New("unterminated procedure in content stream"))
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
	foundData := false
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
			foundData = true
			break
		}
		if lx.i == before {
			lx.i++
		}
	}
	if !foundData {
		lx.setErr(errors.New("inline image missing ID marker"))
		return
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
	lx.setErr(errors.New("inline image missing EI marker"))
}
