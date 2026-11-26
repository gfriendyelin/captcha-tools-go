package vision

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
)

// GrayImage is a single-channel 8-bit image.
type GrayImage struct {
	W, H int
	Pix  []uint8
}

// GrayFromRGB converts an RGB image to grayscale the way the Lotus pipeline
// does: alpha is composited onto white first (for RGBA input), then
// COLOR_RGBA2GRAY with OpenCV's BT.601 weights 0.299R + 0.587G + 0.114B.
func GrayFromRGB(im *RGB) *GrayImage {
	if im == nil {
		return nil
	}
	g := &GrayImage{W: im.W, H: im.H, Pix: make([]uint8, im.W*im.H)}
	for i := 0; i < im.W*im.H; i++ {
		r := float64(im.Pix[i*3]) * 0.299
		gg := float64(im.Pix[i*3+1]) * 0.587
		b := float64(im.Pix[i*3+2]) * 0.114
		g.Pix[i] = uint8(math.Round(r + gg + b))
	}
	return g
}

// bilateralFilter5 is a 5-diameter bilateral filter with sigmaColor=sigmaSpace=60
// approximating OpenCV's bilateralFilter(src, dst, 5, 60, 60) on a gray image.
func BilateralFilter5(g *GrayImage) *GrayImage {
	if g == nil {
		return nil
	}
	out := &GrayImage{W: g.W, H: g.H, Pix: make([]uint8, g.W*g.H)}
	const (
		sigmaColor = 60.0
		sigmaSpace = 60.0
		radius     = 2
	)
	// Precompute the spatial Gaussian weight for each offset in the radius.
	spaceW := make([][]float64, 2*radius+1)
	for dy := -radius; dy <= radius; dy++ {
		row := make([]float64, 2*radius+1)
		for dx := -radius; dx <= radius; dx++ {
			row[dx+radius] = math.Exp(-(float64(dx*dx + dy*dy)) / (2 * sigmaSpace * sigmaSpace))
		}
		spaceW[dy+radius] = row
	}
	colorW := make([]float64, 256)
	for d := 0; d < 256; d++ {
		colorW[d] = math.Exp(-(float64(d * d)) / (2 * sigmaColor * sigmaColor))
	}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			var sum, weightSum float64
			center := float64(g.Pix[y*g.W+x])
			for dy := -radius; dy <= radius; dy++ {
				yy := y + dy
				if yy < 0 || yy >= g.H {
					continue
				}
				for dx := -radius; dx <= radius; dx++ {
					xx := x + dx
					if xx < 0 || xx >= g.W {
						continue
					}
					val := float64(g.Pix[yy*g.W+xx])
					w := spaceW[dy+radius][dx+radius] * colorW[int(math.Abs(val-center))]
					sum += w * val
					weightSum += w
				}
			}
			if weightSum > 0 {
				out.Pix[y*g.W+x] = uint8(math.Round(sum / weightSum))
			} else {
				out.Pix[y*g.W+x] = g.Pix[y*g.W+x]
			}
		}
	}
	return out
}

// MorphGradient applies OpenCV MORPH_GRADIENT with a 3x3 rectangular kernel:
// dilate minus erode.
func MorphGradient(g *GrayImage) *GrayImage {
	if g == nil {
		return nil
	}
	dilated := morphDilate(g)
	eroded := morphErode(g)
	out := &GrayImage{W: g.W, H: g.H, Pix: make([]uint8, g.W*g.H)}
	for i := 0; i < g.W*g.H; i++ {
		v := int(dilated.Pix[i]) - int(eroded.Pix[i])
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		out.Pix[i] = uint8(v)
	}
	return out
}

func morphDilate(g *GrayImage) *GrayImage {
	out := &GrayImage{W: g.W, H: g.H, Pix: make([]uint8, g.W*g.H)}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			best := 0
			for dy := -1; dy <= 1; dy++ {
				yy := y + dy
				if yy < 0 || yy >= g.H {
					continue
				}
				for dx := -1; dx <= 1; dx++ {
					xx := x + dx
					if xx < 0 || xx >= g.W {
						continue
					}
					if v := int(g.Pix[yy*g.W+xx]); v > best {
						best = v
					}
				}
			}
			out.Pix[y*g.W+x] = uint8(best)
		}
	}
	return out
}

func morphErode(g *GrayImage) *GrayImage {
	out := &GrayImage{W: g.W, H: g.H, Pix: make([]uint8, g.W*g.H)}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			best := 255
			for dy := -1; dy <= 1; dy++ {
				yy := y + dy
				if yy < 0 || yy >= g.H {
					continue
				}
				for dx := -1; dx <= 1; dx++ {
					xx := x + dx
					if xx < 0 || xx >= g.W {
						continue
					}
					if v := int(g.Pix[yy*g.W+xx]); v < best {
						best = v
					}
				}
			}
			out.Pix[y*g.W+x] = uint8(best)
		}
	}
	return out
}

