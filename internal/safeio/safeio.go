// Package safeio bounds reads from untrusted streams: decompression
// bombs and stalled filter chains must cost bounded memory and CPU.
package safeio

import "io"

// MaxStreamBytes bounds decompressed stream size (decompression bombs).
const MaxStreamBytes = 64 << 20

// ReadAllGuarded reads r until error, the size cap, or a run of empty
// reads (a stalled reader violating the io.Reader contract).
func ReadAllGuarded(r io.Reader) []byte {
	var out []byte
	buf := make([]byte, 64<<10)
	zeros := 0
	for len(out) < MaxStreamBytes {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
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
