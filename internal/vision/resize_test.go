package vision

import (
	"bytes"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func patternedRGB(w, h int) *RGB {
	im := NewRGB(w, h)
	for i := range im.Pix {
		im.Pix[i] = uint8((i*37 + 11) % 256)
	}
	return im
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	out, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestResizePillowGolden(t *testing.T) {
	src := patternedRGB(7, 5)
	tests := []struct {
		name   string
		filter Resample
		w, h   int
		hex    string
	}{
		{"bilinear-down", Bilinear, 4, 3, "496287ae4e738fb4547ba0b95470959e5c819da46289ab5f597ea3456a8fab4b7097b04f"},
		{"bilinear-up", Bilinear, 8, 6, "0b3055658aafc63b60974c7189ae639abf344b7095a5caef12375c6c91b6cd42679e537890b56aa1c63b52779cacd1f6193e637398bdd4496ea55a7f97bc71a8cd42597e87b3d86821466b7ba0c59251767e62879f9579b08b4a61867bbbe005284d7282a7cc33587d44698ea65b80b72c51688d82c2e70c2f547989aed33a5f844b7095ad6287be33586f9489c9ee13"},
		{"bicubic-down", Bicubic, 4, 3, "446287b94b748cba4c799ec1527196a55b839ca66288ac5c5b80a53f6d92ae407397b548"},
		{"bicubic-up", Bicubic, 8, 6, "082d526297bcd93156943f7585bb67a4c9213e6398a8cdf20e3358689dc2df375c9a457b8bc16daacf274469a1aed3ff163b606fa5caf63f64ab4c8392d275b2e6344c708fb6db641e43687eadd29d476c795b8ba1a17dba8d3f547f84bee300264b7091b5da1c4f742f6f93b55785c20c475c928fc6eb082c517696bbe030557a3e7499ba668bc8204d629795ccf10f"},
		{"lanczos-down", Lanczos, 4, 3, "476285c24e7c83b9457b9ec0547194ab608795a2638aab585b7ea3407695ab377797b243"},
		{"lanczos-up", Lanczos, 8, 6, "082a4f5999bee936538e2c7684ce6ca7c4113c61a0abd0ee0e3055609fc4e93c5990337c8bd073adc4154268acb1d6ff17395e65a8cdff4662ad36858eed78b7ed2e4b6d93badf621c3f6479aed3ab48686f508ba8af7cb98640518182c0e40023486d95b7dc1a4b71187394cb5885bc00455a9d91c9eb0d294e7398bde23952772d759acd6d8bc3144c60a096cff10c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resize(src, tt.w, tt.h, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Pix, decodeHex(t, tt.hex)) {
				t.Fatalf(`Pillow RGB mismatch
 got %x
want %s`, got.Pix, tt.hex)
			}
		})
	}
}

func TestDecodeRGBDropsAlpha(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 11, G: 22, B: 33, A: 0})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRGB(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Pix, []byte{11, 22, 33}) {
		t.Fatalf("got %v", got.Pix)
	}
}

func TestCropV3GeometryAndBlackPadding(t *testing.T) {
	src := patternedRGB(344, 384)
	got, err := CropV3(src)
	if err != nil {
		t.Fatal(err)
	}
	if got.Nine.W != 300 || got.Nine.H != 261 {
		t.Fatalf("unexpected nine dimensions: %+v", got.Nine)
	}
	for i, cell := range got.Cells[:9] {
		if cell.W != 112 || cell.H != 112 {
			t.Fatalf("cell %d dimensions %dx%d", i, cell.W, cell.H)
		}
	}
	if got.Cells[9].W != 40 || got.Cells[9].H != 40 {
		t.Fatalf("target dimensions %dx%d", got.Cells[9].W, got.Cells[9].H)
	}
	if got.Cells[9].Pix[0] != src.Pix[344*src.Stride+2*3] {
		t.Fatal("target crop origin mismatch")
	}
	for x := 0; x < 300; x++ {
		i := 260*got.Nine.Stride + 3*x
		if got.Nine.Pix[i] != 0 || got.Nine.Pix[i+1] != 0 || got.Nine.Pix[i+2] != 0 {
			t.Fatalf("nine bottom row is not black at %d", x)
		}
	}

	padded, err := CropBlack(NewRGB(2, 2), IntBox{-1, -1, 2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if padded.W != 3 || padded.H != 3 || !bytes.Equal(padded.Pix[:3], []byte{0, 0, 0}) {
		t.Fatalf("invalid black padding: %+v", padded)
	}
}
