package jpx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"
)

// The fixtures in testdata are OpenJPEG's encodings of src.pgm and
// src.ppm (41×29) under the options in gen.sh. Lossless ones must decode
// to the source exactly; lossy ones (with a .ref decode by
// opj_decompress) within one level, the floating-point 9/7 filter's
// rounding.

var testdata = os.DirFS("testdata")

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(testdata, name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// readPNM reads a binary PGM or PPM, skipping comment lines.
func readPNM(t testing.TB, name string) (w, h, channels int, pix []byte) {
	t.Helper()
	data := readFixture(t, name)
	var fields []string
	pos := 0
	for len(fields) < 4 {
		end := bytes.IndexByte(data[pos:], '\n')
		if end < 0 {
			t.Fatalf("%s: truncated header", name)
		}
		line := string(data[pos : pos+end])
		pos += end + 1
		if !strings.HasPrefix(line, "#") {
			fields = append(fields, strings.Fields(line)...)
		}
	}
	channels = 1
	if fields[0] == "P6" {
		channels = 3
	}
	if _, err := fmt.Sscan(fields[1], &w); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscan(fields[2], &h); err != nil {
		t.Fatal(err)
	}
	return w, h, channels, data[pos:]
}

// compare reports the largest difference between img and interleaved
// samples of the given shape.
func compare(t *testing.T, img *Image, w, h, channels int, want []byte) int {
	t.Helper()
	if img.Width != w || img.Height != h || len(img.Channels) != channels {
		t.Fatalf("decoded %d×%d×%d, want %d×%d×%d", img.Width, img.Height, len(img.Channels), w, h, channels)
	}
	worst := 0
	for i := range w * h {
		for c := range channels {
			d := int(img.Channels[c][i]) - int(want[i*channels+c])
			worst = max(worst, d, -d)
		}
	}
	return worst
}

func TestFixtures(t *testing.T) {
	entries, err := fs.ReadDir(testdata, ".")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		ext := path.Ext(e.Name())
		if ext != ".jp2" && ext != ".j2k" {
			continue
		}
		n++
		name := strings.TrimSuffix(e.Name(), ext)
		t.Run(name, func(t *testing.T) {
			img, err := Decode(readFixture(t, e.Name()), Options{})
			if err != nil {
				t.Fatal(err)
			}
			src := "src.ppm"
			if len(img.Channels) == 1 {
				src = "src.pgm"
			}
			tolerance := 0
			ref := src
			if refs, _ := fs.Glob(testdata, name+".ref.p?m"); len(refs) == 1 {
				ref, tolerance = refs[0], 1
			}
			w, h, ch, want := readPNM(t, ref)
			if d := compare(t, img, w, h, ch, want); d > tolerance {
				t.Errorf("differs from %s by up to %d", ref, d)
			}
		})
	}
	if n < 25 {
		t.Fatalf("found %d fixtures", n)
	}
}

