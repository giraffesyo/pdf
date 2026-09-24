package function

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/giraffesyo/pdf/internal/object"
)

// A type 4 function is a PostScript calculator program (ISO 32000-1
// §7.10.5): arithmetic, relational, boolean and stack operators, with
// if and ifelse as the only control flow. With no loops, running a
// program costs at most its length.

const (
	maxProgram = 64 << 10 // instructions
	maxStack   = 100      // the operand stack depth PostScript specifies
)

type opcode uint8

const (
	opPush  opcode = iota // push the literal
	opJumpF               // pop a boolean; jump to arg if false
	opJump                // jump to arg
	opAbs
	opAdd
	opAtan
	opCeiling
	opCos
	opCvi
	opCvr
	opDiv
	opExp
	opFloor
	opIdiv
	opLn
	opLog
	opMod
	opMul
	opNeg
	opRound
	opSin
	opSqrt
	opSub
	opTruncate
	opAnd
	opBitshift
	opEq
	opGe
	opGt
	opLe
	opLt
	opNe
	opNot
	opOr
	opXor
	opCopy
	opDup
	opExch
	opIndex
	opPop
	opRoll
)

var operators = map[string]opcode{
	"abs": opAbs, "add": opAdd, "atan": opAtan, "ceiling": opCeiling,
	"cos": opCos, "cvi": opCvi, "cvr": opCvr, "div": opDiv, "exp": opExp,
	"floor": opFloor, "idiv": opIdiv, "ln": opLn, "log": opLog, "mod": opMod,
	"mul": opMul, "neg": opNeg, "round": opRound, "sin": opSin,
	"sqrt": opSqrt, "sub": opSub, "truncate": opTruncate,
	"and": opAnd, "bitshift": opBitshift, "eq": opEq, "ge": opGe, "gt": opGt,
	"le": opLe, "lt": opLt, "ne": opNe, "not": opNot, "or": opOr, "xor": opXor,
	"copy": opCopy, "dup": opDup, "exch": opExch, "index": opIndex,
	"pop": opPop, "roll": opRoll,
}

// value is a calculator operand: a number, flagged integer or boolean.
type value struct {
	f    float64
	kind uint8
}

const (
	kindReal uint8 = iota
	kindInt
	kindBool
)

type instr struct {
	op  opcode
	arg int   // jump target
	lit value // opPush
}

func (f *Function) parseCalculator(v object.Value) error {
	if f.nout == 0 {
		return errors.New("function: calculator function without /Range")
	}
	src, err := readStream(v)
	if err != nil {
		return err
	}
	p := &psParser{src: src}
	p.skipSpace()
	if !p.consume('{') {
		return errors.New("function: calculator program does not start with {")
	}
	if err := p.block(0); err != nil {
		return err
	}
	f.prog = p.prog
	f.eval = (*Function).evalCalculator
	return nil
}

type psParser struct {
	src  []byte
	pos  int
	prog []instr
}

func (p *psParser) skipSpace() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '%':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' && p.src[p.pos] != '\r' {
				p.pos++
			}
		case ' ', '\t', '\n', '\r', '\f', 0:
			p.pos++
		default:
			return
		}
	}
}

func (p *psParser) consume(c byte) bool {
	if p.pos < len(p.src) && p.src[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

func (p *psParser) emit(in instr) error {
	if len(p.prog) >= maxProgram {
		return errors.New("function: calculator program too long")
	}
	p.prog = append(p.prog, in)
	return nil
}

// block compiles instructions up to the '}' closing the current
// procedure. A procedure followed by if or ifelse compiles to jumps.
func (p *psParser) block(depth int) error {
	if depth > 64 {
		return errors.New("function: calculator procedures nested too deeply")
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			return errors.New("function: calculator program ends inside a procedure")
		}
		switch c := p.src[p.pos]; c {
		case '}':
			p.pos++
			return nil
		case '{':
			p.pos++
			if err := p.conditional(depth); err != nil {
				return err
			}
		default:
			tok := p.token()
			if tok == "" {
				return fmt.Errorf("function: unexpected byte %q in calculator program", c)
			}
			if err := p.word(tok); err != nil {
				return err
			}
		}
	}
}

// conditional compiles "{ a } if" or "{ a } { b } ifelse"; the first '{'
// is consumed.
func (p *psParser) conditional(depth int) error {
	jumpF := len(p.prog)
	if err := p.emit(instr{op: opJumpF}); err != nil {
		return err
	}
	if err := p.block(depth + 1); err != nil {
		return err
	}
	p.skipSpace()
	if p.consume('{') {
		jump := len(p.prog)
		if err := p.emit(instr{op: opJump}); err != nil {
			return err
		}
		p.prog[jumpF].arg = len(p.prog)
		if err := p.block(depth + 1); err != nil {
			return err
		}
		p.prog[jump].arg = len(p.prog)
		p.skipSpace()
		if p.token() != "ifelse" {
			return errors.New("function: two procedures not followed by ifelse")
		}
		return nil
	}
	p.prog[jumpF].arg = len(p.prog)
	if p.token() != "if" {
		return errors.New("function: procedure not followed by if")
	}
	return nil
}

func (p *psParser) token() string {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == 0 || c == '{' || c == '}' || c == '%' {
			break
		}
		p.pos++
	}
	return string(p.src[start:p.pos])
}

