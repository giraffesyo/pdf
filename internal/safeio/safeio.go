// Package safeio bounds reads from untrusted streams: decompression
// bombs and stalled filter chains must cost bounded memory and CPU.
package safeio

import (
	"errors"
	"io"
)

// MaxStreamBytes bounds decompressed stream size (decompression bombs).
const MaxStreamBytes = 64 << 20

// ErrLimitExceeded reports that decoded data exceeded the configured cap.
var ErrLimitExceeded = errors.New("decoded stream exceeds size limit")

// ErrStalled reports a reader that repeatedly returned no data and no error.
var ErrStalled = errors.New("decoded stream reader stalled")

// ReadAllGuarded reads r until error, the size cap, or a run of empty
// reads (a stalled reader violating the io.Reader contract).
func ReadAllGuarded(r io.Reader) []byte {
	return ReadAllGuardedLimit(r, MaxStreamBytes)
}

// ReadAllGuardedLimit is ReadAllGuarded with a caller-supplied size cap.
// A non-positive limit returns no data without reading from r.
func ReadAllGuardedLimit(r io.Reader, limit int) []byte {
	out, _ := ReadAllGuardedLimitError(r, limit)
	return out
}

// ReadAllGuardedLimitError is ReadAllGuardedLimit with an explanation when
// reading stopped because of a source error, the size cap, or a stalled
// reader. Bytes successfully read before the error are retained.
func ReadAllGuardedLimitError(r io.Reader, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, nil
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
				capacity := min(max(2*cap(out), 4<<10), limit)
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
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
		if n == 0 {
			if zeros++; zeros > 100 {
				return out, ErrStalled
			}
			continue
		}
		zeros = 0
	}

	// Distinguish an exact-sized stream from one truncated at the cap.
	var probeByte [1]byte
	for zeros := 0; ; {
		n, err := r.Read(probeByte[:])
		if n > 0 {
			return out, ErrLimitExceeded
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
		if zeros++; zeros > 100 {
			return out, ErrStalled
		}
	}
}
