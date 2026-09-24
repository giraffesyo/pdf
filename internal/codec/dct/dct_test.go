package dct

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io/fs"
	"os"
	"testing"
)

// The fixtures, made by testdata/gen.py: a 16×8 CMYK image, the left half
// ink (200, 0, 50, 10), the right half (0, 0, 0, 255). adobe-cmyk.jpg is
// Pillow's output, Adobe-inverted samples with an APP14 marker;
// plain-cmyk.jpg is the same file with the marker removed, so its stored
// samples read as plain ink are the inverse: (55, 255, 205, 245) and
// (255, 255, 255, 0).

var testdata = os.DirFS("testdata")

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(testdata, name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func checkHalves(t *testing.T, img image.Image, left, right color.CMYK) {
	t.Helper()
	c, ok := img.(*image.CMYK)
	if !ok {
		t.Fatalf("decoded %T, want *image.CMYK", img)
	}
	near := func(a, b uint8) bool { return a-b < 4 || b-a < 4 }
	for _, p := range []struct {
		x    int
		want color.CMYK
	}{{2, left}, {13, right}} {
		got := c.CMYKAt(p.x, 4)
		if !near(got.C, p.want.C) || !near(got.M, p.want.M) || !near(got.Y, p.want.Y) || !near(got.K, p.want.K) {
			t.Errorf("pixel (%d, 4) = %v, want %v", p.x, got, p.want)
		}
	}
}

func TestPlainCMYK(t *testing.T) {
	data := read(t, "plain-cmyk.jpg")
	if _, err := jpeg.Decode(bytes.NewReader(data)); err == nil {
		t.Fatal("image/jpeg decodes the marker-less fixture; the test no longer covers anything")
	}
	cfg, err := DecodeConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 16 || cfg.Height != 8 || cfg.ColorModel != color.CMYKModel {
		t.Errorf("config = %d×%d %v", cfg.Width, cfg.Height, cfg.ColorModel)
	}
	img, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	checkHalves(t, img, color.CMYK{C: 55, M: 255, Y: 205, K: 245}, color.CMYK{C: 255, M: 255, Y: 255})
}

func TestAdobeCMYKUnchanged(t *testing.T) {
	img, err := Decode(read(t, "adobe-cmyk.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	checkHalves(t, img, color.CMYK{C: 200, Y: 50, K: 10}, color.CMYK{K: 255})
}

func TestOtherJPEGsUntouched(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"grey":      buf.Bytes(),
		"adobe":     read(t, "adobe-cmyk.jpg"),
		"empty":     nil,
		"not jpeg":  []byte("%PDF-1.7"),
		"truncated": read(t, "plain-cmyk.jpg")[:40],
	} {
		if got := fixup(data); len(got) != len(data) {
			t.Errorf("%s: fixup changed the data", name)
		}
	}
}

func FuzzDecode(f *testing.F) {
	for _, name := range []string{"plain-cmyk.jpg", "adobe-cmyk.jpg"} {
		data, err := fs.ReadFile(testdata, name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		if cfg, err := DecodeConfig(data); err != nil || cfg.Width*cfg.Height > 1<<20 {
			return
		}
		_, _ = Decode(data)
	})
}
