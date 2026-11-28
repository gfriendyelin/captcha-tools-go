package svgcaptcha

import (
	"math"
	"sort"
)

// medianBlur3 reproduces cv2.medianBlur(..., 3): a 3x3 median with replicated
// borders.
func medianBlur3(src *grayImage) *grayImage {
	return medianBlur(src, 1)
}

func medianBlur(src *grayImage, radius int) *grayImage {
	result := newGray(src.w, src.h)
	window := make([]float64, 0, (2*radius+1)*(2*radius+1))
	for y := 0; y < src.h; y++ {
		for x := 0; x < src.w; x++ {
			window = window[:0]
			for dy := -radius; dy <= radius; dy++ {
				for dx := -radius; dx <= radius; dx++ {
					window = append(window, src.at(clamp(x+dx, src.w), clamp(y+dy, src.h)))
				}
			}
			sort.Float64s(window)
			result.pix[y*src.w+x] = window[len(window)/2]
		}
	}
	return result
}

// bilateralFilter approximates cv2.bilateralFilter(d=5, sigmaColor=50,
// sigmaSpace=50). With such a large sigmaSpace the spatial term is nearly
// uniform, so only the colour distance carries weight.
func bilateralFilter(src *grayImage, radius int, sigmaColor float64) *grayImage {
	result := newGray(src.w, src.h)
	colorWeight := make([]float64, 256)
	for diff := range colorWeight {
		colorWeight[diff] = math.Exp(-float64(diff*diff) / (2 * sigmaColor * sigmaColor))
	}
	for y := 0; y < src.h; y++ {
		for x := 0; x < src.w; x++ {
			center := src.at(x, y)
			var sum, weight float64
			for dy := -radius; dy <= radius; dy++ {
				for dx := -radius; dx <= radius; dx++ {
					value := src.at(clamp(x+dx, src.w), clamp(y+dy, src.h))
					diff := int(math.Abs(value - center))
					if diff < 0 {
						diff = -diff
					}
					if diff > 255 {
						diff = 255
					}
					w := colorWeight[diff]
					sum += value * w
					weight += w
				}
			}
			if weight == 0 {
				result.pix[y*src.w+x] = center
				continue
			}
			result.pix[y*src.w+x] = sum / weight
		}
	}
	return result
}

// canny reproduces the edge output the reference relies on: Sobel gradients,
// non-maximum suppression along the gradient direction, then a 50/150 double
// threshold with hysteresis.
func canny(src *grayImage, low, high float64) *grayImage {
	w, h := src.w, src.h
	magnitude := make([]float64, w*h)
	direction := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gx := src.at(reflect101(x+1, w), reflect101(y-1, h)) +
				2*src.at(reflect101(x+1, w), reflect101(y, h)) +
				src.at(reflect101(x+1, w), reflect101(y+1, h)) -
				src.at(reflect101(x-1, w), reflect101(y-1, h)) -
				2*src.at(reflect101(x-1, w), reflect101(y, h)) -
				src.at(reflect101(x-1, w), reflect101(y+1, h))
			gy := src.at(reflect101(x-1, w), reflect101(y+1, h)) +
				2*src.at(reflect101(x, w), reflect101(y+1, h)) +
				src.at(reflect101(x+1, w), reflect101(y+1, h)) -
				src.at(reflect101(x-1, w), reflect101(y-1, h)) -
				2*src.at(reflect101(x, w), reflect101(y-1, h)) -
				src.at(reflect101(x+1, w), reflect101(y-1, h))
			magnitude[y*w+x] = math.Hypot(gx, gy)
			direction[y*w+x] = math.Atan2(gy, gx)
		}
	}

	suppressed := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			angle := direction[y*w+x] * 180 / math.Pi
			if angle < 0 {
				angle += 180
			}
			value := magnitude[y*w+x]
			var a, b float64
			switch {
			case angle < 22.5 || angle >= 157.5:
				a, b = magnitude[y*w+clamp(x-1, w)], magnitude[y*w+clamp(x+1, w)]
			case angle < 67.5:
				a = magnitude[clamp(y-1, h)*w+clamp(x+1, w)]
				b = magnitude[clamp(y+1, h)*w+clamp(x-1, w)]
			case angle < 112.5:
				a, b = magnitude[clamp(y-1, h)*w+x], magnitude[clamp(y+1, h)*w+x]
			default:
				a = magnitude[clamp(y-1, h)*w+clamp(x-1, w)]
				b = magnitude[clamp(y+1, h)*w+clamp(x+1, w)]
			}
			if value >= a && value >= b {
				suppressed[y*w+x] = value
			}
		}
	}

	result := newGray(w, h)
	stack := make([]int, 0, w*h)
	for index, value := range suppressed {
		if value >= high {
			result.pix[index] = 255
			stack = append(stack, index)
		}
	}
	for len(stack) > 0 {
		index := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		x, y := index%w, index/w
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				nx, ny := x+dx, y+dy
				if nx < 0 || ny < 0 || nx >= w || ny >= h {
					continue
				}
				neighbor := ny*w + nx
				if result.pix[neighbor] != 0 || suppressed[neighbor] < low {
					continue
				}
				result.pix[neighbor] = 255
				stack = append(stack, neighbor)
			}
		}
	}
	return result
}

