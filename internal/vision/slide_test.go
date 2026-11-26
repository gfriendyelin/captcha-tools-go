package vision

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func testPNG(t *testing.T, w, h int, fill func(x, y int) color.NRGBA) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.SetNRGBA(x, y, fill(x, y))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRGBAFromBytes(t *testing.T) {
	data := testPNG(t, 2, 1, func(x, y int) color.NRGBA {
		if x == 0 {
			return color.NRGBA{R: 255, G: 0, B: 0, A: 128}
		}
		return color.NRGBA{R: 0, G: 255, B: 0, A: 0}
	})
	im, err := RGBAFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if im.W != 2 || im.H != 1 {
		t.Fatalf("RGBAFromBytes size = %dx%d, want 2x1", im.W, im.H)
	}
	if im.Pix[3] != 128 || im.Pix[7] != 0 {
		t.Fatalf("alpha not preserved: %v", im.Pix)
	}
}

func TestGrayFromRGBAWhite(t *testing.T) {
	// Pure red with alpha 128 composites to (255,127,127) at ~50% over white.
	im := &RGBA{W: 1, H: 1, Stride: 4, Pix: []uint8{255, 0, 0, 128}}
	g := GrayFromRGBA(im, "white")
	// gray = 0.299*255 + 0.587*127 + 0.114*127 = 165.27 -> 165
	if g.Pix[0] != 165 {
		t.Fatalf("white-composite gray = %d, want 165", g.Pix[0])
	}
}

func TestGrayFromRGBAZero(t *testing.T) {
	im := &RGBA{W: 2, H: 1, Stride: 4, Pix: []uint8{255, 0, 0, 255, 10, 20, 30, 0}}
	g := GrayFromRGBA(im, "zero")
	if g.Pix[0] == 0 {
		t.Fatalf("opaque pixel became 0")
	}
	if g.Pix[1] != 0 {
		t.Fatalf("transparent pixel = %d, want 0", g.Pix[1])
	}
}

func TestSlideMatchSynthetic(t *testing.T) {
	// Background: 80x40 light gray with a 12x12 textured block at x=50, y=12.
	texture := func(x, y int) color.NRGBA {
		v := uint8((x*53 + y*97 + (x*y)*7) % 256)
		return color.NRGBA{R: v, G: v, B: v, A: 255}
	}
	bgData := testPNG(t, 80, 40, func(x, y int) color.NRGBA {
		if x >= 50 && x < 62 && y >= 12 && y < 24 {
			return texture(x, y)
		}
		return color.NRGBA{R: 240, G: 240, B: 240, A: 255}
	})
	// The piece is the block exactly as it appears at its true location.
	pcData := testPNG(t, 12, 12, func(x, y int) color.NRGBA {
		return texture(x+50, y+12)
	})
	bg, err := RGBAFromBytes(bgData)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := RGBAFromBytes(pcData)
	if err != nil {
		t.Fatal(err)
	}
	offset, ok := SlideMatch(RGBFromRGBAWhite(bg), RGBFromRGBAWhite(pc), 0)
	if !ok {
		t.Fatal("SlideMatch returned not-ok")
	}
	if offset != 50 {
		t.Fatalf("SlideMatch offset = %d, want 50", offset)
	}
}
