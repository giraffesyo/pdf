// Package safeio bounds reads from untrusted streams: decompression
// bombs and stalled filter chains must cost bounded memory and CPU.
package safeio

import "io"

// MaxStreamBytes bounds decompressed stream size (decompression bombs).
const MaxStreamBytes = 64 << 20

// ReadAllGuarded reads r until error, the size cap, or a run of empty
// reads (a stalled reader violating the io.Reader contract).
func ReadAllGuarded(r io.Reader) []byte {
	return ReadAllGuardedLimit(r, MaxStreamBytes)
}

// ReadAllGuardedLimit is ReadAllGuarded with a caller-supplied size cap.
// A non-positive limit returns no data without reading from r.
func ReadAllGuardedLimit(r io.Reader, limit int) []byte {
	if limit <= 0 {
		return nil
	}

	// Read directly into the result and grow it geometrically. A separate
	// fixed-size scratch buffer made even tiny streams cost 64 KiB, then
	// copied every byte into a second allocation.
	out := make([]byte, 0, min(512, limit))
	var probe []byte
	zeros := 0
	for len(out) < limit {
		var n int
		var err error
		if len(out) == cap(out) {
			// Probe before growing so a stream whose size exactly matches
			// the current capacity does not force an unused allocation.
			if probe == nil {
				probe = make([]byte, 1)
			}
			n, err = r.Read(probe)
			if n > 0 {
				capacity := min(max(2*cap(out), 32<<10), limit)
				grown := make([]byte, len(out), capacity)
				copy(grown, out)
				out = grown
				out = append(out, probe[:n]...)
			}
		} else {
			n, err = r.Read(out[len(out):cap(out)])
			out = out[:len(out)+n]
		}
		if err != nil {
			break
		}
		if n == 0 {
			if zeros++; zeros > 100 {
				break
			}
			continue
		}
		zeros = 0
	}
	return out
}