// rotate rotates around the image center like cv2.warpAffine with
// cv2.getRotationMatrix2D, using bilinear sampling and a black border.
func rotate(src *grayImage, degrees float64) *grayImage {
	if degrees == 0 {
		return src
	}
	radians := degrees * math.Pi / 180
	alpha, beta := math.Cos(radians), math.Sin(radians)
	cx, cy := float64(src.w)/2, float64(src.h)/2
	// Forward matrix from getRotationMatrix2D, then invert it for sampling.
	a, b := alpha, beta
	d, e := -beta, alpha
	c := (1-alpha)*cx - beta*cy
	f := beta*cx + (1-alpha)*cy
	det := a*e - b*d
	if det == 0 {
		return src
	}
	inv := [6]float64{
		e / det, -b / det, (b*f - c*e) / det,
		-d / det, a / det, (c*d - a*f) / det,
	}
	result := newGray(src.w, src.h)
	for y := 0; y < src.h; y++ {
		for x := 0; x < src.w; x++ {
			sx := inv[0]*float64(x) + inv[1]*float64(y) + inv[2]
			sy := inv[3]*float64(x) + inv[4]*float64(y) + inv[5]
			result.pix[y*src.w+x] = bilinear(src, sx, sy)
		}
	}
	return result
}

// resizeEdge mimics cv2.resize with INTER_AREA: nearest for upscaling (what
// INTER_AREA degenerates to) and an area average for downscaling.
func resizeEdge(src *grayImage, scale float64) *grayImage {
	if scale == 1 {
		return src
	}
	width := int(float64(src.w)*scale + 0.5)
	height := int(float64(src.h)*scale + 0.5)
	if width < 1 || height < 1 {
		return newGray(1, 1)
	}
	result := newGray(width, height)
	if scale >= 1 {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				result.pix[y*width+x] = src.at(clamp(int(float64(x)/scale), src.w), clamp(int(float64(y)/scale), src.h))
			}
		}
		return result
	}
	radius := int(math.Ceil(1/scale - 0.5))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sx := int(float64(x) / scale)
			sy := int(float64(y) / scale)
			var sum float64
			var count int
			for dy := 0; dy <= radius; dy++ {
				for dx := 0; dx <= radius; dx++ {
					sum += src.at(clamp(sx+dx, src.w), clamp(sy+dy, src.h))
					count++
				}
			}
			result.pix[y*width+x] = sum / float64(count)
		}
	}
	return result
}

func bilinear(src *grayImage, x, y float64) float64 {
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	v00 := src.at(x0, y0)
	v10 := src.at(x0+1, y0)
	v01 := src.at(x0, y0+1)
	v11 := src.at(x0+1, y0+1)
	top := v00*(1-fx) + v10*fx
	bottom := v01*(1-fx) + v11*fx
	return top*(1-fy) + bottom*fy
}

func clamp(value, limit int) int {
	if value < 0 {
		return 0
	}
	if value >= limit {
		return limit - 1
	}
	return value
}

func reflect101(value, limit int) int {
	if limit == 1 {
		return 0
	}
	for value < 0 || value >= limit {
		if value < 0 {
			value = -value
		} else {
			value = 2*limit - value - 2
		}
	}
	return value
}