func (p *psParser) word(tok string) error {
	switch tok {
	case "true":
		return p.emit(instr{op: opPush, lit: value{f: 1, kind: kindBool}})
	case "false":
		return p.emit(instr{op: opPush, lit: value{kind: kindBool}})
	}
	if op, ok := operators[tok]; ok {
		return p.emit(instr{op: op})
	}
	if n, err := strconv.ParseInt(tok, 10, 32); err == nil {
		return p.emit(instr{op: opPush, lit: value{f: float64(n), kind: kindInt}})
	}
	if x, err := strconv.ParseFloat(tok, 64); err == nil && !math.IsInf(x, 0) && !math.IsNaN(x) {
		return p.emit(instr{op: opPush, lit: value{f: x}})
	}
	return fmt.Errorf("function: unknown calculator operator %q", tok)
}

// evalCalculator runs the program. Stack underflow, overflow and type
// errors stop it; the outputs are then whatever the stack holds, zero if
// too little.
func (f *Function) evalCalculator(in, out []float64) {
	var st calcStack
	for _, x := range in {
		st.push(value{f: x})
	}
	st.run(f.prog)
	n := min(len(out), f.nout)
	for j := range n {
		out[j] = 0
		if k := st.n - n + j; k >= 0 {
			out[j] = st.v[k].f
		}
	}
}

type calcStack struct {
	v   [maxStack]value
	n   int
	bad bool
}

func (s *calcStack) push(v value) {
	if s.n == maxStack {
		s.bad = true
		return
	}
	if math.IsNaN(v.f) || math.IsInf(v.f, 0) {
		v.f = 0
	}
	s.v[s.n] = v
	s.n++
}

func (s *calcStack) pop() value {
	if s.n == 0 {
		s.bad = true
		return value{}
	}
	s.n--
	return s.v[s.n]
}

func num(x float64) value { return value{f: x} }

func integer(x float64) value {
	return value{f: math.Trunc(clip(x, math.MinInt32, math.MaxInt32)), kind: kindInt}
}

func boolean(b bool) value {
	if b {
		return value{f: 1, kind: kindBool}
	}
	return value{kind: kindBool}
}

// arith keeps integer results integral when both operands are.
func arith(a, b value, x float64) value {
	if a.kind == kindInt && b.kind == kindInt && math.Abs(x) <= math.MaxInt32 {
		return value{f: x, kind: kindInt}
	}
	return num(x)
}

