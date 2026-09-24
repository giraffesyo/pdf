package jpx

import "math"

// Inverse discrete wavelet transform (T.800 Annex F): the reversible 5/3
// and irreversible 9/7 filters by lifting, over symmetrically extended
// signals.

// The 9/7 lifting constants (T.800 Table F.4).
const (
	liftAlpha = -1.586134342059924
	liftBeta  = -0.052980118572961
	liftGamma = 0.882911075530934
	liftDelta = 0.443506852043971
	liftK     = 1.230174104914001
)

// ext is how far each signal is extended on either side: enough for the
// 9/7 filter's four lifting steps.
const ext = 4

// reconstruct runs the inverse transform over a tile-component, from its
// lowest resolution up, leaving the samples in tc.data.
func (tc *tileComp) reconstruct() {
	ll := tc.res[0].bands[0]
	cur := ll.coeffs
	w := ll.x1 - ll.x0
	var line []float32
	for r := 1; r < len(tc.res); r++ {
		res := tc.res[r]
		u0, v0 := res.x0, res.y0
		nw, nh := res.x1-res.x0, res.y1-res.y0
		hl, lh, hh := res.bands[0], res.bands[1], res.bands[2]
		lx0, ly0 := tc.res[r-1].x0, tc.res[r-1].y0
		out := make([]float32, nw*nh)
		// Interleave (T.800 F.3.3): even positions from the low band, odd
		// from the high.
		for v := range nh {
			ay := v0 + v
			for u := range nw {
				ax := u0 + u
				var val float32
				switch {
				case ay%2 == 0 && ax%2 == 0:
					val = at(cur, w, ax/2-lx0, ay/2-ly0)
				case ay%2 == 0:
					val = at(hl.coeffs, hl.x1-hl.x0, floorDiv(ax, 2)-hl.x0, ay/2-hl.y0)
				case ax%2 == 0:
					val = at(lh.coeffs, lh.x1-lh.x0, ax/2-lh.x0, floorDiv(ay, 2)-lh.y0)
				default:
					val = at(hh.coeffs, hh.x1-hh.x0, floorDiv(ax, 2)-hh.x0, floorDiv(ay, 2)-hh.y0)
				}
				out[v*nw+u] = val
			}
		}
		// Rows, then columns (T.800 F.3.4, F.3.5).
		line = growFloat(line, max(nw, nh)+2*ext)
		for v := range nh {
			synthesize(out[v*nw:(v+1)*nw], u0, tc.coding.reversible, line)
		}
		col := make([]float32, nh)
		for u := range nw {
			for v := range nh {
				col[v] = out[v*nw+u]
			}
			synthesize(col, v0, tc.coding.reversible, line)
			for v := range nh {
				out[v*nw+u] = col[v]
			}
		}
		cur, w = out, nw
	}
	tc.data = cur
}

func at(s []float32, stride, x, y int) float32 {
	if x < 0 || y < 0 || x >= stride {
		return 0
	}
	if i := y*stride + x; i < len(s) {
		return s[i]
	}
	return 0
}

func growFloat(s []float32, n int) []float32 {
	if cap(s) < n {
		return make([]float32, n)
	}
	return s[:n]
}

// synthesize is 1D_SR (T.800 F.3.6): x holds the interleaved samples of
// positions i0, i0+1, …, and is replaced by the reconstructed signal.
func synthesize(x []float32, i0 int, reversible bool, buf []float32) {
	n := len(x)
	switch n {
	case 0:
		return
	case 1:
		if i0%2 != 0 {
			if reversible {
				x[0] = float32(math.Trunc(float64(x[0]) / 2))
			} else {
				x[0] /= 2
			}
		}
		return
	}
	// Periodic symmetric extension (T.800 F.3.7).
	e := buf[:n+2*ext]
	period := 2 * (n - 1)
	for j := range e {
		k := j - ext
		k %= period
		if k < 0 {
			k += period
		}
		if k >= n {
			k = period - k
		}
		e[j] = x[k]
	}
	// e[j] is at position i0-ext+j: low-pass where that is even.
	odd0 := (i0 - ext) & 1
	if reversible {
		// T.800 F.3.8.1.
		for j := 1; j < len(e)-1; j++ {
			if (odd0+j)&1 == 0 {
				e[j] -= float32(math.Floor(float64(e[j-1]+e[j+1]+2) / 4))
			}
		}
		for j := 2; j < len(e)-2; j++ {
			if (odd0+j)&1 == 1 {
				e[j] += float32(math.Floor(float64(e[j-1]+e[j+1]) / 2))
			}
		}
	} else {
		// T.800 F.3.8.2.
		for j := range e {
			if (odd0+j)&1 == 0 {
				e[j] *= liftK
			} else {
				e[j] *= 1 / liftK
			}
		}
		lift(e, odd0, 0, 1, liftDelta)
		lift(e, odd0, 1, 2, liftGamma)
		lift(e, odd0, 0, 3, liftBeta)
		lift(e, odd0, 1, 4, liftAlpha)
	}
	copy(x, e[ext:ext+n])
}

// lift subtracts c times the sum of each neighbour pair from the samples
// of the given parity, over the range still valid after step steps.
func lift(e []float32, odd0, parity, step int, c float32) {
	for j := step; j < len(e)-step; j++ {
		if (odd0+j)&1 == parity {
			e[j] -= c * (e[j-1] + e[j+1])
		}
	}
}
