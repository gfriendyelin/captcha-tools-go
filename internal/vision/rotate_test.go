package vision

import (
	"testing"
)

func TestRotateRGBWhite0(t *testing.T) {
	im := NewRGB(2, 2)
	im.Pix = []uint8{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	out := RotateRGBWhite(im, 0)
	if len(out.Pix) != 12 {
		t.Fatalf("0 rotation changed size")
	}
	for i := range out.Pix {
		if out.Pix[i] != im.Pix[i] {
			t.Fatalf("0 rotation changed pixel %d", i)
		}
	}
}

func TestRotateRGBWhite90CW(t *testing.T) {
	// 2x1 image: pixels A=[1,2,3], B=[4,5,6].
	// Rotating clockwise moves the left edge to the top: output 1x2 is A then B.
	im := NewRGB(2, 1)
	im.Pix = []uint8{1, 2, 3, 4, 5, 6}
	out := RotateRGBWhite(im, 90)
	if out.W != 1 || out.H != 2 {
		t.Fatalf("90CW size = %dx%d, want 1x2", out.W, out.H)
	}
	if out.Pix[0] != 1 || out.Pix[1] != 2 || out.Pix[2] != 3 {
		t.Fatalf("90CW row0 = %v, want A", out.Pix[:3])
	}
	if out.Pix[3] != 4 || out.Pix[4] != 5 || out.Pix[5] != 6 {
		t.Fatalf("90CW row1 = %v, want B", out.Pix[3:6])
	}
}

func TestRotateRGBWhite180(t *testing.T) {
	im := NewRGB(2, 1)
	im.Pix = []uint8{1, 2, 3, 4, 5, 6}
	out := RotateRGBWhite(im, 180)
	if out.W != 2 || out.H != 1 {
		t.Fatalf("180 size = %dx%d, want 2x1", out.W, out.H)
	}
	if out.Pix[0] != 4 || out.Pix[1] != 5 || out.Pix[2] != 6 || out.Pix[3] != 1 || out.Pix[4] != 2 || out.Pix[5] != 3 {
		t.Fatalf("180 = %v, want B,A", out.Pix)
	}
	// Odd row count: the middle row must be reversed horizontally too.
	tall := NewRGB(1, 3)
	tall.Pix = []uint8{1, 2, 3, 4, 5, 6, 7, 8, 9}
	flipped := RotateRGBWhite(tall, 180)
	if flipped.W != 1 || flipped.H != 3 || flipped.Pix[0] != 7 || flipped.Pix[3] != 4 || flipped.Pix[6] != 1 {
		t.Fatalf("180 of 1x3 = %v, want 7,4,1 column", flipped.Pix)
	}
}

func TestRotateRGBWhite270(t *testing.T) {
	im := NewRGB(2, 1)
	im.Pix = []uint8{1, 2, 3, 4, 5, 6}
	out := RotateRGBWhite(im, 270)
	if out.W != 1 || out.H != 2 {
		t.Fatalf("270 size = %dx%d, want 1x2", out.W, out.H)
	}
	// 270 CW = 90 CCW: the right edge moves to the top, giving B then A.
	if out.Pix[0] != 4 || out.Pix[1] != 5 || out.Pix[2] != 6 {
		t.Fatalf("270 row0 = %v, want B", out.Pix[:3])
	}
	if out.Pix[3] != 1 || out.Pix[4] != 2 || out.Pix[5] != 3 {
		t.Fatalf("270 row1 = %v, want A", out.Pix[3:6])
	}
}
