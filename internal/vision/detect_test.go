package vision

import (
	"testing"
)

func TestLetterboxFill(t *testing.T) {
	// 10x10 image into 20x20: scale = min(2,2) = 2, w=h=20, padX=padY=0.
	im := NewRGB(10, 10)
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			im.Pix[y*im.Stride+3*x] = 100
			im.Pix[y*im.Stride+3*x+1] = 150
			im.Pix[y*im.Stride+3*x+2] = 200
		}
	}
	lb, err := Letterbox(im, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	if lb.Scale != 2 || lb.PadX != 0 || lb.PadY != 0 {
		t.Fatalf("scale=%v pad=%v,%v want 2,0,0", lb.Scale, lb.PadX, lb.PadY)
	}
	// Center pixel of each channel: values 100/150/200 -> /255.
	if lb.Values[0*20*20+10*20+10] != 100.0/255.0 {
		t.Fatalf("channel 0 center = %v, want %v", lb.Values[0*20*20+10*20+10], 100.0/255.0)
	}
	if lb.Values[1*20*20+10*20+10] != 150.0/255.0 {
		t.Fatalf("channel 1 center = %v, want %v", lb.Values[1*20*20+10*20+10], 150.0/255.0)
	}
	if lb.Values[2*20*20+10*20+10] != 200.0/255.0 {
		t.Fatalf("channel 2 center = %v, want %v", lb.Values[2*20*20+10*20+10], 200.0/255.0)
	}
}

func TestLetterboxPad(t *testing.T) {
	// 20x10 into 20x20: scale = min(1,2) = 1, w=20, h=10, padX=0, padY=5.
	im := NewRGB(20, 10)
	lb, err := Letterbox(im, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	if lb.Scale != 1 || lb.PadX != 0 || lb.PadY != 5 {
		t.Fatalf("scale=%v pad=%v,%v want 1,0,5", lb.Scale, lb.PadX, lb.PadY)
	}
	// Row 0 (outside image, padY=5) stays 114/255.
	if lb.Values[0*20+0] != 114.0/255.0 {
		t.Fatalf("top pad = %v, want 114/255", lb.Values[0])
	}
}

func TestDecodeDetectCXCywhTransposed(t *testing.T) {
	// Simulate [1, 6, 1] output: one anchor, transposed (channels < count? no:
	// dims[1]=6, dims[2]=1 -> transposed when dims[1] < dims[2]? 6 < 1 false ->
	// NOT transposed. Layout [count, channels]: data[n*channels+c].
	// box: cx=0.5, cy=0.5, w=1.0, h=1.0, obj=0.9, cls0=0.1 -> label 0, score 0.9.
	// scale=1, pad=0 -> x1=(0.5-0.5)/1=0, y1=0, x2=1.0, y2=1.0 -> 1x1 at 0,0.
	data := []float32{0.5, 0.5, 1.0, 1.0, 0.9, 0.1}
	boxes, err := DecodeDetect(data, 6, 1, false, "", 1, 0, 0, 100, 100, 0.5, 0.45, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 1 {
		t.Fatalf("DecodeDetect boxes = %d, want 1", len(boxes))
	}
	b := boxes[0]
	if b.X != 0 || b.Y != 0 || b.Width != 1 || b.Height != 1 || b.Label != 0 {
		t.Fatalf("box = %+v, want 1x1 at 0,0 label 0", b)
	}
}

func TestDecodeDetectTruncAndFilter(t *testing.T) {
	// Box that truncates to zero width must be filtered.
	// cx=0.4, cy=0.5, w=0.5, h=1 -> x1=0.15, x2=0.65 -> trunc x1=0, trunc x2=0 -> w=0 -> filtered.
	data := []float32{0.4, 0.5, 0.5, 1.0, 0.9, 0.1}
	boxes, err := DecodeDetect(data, 6, 1, false, "", 1, 0, 0, 100, 100, 0.5, 0.45, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 0 {
		t.Fatalf("DecodeDetect boxes = %d, want 0 (degenerate filtered)", len(boxes))
	}
}

func TestDecodeYoloXSingleGrid(t *testing.T) {
	// Build a minimal [1, 3549, 6] output where only one anchor fires.
	// Stride 8 -> side 52. Anchor (gx=10, gy=5, row=5*52+10).
	// x=0.0, y=0.0, w=ln(2), h=ln(2) -> box center (10*8, 5*8)=(80,40), w=h=16.
	// score = obj(0.8) * best-cls(0.7) = 0.56.
	const channels = 6
	const anchors = 3549
	data := make([]float32, anchors*channels)
	row := 5*52 + 10
	data[row*channels+0] = 0.0
	data[row*channels+1] = 0.0
	data[row*channels+2] = float32(ln2())
	data[row*channels+3] = float32(ln2())
	data[row*channels+4] = 0.8
	data[row*channels+5] = 0.7
	boxes, err := DecodeYoloX(data, channels, 1.0, 100, 100, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 1 {
		t.Fatalf("DecodeYoloX boxes = %d, want 1", len(boxes))
	}
	b := boxes[0]
	// float32 ln(2) rounds slightly high, so exp() lands just above 2 and the
	// truncated left/top edges are 71/31 instead of the mathematical 72/32.
	// Lotus truncates the same Float32 values, so this matches the TS behaviour.
	if b.X != 71 || b.Y != 31 || b.Width != 16 || b.Height != 16 {
		t.Fatalf("box = %+v, want 71,31,16,16", b)
	}
}

func ln2() float64 {
	// natural log of 2 via a small loop (avoid importing math here? use math)
	return 0.6931471805599453
}

func TestBlurRGBKeepsSize(t *testing.T) {
	im := NewRGB(8, 8)
	for i := range im.Pix {
		im.Pix[i] = 200
	}
	blurred := BlurRGB(im, 0.8)
	if blurred.W != 8 || blurred.H != 8 {
		t.Fatalf("blur size = %dx%d, want 8x8", blurred.W, blurred.H)
	}
	// Uniform image stays uniform.
	for i := range blurred.Pix {
		if blurred.Pix[i] != 200 {
			t.Fatalf("blur broke uniformity at %d: %d", i, blurred.Pix[i])
		}
	}
}

func TestYoloXInputBGRSwap(t *testing.T) {
	im := NewRGB(1, 1)
	im.Pix[0], im.Pix[1], im.Pix[2] = 10, 20, 30
	in, err := YoloXInput(im)
	if err != nil {
		t.Fatal(err)
	}
	// size 416, scale = min(416/1,416/1) = 416, w=trunc(416)=416, h=416.
	// channel 0 gets resized.Pix[2] (blue=30), channel 1 -> green 20, channel 2 -> red 10.
	if in.Values[0] != 30 || in.Values[416*416] != 20 || in.Values[2*416*416] != 10 {
		t.Fatalf("BGR swap wrong: %v %v %v", in.Values[0], in.Values[416*416], in.Values[2*416*416])
	}
}
