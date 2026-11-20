package vision

import (
	"bytes"
	"testing"
)

func TestMemoryJPEGAndDrawing(t *testing.T) {
	src := patternedRGB(40, 40)
	encoded, err := EncodeJPEG(src)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := RGBFromBytes(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.W != 40 || decoded.H != 40 {
		t.Fatalf("JPEG dimensions %dx%d", decoded.W, decoded.H)
	}
	if _, err := JPEGRoundTrip(src); err != nil {
		t.Fatal(err)
	}

	dfine, err := DrawDFINE(src, DFINEResult{
		Groups: []DFINEGroup{{ClassID: 2, Detections: []Detection{
			{Box: Box{2, 2, 20, 20}, Label: 2, Score: .8},
			{Box: Box{3, 21, 21, 35}, Label: 2, Score: .7},
		}}},
		Points: []Point{{11, 11}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(dfine.Pix, src.Pix) {
		t.Fatal("D-FINE drawing did not change pixels")
	}
	if _, err := EncodeJPEG(dfine); err != nil {
		t.Fatal(err)
	}

	multi, err := DrawMulti(src, []IntBox{{1, 1, 20, 20}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(multi.Pix, src.Pix) {
		t.Fatal("Multi drawing did not change pixels")
	}
	if _, err := DrawMulti(src, make([]IntBox, 8)); err == nil {
		t.Fatal("Multi must reject more than seven drawn boxes")
	}
}
