package vision

import (
	"math"
	"testing"
)

func solidRGB(w, h int, r, g, b uint8) *RGB {
	im := NewRGB(w, h)
	for i := 0; i < len(im.Pix); i += 3 {
		im.Pix[i], im.Pix[i+1], im.Pix[i+2] = r, g, b
	}
	return im
}

func normalized(v uint8, c int) float32 {
	return (float32(v)/255 - ImageNetMean[c]) / ImageNetStd[c]
}

func checkTensorGolden(t *testing.T, tensor Tensor, shape [4]int, normalize bool) {
	t.Helper()
	if tensor.Shape != shape {
		t.Fatalf("shape=%v want %v", tensor.Shape, shape)
	}
	channels := [3]uint8{64, 128, 192}
	for c := 0; c < 3; c++ {
		want := float32(channels[c]) / 255
		if normalize {
			want = normalized(channels[c], c)
		}
		got := tensor.At(0, c, shape[2]-1, shape[3]-1)
		if got != want {
			t.Fatalf("channel %d got %.9g want %.9g", c, got, want)
		}
	}
}

func TestPreprocessGolden(t *testing.T) {
	src := solidRGB(112, 112, 64, 128, 192)
	pdl, err := PreprocessPDL([]*RGB{src, src})
	if err != nil {
		t.Fatal(err)
	}
	if pdl.Shape != [4]int{2, 3, 224, 224} {
		t.Fatalf("PDL shape %v", pdl.Shape)
	}
	for c, v := range []uint8{64, 128, 192} {
		if got, want := pdl.At(1, c, 223, 223), normalized(v, c); got != want {
			t.Fatalf("PDL c%d got %g want %g", c, got, want)
		}
	}

	legacy, err := PreprocessLegacy([]*RGB{src})
	if err != nil {
		t.Fatal(err)
	}
	checkTensorGolden(t, legacy, [4]int{1, 3, 224, 224}, true)

	dfine, err := PreprocessDFINE(src)
	if err != nil {
		t.Fatal(err)
	}
	checkTensorGolden(t, dfine.Images, [4]int{1, 3, 320, 320}, false)
	if dfine.OrigTargetSizes != [2]int64{112, 112} {
		t.Fatalf("D-FINE size %v", dfine.OrigTargetSizes)
	}

	yolo, err := PreprocessYOLO(src)
	if err != nil {
		t.Fatal(err)
	}
	checkTensorGolden(t, yolo, [4]int{1, 3, 384, 384}, false)

	dino, err := PreprocessDINO(src)
	if err != nil {
		t.Fatal(err)
	}
	checkTensorGolden(t, dino, [4]int{1, 3, 224, 224}, true)

	patches, err := DINOPatchTokens([]float32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, 6, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 2 || patches[0] != 10 || patches[1] != 11 {
		t.Fatalf("patches %v", patches)
	}
}

func TestPDLClassificationAndFallback(t *testing.T) {
	const classes = 4
	outputs := make([]float32, 10*classes)
	set := func(row int, values ...float32) { copy(outputs[row*classes:(row+1)*classes], values) }
	for i := 0; i < 9; i++ {
		set(i, 1, 0, 0, 0)
	}
	set(0, 0, 0, 1, 0)
	set(5, 0, 0, 1, 0)
	set(9, 0, 0, 1, 0)
	got, err := PDLCoordinates(outputs, 10, classes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (GridPoint{1, 1}) || got[1] != (GridPoint{2, 3}) {
		t.Fatalf("top-1 coordinates %v", got)
	}

	for i := 0; i < 9; i++ {
		set(i, .9, .8, .7, .6)
	}
	set(0, .1, .9, .8, .2) // argsort = [0,3,2,1], second index is target 3.
	set(9, 0, 0, 0, 1)
	got, err = PDLCoordinates(outputs, 10, classes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (GridPoint{1, 1}) {
		t.Fatalf("fallback coordinates %v", got)
	}
}

func TestLegacyCosineSortingAndThreshold(t *testing.T) {
	outputs := make([]float32, 20)
	for row := 2; row < 10; row++ {
		outputs[row*2+1] = 1 // Remaining cells: cosine 0, not a zero norm.
	}
	outputs[0] = 1
	outputs[2], outputs[3] = 1, 0   // cell 0: cosine 1
	outputs[6], outputs[7] = .8, .6 // cell 2: cosine .8
	got, err := LegacyCoordinates(outputs, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != (GridPoint{1, 1}) || got[1] != (GridPoint{1, 3}) {
		t.Fatalf("legacy top three %v", got)
	}
	threshold := float32(.85)
	got, err = LegacyCoordinates(outputs, 2, &threshold)
	if err != nil || len(got) != 1 || got[0] != (GridPoint{1, 1}) {
		t.Fatalf("legacy threshold %v, %v", got, err)
	}
	if score := CosineSimilarity([]float32{1, 0}, []float32{.8, .6}); math.Abs(float64(score)-.8) > 1e-6 {
		t.Fatalf("cosine %g", score)
	}
	if ArgMax([]float32{1, 3, 3, 2}) != 1 {
		t.Fatal("argmax must keep first maximum")
	}
}
