package vision

import (
	"bytes"
	"fmt"
	"image"

	libjpeg "github.com/pixiv/go-libjpeg/jpeg"
)

func decodeJPEGNative(data []byte) (*RGB, error) {
	decoded, err := libjpeg.DecodeIntoRGB(bytes.NewReader(data), &libjpeg.DecoderOptions{})
	if err != nil {
		return nil, fmt.Errorf("vision: libjpeg decode: %w", err)
	}
	bounds := decoded.Bounds()
	out := NewRGB(bounds.Dx(), bounds.Dy())
	for y := 0; y < out.H; y++ {
		copy(out.Pix[y*out.Stride:(y+1)*out.Stride], decoded.Pix[y*decoded.Stride:y*decoded.Stride+out.Stride])
	}
	return out, nil
}

// EncodeJPEG uses quality 75 and libjpeg's default 4:2:0/DCT settings, matching
// the Pillow save-then-decode stage used by the original PDL pipeline.
func EncodeJPEG(im *RGB) ([]byte, error) { return EncodeJPEGQuality(im, 75) }

func EncodeJPEGQuality(im *RGB, quality int) ([]byte, error) {
	if im == nil {
		return nil, fmt.Errorf("vision: nil JPEG image")
	}
	if quality < 1 || quality > 100 {
		return nil, fmt.Errorf("vision: invalid JPEG quality %d", quality)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, im.W, im.H))
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			si, di := im.offset(x, y), rgba.PixOffset(x, y)
			rgba.Pix[di], rgba.Pix[di+1], rgba.Pix[di+2], rgba.Pix[di+3] = im.Pix[si], im.Pix[si+1], im.Pix[si+2], 255
		}
	}
	var buf bytes.Buffer
	if err := libjpeg.Encode(&buf, rgba, &libjpeg.EncoderOptions{Quality: quality}); err != nil {
		return nil, fmt.Errorf("vision: libjpeg encode: %w", err)
	}
	return buf.Bytes(), nil
}

// JPEGRoundTrip reproduces the save-then-decode stage in memory.
func JPEGRoundTrip(im *RGB) (*RGB, error) {
	data, err := EncodeJPEG(im)
	if err != nil {
		return nil, err
	}
	return RGBFromBytes(data)
}