func (s *calcStack) run(prog []instr) {
	for pc := 0; pc < len(prog) && !s.bad; pc++ {
		in := prog[pc]
		switch in.op {
		case opPush:
			s.push(in.lit)
		case opJumpF:
			if s.pop().f == 0 {
				pc = in.arg - 1
			}
		case opJump:
			pc = in.arg - 1
		case opAbs:
			a := s.pop()
			s.push(value{f: math.Abs(a.f), kind: a.kind})
		case opNeg:
			a := s.pop()
			s.push(value{f: -a.f, kind: a.kind})
		case opAdd:
			b, a := s.pop(), s.pop()
			s.push(arith(a, b, a.f+b.f))
		case opSub:
			b, a := s.pop(), s.pop()
			s.push(arith(a, b, a.f-b.f))
		case opMul:
			b, a := s.pop(), s.pop()
			s.push(arith(a, b, a.f*b.f))
		case opDiv:
			b, a := s.pop(), s.pop()
			if b.f == 0 {
				s.bad = true
				break
			}
			s.push(num(a.f / b.f))
		case opIdiv, opMod:
			b, a := s.pop(), s.pop()
			x, y := math.Trunc(a.f), math.Trunc(b.f)
			if y == 0 {
				s.bad = true
				break
			}
			if in.op == opIdiv {
				s.push(integer(math.Trunc(x / y)))
			} else {
				s.push(integer(math.Mod(x, y)))
			}
		case opAtan:
			b, a := s.pop(), s.pop()
			d := math.Atan2(a.f, b.f) * 180 / math.Pi
			if d < 0 {
				d += 360
			}
			s.push(num(d))
		case opCos:
			s.push(num(math.Cos(s.pop().f * math.Pi / 180)))
		case opSin:
			s.push(num(math.Sin(s.pop().f * math.Pi / 180)))
		case opExp:
			b, a := s.pop(), s.pop()
			s.push(num(math.Pow(a.f, b.f)))
		case opLn:
			s.push(num(math.Log(s.pop().f)))
		case opLog:
			s.push(num(math.Log10(s.pop().f)))
		case opSqrt:
			s.push(num(math.Sqrt(s.pop().f)))
		case opCeiling:
			a := s.pop()
			s.push(value{f: math.Ceil(a.f), kind: a.kind})
		case opFloor:
			a := s.pop()
			s.push(value{f: math.Floor(a.f), kind: a.kind})
		case opRound:
			a := s.pop()
			s.push(value{f: math.Floor(a.f + 0.5), kind: a.kind})
		case opTruncate:
			a := s.pop()
			s.push(value{f: math.Trunc(a.f), kind: a.kind})
		case opCvi:
			s.push(integer(s.pop().f))
		case opCvr:
			s.push(num(s.pop().f))
		case opAnd, opOr, opXor:
			b, a := s.pop(), s.pop()
			x, y := int64(a.f), int64(b.f)
			var r int64
			switch in.op {
			case opAnd:
				r = x & y
			case opOr:
				r = x | y
			default:
				r = x ^ y
			}
			if a.kind == kindBool && b.kind == kindBool {
				s.push(boolean(r != 0))
			} else {
				s.push(integer(float64(r)))
			}
		case opNot:
			a := s.pop()
			if a.kind == kindBool {
				s.push(boolean(a.f == 0))
			} else {
				s.push(integer(float64(^int64(a.f))))
			}
		case opBitshift:
			b, a := s.pop(), s.pop()
			x, sh := int32(a.f), int(b.f)
			switch {
			case sh >= 32 || sh <= -32:
				s.push(integer(0))
			case sh >= 0:
				s.push(integer(float64(x << sh)))
			default:
				s.push(integer(float64(x >> -sh)))
			}
		case opEq, opNe, opGe, opGt, opLe, opLt:
			b, a := s.pop(), s.pop()
			var r bool
			switch in.op {
			case opEq:
				r = a.f == b.f
			case opNe:
				r = a.f != b.f
			case opGe:
				r = a.f >= b.f
			case opGt:
				r = a.f > b.f
			case opLe:
				r = a.f <= b.f
			default:
				r = a.f < b.f
			}
			s.push(boolean(r))
		case opDup:
			a := s.pop()
			s.push(a)
			s.push(a)
		case opExch:
			b, a := s.pop(), s.pop()
			s.push(b)
			s.push(a)
		case opPop:
			s.pop()
		case opCopy:
			n := int(s.pop().f)
			if n < 0 || n > s.n || s.n+n > maxStack {
				s.bad = true
				break
			}
			copy(s.v[s.n:], s.v[s.n-n:s.n])
			s.n += n
		case opIndex:
			n := int(s.pop().f)
			if n < 0 || n >= s.n {
				s.bad = true
				break
			}
			s.push(s.v[s.n-1-n])
		case opRoll:
			j, n := int(s.pop().f), int(s.pop().f)
			if n < 0 || n > s.n {
				s.bad = true
				break
			}
			if n == 0 {
				break
			}
			j %= n
			if j < 0 {
				j += n
			}
			seg := s.v[s.n-n : s.n]
			rotate(seg, j)
		}
	}
}

// rotate moves the top j values of seg to its bottom: PostScript roll with
// a positive count.
func rotate(seg []value, j int) {
	n := len(seg)
	reverse(seg)
	reverse(seg[:j])
	reverse(seg[j:n])
}

func reverse(v []value) {
	for i, k := 0, len(v)-1; i < k; i, k = i+1, k-1 {
		v[i], v[k] = v[k], v[i]
	}
}
