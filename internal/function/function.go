// Package function evaluates PDF functions (ISO 32000-1 §7.10): sampled
// (type 0), exponential interpolation (type 2), stitching (type 3) and
// PostScript calculator (type 4). The extractor needs them for the tint
// transforms of Separation and DeviceN colour spaces, which map colorant
// tints to an alternate space.
//
// Parse validates a function once; Eval never fails and never panics. A
// function is immutable after Parse and safe for concurrent use.
package function

import (
	"errors"
	"fmt"
	"math"

	"github.com/giraffesyo/pdf/internal/object"
	"github.com/giraffesyo/pdf/internal/safeio"
)

// Bounds on what Parse accepts, far beyond real tint transforms.
const (
	maxInputs      = 16       // DeviceN allows 32 colorants; a sampled table over 16 is already absurd
	maxOutputs     = 32       //
	maxSamples     = 16 << 20 // sample values in a type 0 table
	maxStreamBytes = 16 << 20 // a type 0 or type 4 stream
	maxDepth       = 8        // nested type 3 functions
)

// Function is a parsed PDF function.
type Function struct {
	domain []float64 // 2 per input
	rng    []float64 // 2 per output; nil for type 2 and 3 without /Range
	eval   func(f *Function, in, out []float64)
	nout   int

	// type 0
	size   []int
	encode []float64
	decode []float64
	sample []float64 // normalised to [0, 1], output-major within a point

	// type 2
	c0, c1 []float64
	n      float64

	// type 3
	parts  []*Function
	bounds []float64

	// type 4
	prog []instr
}

// Inputs is the number of input values the function takes.
func (f *Function) Inputs() int { return len(f.domain) / 2 }

// Outputs is the number of values Eval writes.
func (f *Function) Outputs() int { return f.nout }

// Eval evaluates the function at in, writing Outputs values to out. Inputs
// beyond the domain are clipped to it; missing inputs read as zero.
func (f *Function) Eval(in, out []float64) {
	var buf [maxInputs]float64
	x := buf[:f.Inputs()]
	for i := range x {
		v := 0.0
		if i < len(in) {
			v = in[i]
		}
		x[i] = clip(v, f.domain[2*i], f.domain[2*i+1])
	}
	f.eval(f, x, out)
	for i := range min(len(out), len(f.rng)/2) {
		out[i] = clip(out[i], f.rng[2*i], f.rng[2*i+1])
	}
}

// Parse reads the function v: a dictionary (types 2, 3) or stream (types
// 0, 4).
func Parse(v object.Value) (*Function, error) {
	return parse(v, 0)
}