func TestDecodeConfig(t *testing.T) {
	cfg, err := DecodeConfig(readFixture(t, "rgb.jp2"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg != (Config{Width: 41, Height: 29, Components: 3}) {
		t.Errorf("config = %+v", cfg)
	}
	if _, err := DecodeConfig([]byte("not an image")); err == nil {
		t.Error("garbage accepted")
	}
}

// codestreamOf returns the codestream inside a fixture.
func codestreamOf(t *testing.T, name string) []byte {
	t.Helper()
	j, err := parseJP2(readFixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return j.codestream
}

func box(typ string, body ...[]byte) []byte {
	var b bytes.Buffer
	n := 8
	for _, p := range body {
		n += len(p)
	}
	_ = binary.Write(&b, binary.BigEndian, uint32(n))
	b.WriteString(typ)
	for _, p := range body {
		b.Write(p)
	}
	return b.Bytes()
}

// jp2File wraps a codestream in a JP2 file with the given header boxes.
func jp2File(codestream []byte, header ...[]byte) []byte {
	sig := box("jP  ", []byte{0x0D, 0x0A, 0x87, 0x0A})
	ftyp := box("ftyp", []byte("jp2 \x00\x00\x00\x00jp2 "))
	return bytes.Join([][]byte{sig, ftyp, box("jp2h", header...), box("jp2c", codestream)}, nil)
}

func colrEnum(cs uint32) []byte {
	b := []byte{1, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[3:], cs)
	return box("colr", b)
}

func TestPalette(t *testing.T) {
	// Entry i is (i, 255−i, i/2).
	pclr := []byte{0x01, 0x00, 3, 7, 7, 7}
	for i := range 256 {
		pclr = append(pclr, byte(i), byte(255-i), byte(i/2))
	}
	cmap := []byte{0, 0, 1, 0, 0, 0, 1, 1, 0, 0, 1, 2}
	file := jp2File(codestreamOf(t, "gray.jp2"), colrEnum(16), box("pclr", pclr), box("cmap", cmap))
	_, _, _, gray := readPNM(t, "src.pgm")

	img, err := Decode(file, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if img.ColorSpace != RGB || len(img.Channels) != 3 {
		t.Fatalf("decoded %v with %d channels", img.ColorSpace, len(img.Channels))
	}
	for i, g := range gray {
		if got := [3]byte{img.Channels[0][i], img.Channels[1][i], img.Channels[2][i]}; got != [3]byte{g, 255 - g, g / 2} {
			t.Fatalf("pixel %d = %v, want the palette entry for %d", i, got, g)
		}
	}

	t.Run("indexed", func(t *testing.T) {
		img, err := Decode(file, Options{Indexed: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(img.Channels) != 1 || !bytes.Equal(img.Channels[0], gray) {
			t.Error("Indexed did not return the palette indices")
		}
	})
	t.Run("unusable mapping", func(t *testing.T) {
		bad := []byte{0, 0, 1, 0, 0x36, 0x70, 0, 0, 0, 0, 1, 9} // a missing component and column
		file := jp2File(codestreamOf(t, "gray.jp2"), colrEnum(16), box("pclr", pclr), box("cmap", bad))
		img, err := Decode(file, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(img.Channels) != 3 {
			t.Errorf("%d channels, want the palette over component 0", len(img.Channels))
		}
	})
}

func TestChannelDefinitions(t *testing.T) {
	cs := codestreamOf(t, "rgb-no-mct.jp2")
	_, _, _, rgb := readPNM(t, "src.ppm")
	cdef := func(entries ...[3]uint16) []byte {
		b := binary.BigEndian.AppendUint16(nil, uint16(len(entries)&0xFFFF))
		for _, e := range entries {
			for _, v := range e {
				b = binary.BigEndian.AppendUint16(b, v)
			}
		}
		return box("cdef", b)
	}
	t.Run("reordered", func(t *testing.T) {
		img, err := Decode(jp2File(cs, colrEnum(16), cdef([3]uint16{0, 0, 3}, [3]uint16{1, 0, 2}, [3]uint16{2, 0, 1})), Options{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range 41 * 29 {
			if img.Channels[0][i] != rgb[3*i+2] || img.Channels[2][i] != rgb[3*i] {
				t.Fatalf("pixel %d not reordered by association", i)
			}
		}
	})
	t.Run("opacity dropped", func(t *testing.T) {
		img, err := Decode(jp2File(cs, colrEnum(17), cdef([3]uint16{0, 0, 1}, [3]uint16{1, 1, 0}, [3]uint16{2, 1, 0})), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if img.ColorSpace != Gray || len(img.Channels) != 1 {
			t.Fatalf("decoded %v with %d channels", img.ColorSpace, len(img.Channels))
		}
	})
	t.Run("extra channel without cdef", func(t *testing.T) {
		img, err := Decode(jp2File(cs, colrEnum(17)), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(img.Channels) != 3 {
			t.Errorf("%d channels; only one beyond the colour space's count is opacity", len(img.Channels))
		}
	})
}

func TestYCC(t *testing.T) {
	img, err := Decode(jp2File(codestreamOf(t, "rgb-no-mct.jp2"), colrEnum(18)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if img.ColorSpace != RGB {
		t.Fatalf("colour space %v, want sYCC converted to RGB", img.ColorSpace)
	}
	_, _, _, src := readPNM(t, "src.ppm")
	want := [][]byte{make([]byte, 41*29), make([]byte, 41*29), make([]byte, 41*29)}
	for i := range 41 * 29 {
		want[0][i], want[1][i], want[2][i] = src[3*i], src[3*i+1], src[3*i+2]
	}
	yccToRGB(want)
	for c := range 3 {
		if !bytes.Equal(img.Channels[c], want[c]) {
			t.Fatalf("channel %d differs from the sYCC conversion", c)
		}
	}
}

// packHeaders rewrites a one-tile codestream so its packet headers travel
// in PPT (or PPM) marker segments, apart from the packet bodies, as some
// encoders write them.
func packHeaders(t *testing.T, data []byte, ppm bool) []byte {
	t.Helper()
	cs, pos, err := parseMainHeader(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.parseTiles(data, pos); err != nil || len(cs.tiles) != 1 || len(cs.tiles[0].parts) != 1 {
		t.Fatalf("fixture must have one tile-part: %v", err)
	}
	td := cs.tiles[0]
	tl := cs.newTile(td)
	body := td.parts[0]
	var headers, bodies []byte
	h := &bitReader{data: body}
	layers, _, _, _, vols := cs.tileProgression(td)
	for _, v := range vols {
		cs.progress(tl, v, min(v.layerEnd, layers), func(l, r, c, p int) bool {
			start := h.pos
			if start >= len(body) {
				return false
			}
			h.reset()
			lengths := tl.comps[c].packetHeader(h, tl.comps[c].res[r], p, l)
			h.align()
			headers = append(headers, body[start:h.pos]...)
			n := 0
			for _, ln := range lengths {
				n += ln.n
			}
			bodies = append(bodies, body[h.pos:h.pos+n]...)
			h.pos += n
			return true
		})
	}
	var out bytes.Buffer
	marker := func(code uint16, payload []byte) {
		_ = binary.Write(&out, binary.BigEndian, code)
		_ = binary.Write(&out, binary.BigEndian, uint16((len(payload)+2)&0xFFFF))
		out.Write(payload)
	}
	out.Write(data[:pos])
	if ppm {
		payload := append([]byte{0}, binary.BigEndian.AppendUint32(nil, uint32(len(headers)&0x7FFFFFFF))...)
		marker(mPPM, append(payload, headers...))
	}
	sot := out.Len()
	marker(mSOT, make([]byte, 8)) // Isot 0, Psot patched below, TPsot 0, TNsot 1
	if !ppm {
		marker(mPPT, append([]byte{0}, headers...))
	}
	out.Write([]byte{0xFF, 0x93})
	out.Write(bodies)
	b := out.Bytes()
	binary.BigEndian.PutUint32(b[sot+6:], uint32((len(b)-sot)&0x7FFFFFFF))
	b[sot+11] = 1
	return append(b, 0xFF, 0xD9)
}

func TestPackedHeaders(t *testing.T) {
	for _, name := range []string{"rgb.jp2", "rgb-lossy.jp2", "gray-lossy-layers.jp2"} {
		cs := codestreamOf(t, name)
		want, err := Decode(cs, Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, ppm := range []bool{false, true} {
			got, err := Decode(packHeaders(t, cs, ppm), Options{})
			if err != nil {
				t.Fatalf("%s ppm=%v: %v", name, ppm, err)
			}
			for c := range want.Channels {
				if !bytes.Equal(got.Channels[c], want.Channels[c]) {
					t.Errorf("%s ppm=%v: channel %d differs from the unpacked decode", name, ppm, c)
				}
			}
		}
	}
}

func TestTruncated(t *testing.T) {
	data := readFixture(t, "rgb-lossy.jp2")
	full, err := Decode(data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for n := len(data) - 1; n > 0; n -= 7 {
		img, err := Decode(data[:n], Options{})
		if err != nil {
			continue
		}
		if img.Width != full.Width || len(img.Channels) != len(full.Channels) {
			t.Fatalf("cut to %d: decoded %d×%d×%d", n, img.Width, img.Height, len(img.Channels))
		}
	}
}

func TestLimits(t *testing.T) {
	data := readFixture(t, "rgb.jp2")
	if _, err := Decode(data, Options{MaxSamples: 41 * 29 * 3}); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	if _, err := Decode(data, Options{MaxSamples: 41*29*3 - 1}); err == nil {
		t.Error("over the limit: decoded")
	}
}

func TestUnsupported(t *testing.T) {
	cs := codestreamOf(t, "gray.jp2")
	ht := bytes.Clone(cs)
	ht[6] |= 0x40 // Rsiz: Part 15
	deep := bytes.Clone(cs)
	deep[42] = 16 // Ssiz: 17-bit samples
	for name, data := range map[string][]byte{"htj2k": ht, "17-bit": deep} {
		if _, err := Decode(data, Options{}); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func FuzzDecode(f *testing.F) {
	entries, err := fs.ReadDir(testdata, ".")
	if err != nil {
		f.Fatal(err)
	}
	for _, e := range entries {
		if ext := path.Ext(e.Name()); ext == ".jp2" || ext == ".j2k" {
			data, err := fs.ReadFile(testdata, e.Name())
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data)
		}
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = Decode(data, Options{MaxSamples: 1 << 16})
	})
}
