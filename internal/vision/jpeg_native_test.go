package vision

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

type jpegGolden struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Raw     string `json:"raw"`
	JPEG    string `json:"jpeg"`
	Decoded string `json:"decoded"`
}

func TestLibJPEGMatchesPillowGolden(t *testing.T) {
	data, err := os.ReadFile("../../testdata/jpeg_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden jpegGolden
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	raw := mustBase64(t, golden.Raw)
	encodedGolden := mustBase64(t, golden.JPEG)
	decodedGolden := mustBase64(t, golden.Decoded)
	image := NewRGB(golden.Width, golden.Height)
	copy(image.Pix, raw)
	encoded, err := EncodeJPEG(image)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, encodedGolden) {
		t.Fatalf("libjpeg stream differs from Pillow: got %d bytes, want %d", len(encoded), len(encodedGolden))
	}
	decoded, err := RGBFromBytes(encodedGolden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Pix, decodedGolden) {
		t.Fatal("decoded RGB differs from Pillow")
	}
}

func mustBase64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
