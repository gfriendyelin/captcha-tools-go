package svgcaptcha

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"math"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// grayImage keeps luminance in [0, 255] with a row-major layout.
type grayImage struct {
	w, h int
	pix  []float64
}

func newGray(w, h int) *grayImage {
	return &grayImage{w: w, h: h, pix: make([]float64, w*h)}
}

func (g *grayImage) at(x, y int) float64 {
	if x < 0 || y < 0 || x >= g.w || y >= g.h {
		return 0
	}
	return g.pix[y*g.w+x]
}

// renderSVG rasterizes one grid SVG and composites it over white, matching the
// reference pipeline that pads transparent pixels with white.
func renderSVG(source string, width, height int) (*grayImage, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid grid size %dx%d", width, height)
	}
	icon, err := oksvg.ReadIconStream(strings.NewReader(source), oksvg.WarnErrorMode)
	if err != nil {
		return nil, fmt.Errorf("read grid svg: %w", err)
	}
	icon.SetTarget(0, 0, float64(width), float64(height))
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	scanner := rasterx.NewScannerGV(width, height, canvas, canvas.Bounds())
	icon.Draw(rasterx.NewDasher(width, height, scanner), 1.0)
	return rgbaToGray(canvas), nil
}

func rgbaToGray(img image.Image) *grayImage {
	bounds := img.Bounds()
	result := newGray(bounds.Dx(), bounds.Dy())
	for y := 0; y < result.h; y++ {
		for x := 0; x < result.w; x++ {
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			// RGBA() returns alpha-premultiplied values, so adding the white
			// background only needs the uncovered part.
			r += 0xffff - a
			g += 0xffff - a
			b += 0xffff - a
			value := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
			result.pix[y*result.w+x] = value / 256
		}
	}
	return result
}

// decodeHint loads the hint PNG and composites it over white.
func decodeHint(data []byte) (*grayImage, error) {
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode hint: %w", err)
	}
	bounds := decoded.Bounds()
	canvas := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(canvas, canvas.Bounds(), decoded, bounds.Min, draw.Over)
	return rgbaToGray(canvas), nil
}

func gaussian(value float64) float64 {
	return math.Exp(-value)
}
