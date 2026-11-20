package vision

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
)

// RGB is an unpacked, 8-bit, three-channel image. Its methods implement
// image.Image, so it can be passed directly to the standard JPEG encoder.
type RGB struct {
	W, H   int
	Stride int
	Pix    []uint8
}

func NewRGB(w, h int) *RGB {
	if w < 0 || h < 0 {
		panic("vision: negative image dimensions")
	}
	return &RGB{W: w, H: h, Stride: 3 * w, Pix: make([]uint8, 3*w*h)}
}

// RGBFromBytes decodes JPEG, PNG, or GIF and converts to unassociated RGB.
func RGBFromBytes(data []byte) (*RGB, error) {
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return decodeJPEGNative(data)
	}
	return DecodeRGB(bytes.NewReader(data))
}

// DecodeRGB converts color without alpha compositing, like PIL convert("RGB").
func DecodeRGB(r io.Reader) (*RGB, error) {
	im, _, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("vision: decode image: %w", err)
	}
	return RGBFromImage(im), nil
}

// RGBFromImage drops alpha instead of blending it against a background.
func RGBFromImage(im image.Image) *RGB {
	b := im.Bounds()
	out := NewRGB(b.Dx(), b.Dy())
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			c := color.NRGBAModel.Convert(im.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			i := y*out.Stride + 3*x
			out.Pix[i], out.Pix[i+1], out.Pix[i+2] = c.R, c.G, c.B
		}
	}
	return out
}

func (im *RGB) ColorModel() color.Model { return color.RGBAModel }
func (im *RGB) Bounds() image.Rectangle { return image.Rect(0, 0, im.W, im.H) }
func (im *RGB) At(x, y int) color.Color {
	if x < 0 || y < 0 || x >= im.W || y >= im.H {
		return color.RGBA{}
	}
	i := y*im.Stride + 3*x
	return color.RGBA{R: im.Pix[i], G: im.Pix[i+1], B: im.Pix[i+2], A: 255}
}

func (im *RGB) Clone() *RGB {
	if im == nil {
		return nil
	}
	out := NewRGB(im.W, im.H)
	copy(out.Pix, im.Pix)
	return out
}

func (im *RGB) offset(x, y int) int { return y*im.Stride + 3*x }

func (im *RGB) Set(x, y int, c color.RGBA) {
	if x < 0 || y < 0 || x >= im.W || y >= im.H {
		return
	}
	i := im.offset(x, y)
	im.Pix[i], im.Pix[i+1], im.Pix[i+2] = c.R, c.G, c.B
}

// Paste copies the intersecting part of src at (x,y), leaving the rest intact.
func (im *RGB) Paste(src *RGB, x, y int) {
	for sy := 0; sy < src.H; sy++ {
		dy := y + sy
		if dy < 0 || dy >= im.H {
			continue
		}
		for sx := 0; sx < src.W; sx++ {
			dx := x + sx
			if dx < 0 || dx >= im.W {
				continue
			}
			si := src.offset(sx, sy)
			di := im.offset(dx, dy)
			copy(im.Pix[di:di+3], src.Pix[si:si+3])
		}
	}
}