// TemplateMatchCCOEFFNormed runs OpenCV's TM_CCOEFF_NORMED over the search
// image for the template and returns one score per top-left anchor.
func TemplateMatchCCOEFFNormed(src, templ *GrayImage) []float64 {
	if src == nil || templ == nil || templ.W > src.W || templ.H > src.H {
		return nil
	}
	sw, sh := src.W, src.H
	tw, th := templ.W, templ.H
	outW, outH := sw-tw+1, sh-th+1
	result := make([]float64, outW*outH)
	// Template statistics: mean and norm.
	total := 0
	for _, v := range templ.Pix {
		total += int(v)
	}
	tMean := float64(total) / float64(tw*th)
	var tNorm float64
	for _, v := range templ.Pix {
		d := float64(v) - tMean
		tNorm += d * d
	}
	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			var sum, sumSq float64
			for ty := 0; ty < th; ty++ {
				rowBase := (y+ty)*sw + x
				for tx := 0; tx < tw; tx++ {
					sv := float64(src.Pix[rowBase+tx])
					sum += sv
					sumSq += sv * sv
				}
			}
			mean := sum / float64(tw*th)
			var numer, denomS, denomT float64
			for ty := 0; ty < th; ty++ {
				rowBase := (y+ty)*sw + x
				for tx := 0; tx < tw; tx++ {
					d := float64(src.Pix[rowBase+tx]) - mean
					numer += d * (float64(templ.Pix[ty*tw+tx]) - tMean)
					denomS += d * d
				}
			}
			denomT = tNorm
			denom := math.Sqrt(denomS * denomT)
			if denom > 0 {
				result[y*outW+x] = numer / denom
			} else {
				result[y*outW+x] = 0
			}
		}
	}
	return result
}

// SlideMatch replicates Lotus SlideSolver.solve: bilateral-filter both images,
// take the morphological gradient, optionally restrict the search band with the
// hint y, run TM_CCOEFF_NORMED, and return the x offset of the best match.
func SlideMatch(background, piece *RGB, hintY int) (offset int, ok bool) {
	if background == nil || piece == nil {
		return 0, false
	}
	bg := BilateralFilter5(GrayFromRGB(background))
	pc := BilateralFilter5(GrayFromRGB(piece))
	defer func() {
		// Nothing to free; GrayImage values are plain slices.
		_ = bg
		_ = pc
	}()
	bgEdge := MorphGradient(bg)
	pcEdge := MorphGradient(pc)
	search := bgEdge
	roiY, roiH := 0, bgEdge.H
	if hintY > 0 && hintY+pcEdge.H <= bgEdge.H {
		roiY, roiH = hintY, pcEdge.H
		search = ROIGray(bgEdge, roiY, roiH)
	}
	scores := TemplateMatchCCOEFFNormed(search, pcEdge)
	if scores == nil {
		return 0, false
	}
	bestX := 0
	bestVal := math.Inf(-1)
	for i, v := range scores {
		if v > bestVal {
			bestVal = v
			bestX = i % (search.W - pcEdge.W + 1)
		}
	}
	return bestX, true
}

// ROIGray extracts a horizontal band [y, y+h) from a gray image.
func ROIGray(g *GrayImage, y, h int) *GrayImage {
	if g == nil || y < 0 || h <= 0 || y+h > g.H {
		return nil
	}
	out := &GrayImage{W: g.W, H: h, Pix: make([]uint8, g.W*h)}
	for row := 0; row < h; row++ {
		copy(out.Pix[row*g.W:(row+1)*g.W], g.Pix[(y+row)*g.W:(y+row+1)*g.W])
	}
	return out
}

// RGBA is an unpacked 8-bit four-channel image.
type RGBA struct {
	W, H   int
	Stride int
	Pix    []uint8
}

func NewRGBA(w, h int) *RGBA {
	if w < 0 || h < 0 {
		panic("vision: negative image dimensions")
	}
	return &RGBA{W: w, H: h, Stride: 4 * w, Pix: make([]uint8, 4*w*h)}
}

// RGBAFromBytes decodes JPEG/PNG/GIF and keeps the alpha channel. PNG with
// transparency keeps per-pixel alpha; opaque formats yield alpha 255.
func RGBAFromBytes(data []byte) (*RGBA, error) {
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("vision: decode RGBA: %w", err)
	}
	b := im.Bounds()
	out := NewRGBA(b.Dx(), b.Dy())
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			c := color.NRGBAModel.Convert(im.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			i := y*out.Stride + 4*x
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = c.R, c.G, c.B, c.A
		}
	}
	return out, nil
}

// GrayFromRGBA converts RGBA to grayscale the way Lotus grayImage does:
//   - alpha "white": composite rgb*a + 255*(1-a) onto white, then BT.601 gray.
//   - alpha "zero": plain BT.601 gray, then transparent pixels (a==0) set to 0.
func GrayFromRGBA(im *RGBA, alphaMode string) *GrayImage {
	if im == nil {
		return nil
	}
	g := &GrayImage{W: im.W, H: im.H, Pix: make([]uint8, im.W*im.H)}
	for i := 0; i < im.W*im.H; i++ {
		r := float64(im.Pix[i*4])
		gg := float64(im.Pix[i*4+1])
		b := float64(im.Pix[i*4+2])
		a := float64(im.Pix[i*4+3])
		if alphaMode == "white" {
			alpha := a / 255.0
			r = r*alpha + 255*(1-alpha)
			gg = gg*alpha + 255*(1-alpha)
			b = b*alpha + 255*(1-alpha)
		}
		v := uint8(math.Round(r*0.299 + gg*0.587 + b*0.114))
		if alphaMode == "zero" && a == 0 {
			v = 0
		}
		g.Pix[i] = v
	}
	return g
}

// RGBFromRGBAWhite composites an RGBA image onto a white background and returns
// an opaque RGB copy. Used to feed RGBA captures into the *RGB-based helpers.
func RGBFromRGBAWhite(im *RGBA) *RGB {
	if im == nil {
		return nil
	}
	out := NewRGB(im.W, im.H)
	for i := 0; i < im.W*im.H; i++ {
		a := float64(im.Pix[i*4+3]) / 255.0
		out.Pix[i*3] = uint8(math.Round(float64(im.Pix[i*4])*a + 255*(1-a)))
		out.Pix[i*3+1] = uint8(math.Round(float64(im.Pix[i*4+1])*a + 255*(1-a)))
		out.Pix[i*3+2] = uint8(math.Round(float64(im.Pix[i*4+2])*a + 255*(1-a)))
	}
	return out
}
