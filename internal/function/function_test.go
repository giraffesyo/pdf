package function

import (
	"bytes"
	"math"
	"slices"
	"testing"

	"github.com/giraffesyo/pdf/internal/object"
	"github.com/giraffesyo/pdf/pdftest"
)

// parseObj parses src as object 2 of a minimal document, reached from the
// catalog's /F.
func parseObj(t testing.TB, src string) (*Function, error) {
	t.Helper()
	doc := pdftest.Build(1, "<< /Type /Catalog /F 2 0 R >>", src)
	r, err := object.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatal(err)
	}
	return Parse(r.Trailer().Key("Root").Key("F"))
}

func mustParse(t *testing.T, src string) *Function {
	t.Helper()
	f, err := parseObj(t, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func eval(f *Function, in ...float64) []float64 {
	out := make([]float64, f.Outputs())
	f.Eval(in, out)
	return out
}

func near(got, want []float64) bool {
	return slices.EqualFunc(got, want, func(a, b float64) bool { return math.Abs(a-b) <= 1e-6 })
}

func calculator(domain, rng, program string) string {
	return pdftest.Stream("/FunctionType 4 /Domain ["+domain+"] /Range ["+rng+"]", program)
}

func TestExponential(t *testing.T) {
	f := mustParse(t, "<< /FunctionType 2 /Domain [0 1] /C0 [0 1] /C1 [1 0] /N 2 >>")
	if got := eval(f, 0.5); !near(got, []float64{0.25, 0.75}) {
		t.Errorf("f(0.5) = %v", got)
	}
	if got := eval(f, 7); !near(got, []float64{1, 0}) {
		t.Errorf("f(7) = %v, want the domain clipped to 1", got)
	}
}

func TestStitching(t *testing.T) {
	f := mustParse(t, "<< /FunctionType 3 /Domain [0 1] /Bounds [0.5] /Encode [0 1 1 0] /Functions ["+
		"<< /FunctionType 2 /Domain [0 1] /C0 [0] /C1 [1] /N 1 >> "+
		"<< /FunctionType 2 /Domain [0 1] /C0 [0] /C1 [1] /N 1 >>] >>")
	for _, c := range []struct{ in, want float64 }{{0, 0}, {0.25, 0.5}, {0.5, 1}, {0.75, 0.5}, {1, 0}} {
		if got := eval(f, c.in); !near(got, []float64{c.want}) {
			t.Errorf("f(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSampled(t *testing.T) {
	t.Run("one input", func(t *testing.T) {
		// Two 8-bit samples, 0 and 255, over [0 1] with outputs in [0 2].
		f := mustParse(t, pdftest.Stream("/FunctionType 0 /Domain [0 1] /Range [0 2] /Size [2] /BitsPerSample 8", "\x00\xff"))
		if got := eval(f, 0.25); !near(got, []float64{0.5}) {
			t.Errorf("f(0.25) = %v", got)
		}
	})
	t.Run("bilinear", func(t *testing.T) {
		// A 2×2 grid, first input varying fastest: 0, 1 / 1, 0 (as 4-bit).
		f := mustParse(t, pdftest.Stream("/FunctionType 0 /Domain [0 1 0 1] /Range [0 1] /Size [2 2] /BitsPerSample 4", "\x0f\xf0"))
		for _, c := range []struct{ x, y, want float64 }{{0, 0, 0}, {1, 0, 1}, {0, 1, 1}, {1, 1, 0}, {0.5, 0.5, 0.5}, {0.25, 0, 0.25}} {
			if got := eval(f, c.x, c.y); !near(got, []float64{c.want}) {
				t.Errorf("f(%v, %v) = %v, want %v", c.x, c.y, got, c.want)
			}
		}
	})
	t.Run("tint to CMYK", func(t *testing.T) {
		// A spot colour's tint ramp: 0 → no ink, 1 → 100% cyan, 50% black.
		f := mustParse(t, pdftest.Stream("/FunctionType 0 /Domain [0 1] /Range [0 1 0 1 0 1 0 1] /Size [2] /BitsPerSample 8",
			"\x00\x00\x00\x00\xff\x00\x00\x80"))
		if got := eval(f, 1); !near(got, []float64{1, 0, 0, 128.0 / 255}) {
			t.Errorf("f(1) = %v", got)
		}
	})
}

func TestCalculator(t *testing.T) {
	for _, c := range []struct {
		name, domain, rng, prog string
		in, want                []float64
	}{
		{"tint to CMYK", "0 1", "0 1 0 1 0 1 0 1", "{ dup 0.2 mul exch 0 exch 0 exch }", []float64{0.5}, []float64{0.1, 0, 0, 0.5}},
		{"ifelse", "0 1", "0 1", "{ dup 0.5 gt { pop 1 } { 2 mul } ifelse }", []float64{0.2}, []float64{0.4}},
		{"ifelse taken", "0 1", "0 1", "{ dup 0.5 gt { pop 1 } { 2 mul } ifelse }", []float64{0.7}, []float64{1}},
		{"if", "0 1", "0 1", "{ dup 0.5 lt { 0.5 add } if }", []float64{0.1}, []float64{0.6}},
		{"nested", "0 1 0 1", "0 1", "{ 2 copy gt { pop } { exch pop } ifelse dup 0.9 gt { pop 0.9 } if }", []float64{0.3, 0.95}, []float64{0.9}},
		{"roll", "0 1 0 1 0 1", "0 1 0 1 0 1", "{ 3 1 roll }", []float64{0.1, 0.2, 0.3}, []float64{0.3, 0.1, 0.2}},
		{"roll back", "0 1 0 1 0 1", "0 1 0 1 0 1", "{ 3 -1 roll }", []float64{0.1, 0.2, 0.3}, []float64{0.2, 0.3, 0.1}},
		{"index", "0 1 0 1", "0 1 0 1 0 1", "{ 1 index }", []float64{0.1, 0.2}, []float64{0.1, 0.2, 0.1}},
		{"integer ops", "0 10", "-100 100", "{ cvi 3 idiv 7 mod 2 bitshift }", []float64{9.9}, []float64{12}},
		{"booleans", "0 1", "0 1", "{ 0.5 gt true and false or not { 0 } { 1 } ifelse }", []float64{0.9}, []float64{1}},
		{"trig", "0 360", "-1 1", "{ sin }", []float64{90}, []float64{1}},
		{"range clips", "0 1", "0 1", "{ 10 mul }", []float64{0.5}, []float64{1}},
		{"comment", "0 1", "0 1", "{ % halve\n 2 div }", []float64{0.5}, []float64{0.25}},
		{"underflow leaves zero", "0 1", "0 1 0 1", "{ pop pop pop }", []float64{0.5}, []float64{0, 0}},
		{"divide by zero stops", "0 1", "0 1", "{ 0 div }", []float64{0.5}, []float64{0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := mustParse(t, calculator(c.domain, c.rng, c.prog))
			if got := eval(f, c.in...); !near(got, c.want) {
				t.Errorf("%s on %v = %v, want %v", c.prog, c.in, got, c.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	for name, src := range map[string]string{
		"unknown type":        "<< /FunctionType 1 /Domain [0 1] >>",
		"no domain":           "<< /FunctionType 2 /C0 [0] /C1 [1] /N 1 >>",
		"odd domain":          "<< /FunctionType 2 /Domain [0 1 2] /N 1 >>",
		"C0 and C1 differ":    "<< /FunctionType 2 /Domain [0 1] /C0 [0 0] /C1 [1] /N 1 >>",
		"stitching mismatch":  "<< /FunctionType 3 /Domain [0 1] /Bounds [] /Encode [0 1] /Functions [] >>",
		"sampled no range":    pdftest.Stream("/FunctionType 0 /Domain [0 1] /Size [2] /BitsPerSample 8", "\x00\xff"),
		"sampled bad bits":    pdftest.Stream("/FunctionType 0 /Domain [0 1] /Range [0 1] /Size [2] /BitsPerSample 7", "\x00\xff"),
		"sampled huge table":  pdftest.Stream("/FunctionType 0 /Domain [0 1 0 1] /Range [0 1] /Size [100000 100000] /BitsPerSample 8", ""),
		"calculator operator": calculator("0 1", "0 1", "{ 1 exch sub moveto }"),
		"calculator unclosed": calculator("0 1", "0 1", "{ dup 0.5 gt { pop 1 } if"),
		"calculator bare if":  calculator("0 1", "0 1", "{ dup 0.5 gt if }"),
		"calculator no brace": calculator("0 1", "0 1", "dup"),
		"calculator no range": pdftest.Stream("/FunctionType 4 /Domain [0 1]", "{ }"),
	} {
		if _, err := parseObj(t, src); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestStitchingDepth(t *testing.T) {
	inner := "<< /FunctionType 2 /Domain [0 1] /C0 [0] /C1 [1] /N 1 >>"
	for range maxDepth + 2 {
		inner = "<< /FunctionType 3 /Domain [0 1] /Bounds [] /Encode [0 1] /Functions [" + inner + "] >>"
	}
	if _, err := parseObj(t, inner); err == nil {
		t.Error("deeply nested stitching parsed")
	}
}

// FuzzCalculator: any program either fails to parse or runs without
// panicking, with outputs inside the range.
func FuzzCalculator(f *testing.F) {
	for _, prog := range []string{
		"{ dup 0.5 gt { pop 1 } { 2 mul } ifelse }",
		"{ 3 1 roll 1 index 2 copy add exch pop }",
		"{ cvi 3 idiv 7 mod 2 bitshift not }",
		"{ 0 1 2 3 4 5 6 7 8 9 10 roll }",
		"{ { { } if } if }",
	} {
		f.Add(prog, 0.5)
	}
	f.Fuzz(func(t *testing.T, prog string, x float64) {
		fn, err := parseObj(t, calculator("0 1", "0 1 0 1", prog))
		if err != nil {
			return
		}
		out := make([]float64, 2)
		fn.Eval([]float64{x}, out)
		for _, v := range out {
			if !(v >= 0 && v <= 1) {
				t.Fatalf("%q(%v) = %v, outside the range", prog, x, out)
			}
		}
	})
}