func parse(v object.Value, depth int) (*Function, error) {
	if depth > maxDepth {
		return nil, errors.New("function: stitching nested too deeply")
	}
	f := &Function{domain: floats(v.Key("Domain")), rng: floats(v.Key("Range"))}
	if len(f.domain) < 2 || len(f.domain)%2 != 0 || len(f.domain)/2 > maxInputs {
		return nil, fmt.Errorf("function: invalid /Domain of %d values", len(f.domain))
	}
	if len(f.rng)%2 != 0 || len(f.rng)/2 > maxOutputs {
		return nil, fmt.Errorf("function: invalid /Range of %d values", len(f.rng))
	}
	f.nout = len(f.rng) / 2
	typ, _ := v.Key("FunctionType").Int64()
	var err error
	switch typ {
	case 0:
		err = f.parseSampled(v)
	case 2:
		err = f.parseExponential(v)
	case 3:
		err = f.parseStitching(v, depth)
	case 4:
		err = f.parseCalculator(v)
	default:
		err = fmt.Errorf("function: unknown type %d", typ)
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (f *Function) parseSampled(v object.Value) error {
	if f.nout == 0 {
		return errors.New("function: sampled function without /Range")
	}
	m := f.Inputs()
	f.size = make([]int, m)
	points := 1
	for i := range m {
		n, ok := v.Key("Size").Index(i).Int64()
		if !ok || n < 1 || n > maxSamples {
			return errors.New("function: invalid /Size")
		}
		f.size[i] = int(n)
		points *= int(n)
		if points > maxSamples/f.nout {
			return errors.New("function: sample table too large")
		}
	}
	bps, _ := v.Key("BitsPerSample").Int64()
	switch bps {
	case 1, 2, 4, 8, 12, 16, 24, 32:
	default:
		return fmt.Errorf("function: invalid /BitsPerSample %d", bps)
	}
	f.encode = floats(v.Key("Encode"))
	if len(f.encode) != 2*m {
		f.encode = make([]float64, 2*m)
		for i := range m {
			f.encode[2*i+1] = float64(f.size[i] - 1)
		}
	}
	f.decode = floats(v.Key("Decode"))
	if len(f.decode) != 2*f.nout {
		f.decode = f.rng
	}
	data, err := readStream(v)
	if err != nil {
		return err
	}
	n := points * f.nout
	f.sample = make([]float64, n)
	maxval := math.Exp2(float64(bps)) - 1
	for i := range n {
		f.sample[i] = float64(bitsAt(data, i*int(bps), int(bps))) / maxval
	}
	f.eval = (*Function).evalSampled
	return nil
}

// bitsAt reads the n-bit big-endian value at bit offset off; bits past the
// data read as zero.
func bitsAt(data []byte, off, n int) uint32 {
	var v uint32
	for i := range n {
		bit := off + i
		v <<= 1
		if b := bit / 8; b < len(data) && data[b]>>(7-bit%8)&1 != 0 {
			v |= 1
		}
	}
	return v
}

// evalSampled interpolates the table multilinearly. Order 3 (cubic) tables
// are interpolated linearly too, which the specification permits.
func (f *Function) evalSampled(in, out []float64) {
	m := len(in)
	var lo [maxInputs]int
	var frac [maxInputs]float64
	for i, x := range in {
		e := interpolate(x, f.domain[2*i], f.domain[2*i+1], f.encode[2*i], f.encode[2*i+1])
		e = clip(e, 0, float64(f.size[i]-1))
		lo[i] = min(int(e), f.size[i]-1)
		frac[i] = e - float64(lo[i])
	}
	for j := range min(len(out), f.nout) {
		sum := 0.0
		for corner := range 1 << m {
			w, idx, stride := 1.0, 0, 1
			for i := range m {
				k := lo[i]
				if corner>>i&1 != 0 {
					if frac[i] == 0 || k+1 >= f.size[i] {
						w = 0
						break
					}
					k++
					w *= frac[i]
				} else {
					w *= 1 - frac[i]
				}
				idx += k * stride
				stride *= f.size[i]
			}
			if w != 0 {
				sum += w * f.sample[idx*f.nout+j]
			}
		}
		out[j] = interpolate(sum, 0, 1, f.decode[2*j], f.decode[2*j+1])
	}
}

func (f *Function) parseExponential(v object.Value) error {
	if f.Inputs() != 1 {
		return errors.New("function: exponential function must take one input")
	}
	f.c0, f.c1 = floats(v.Key("C0")), floats(v.Key("C1"))
	if f.c0 == nil {
		f.c0 = []float64{0}
	}
	if f.c1 == nil {
		f.c1 = []float64{1}
	}
	if len(f.c0) != len(f.c1) || len(f.c0) > maxOutputs {
		return errors.New("function: /C0 and /C1 differ in length")
	}
	f.n, _ = v.Key("N").Float64()
	f.nout = len(f.c0)
	f.eval = (*Function).evalExponential
	return nil
}

func (f *Function) evalExponential(in, out []float64) {
	p := math.Pow(in[0], f.n)
	if math.IsNaN(p) || math.IsInf(p, 0) {
		p = 0
	}
	for j := range min(len(out), f.nout) {
		out[j] = f.c0[j] + p*(f.c1[j]-f.c0[j])
	}
}

func (f *Function) parseStitching(v object.Value, depth int) error {
	if f.Inputs() != 1 {
		return errors.New("function: stitching function must take one input")
	}
	fns := v.Key("Functions")
	k := fns.Len()
	if k == 0 {
		return errors.New("function: stitching function without /Functions")
	}
	f.bounds = floats(v.Key("Bounds"))
	f.encode = floats(v.Key("Encode"))
	if len(f.bounds) != k-1 || len(f.encode) != 2*k {
		return errors.New("function: /Bounds or /Encode do not match /Functions")
	}
	for i := range k {
		part, err := parse(fns.Index(i), depth+1)
		if err != nil {
			return err
		}
		if part.Inputs() != 1 {
			return errors.New("function: stitched function must take one input")
		}
		if i == 0 {
			f.nout = part.nout
		} else if part.nout != f.nout {
			return errors.New("function: stitched functions differ in outputs")
		}
		f.parts = append(f.parts, part)
	}
	f.eval = (*Function).evalStitching
	return nil
}

func (f *Function) evalStitching(in, out []float64) {
	x := in[0]
	i := 0
	for i < len(f.bounds) && x >= f.bounds[i] {
		i++
	}
	lo, hi := f.domain[0], f.domain[1]
	if i > 0 {
		lo = f.bounds[i-1]
	}
	if i < len(f.bounds) {
		hi = f.bounds[i]
	}
	e := interpolate(x, lo, hi, f.encode[2*i], f.encode[2*i+1])
	f.parts[i].Eval([]float64{e}, out)
}

// interpolate maps x from [xmin, xmax] to [ymin, ymax].
func interpolate(x, xmin, xmax, ymin, ymax float64) float64 {
	if xmax == xmin {
		return ymin
	}
	return ymin + (x-xmin)*(ymax-ymin)/(xmax-xmin)
}

func clip(x, lo, hi float64) float64 {
	if math.IsNaN(x) {
		return lo
	}
	return max(lo, min(x, hi))
}

func floats(v object.Value) []float64 {
	if v.Kind() != object.Array {
		return nil
	}
	out := make([]float64, v.Len())
	for i := range out {
		x, ok := v.Index(i).Float64()
		if !ok || math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		out[i] = x
	}
	return out
}

func readStream(v object.Value) ([]byte, error) {
	if v.Kind() != object.Stream {
		return nil, errors.New("function: expected a stream")
	}
	rc, err := v.Reader()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }() // read-only handle
	data, err := safeio.ReadAllGuardedLimitError(rc, maxStreamBytes)
	if err != nil && len(data) == 0 {
		return nil, err
	}
	return data, nil // a damaged stream's decoded prefix is still usable
}
