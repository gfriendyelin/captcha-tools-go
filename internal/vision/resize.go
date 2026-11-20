package vision

import (
	"fmt"
	"math"
)

// Resample identifies the Pillow-compatible reconstruction filter.
type Resample int

const (
	Bilinear Resample = iota
	Bicubic
	Lanczos
)

const precisionBits = 22

// Resize reproduces Pillow's RGB resampling strategy: filter support expands
// during downscaling, coefficients are generated in float64 and quantized to
// signed fixed point with 22 fractional bits, horizontal before vertical,
// and each pass rounds and clamps back to uint8. It is deliberately not a
// nearest-neighbor or floating-point-image substitute.
func Resize(src *RGB, width, height int, filter Resample) (*RGB, error) {
	if src == nil {
		return nil, fmt.Errorf("vision: nil image")
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("vision: invalid resize dimensions %dx%d", width, height)
	}
	if filter < Bilinear || filter > Lanczos {
		return nil, fmt.Errorf("vision: unsupported resample filter %d", filter)
	}
	if width == src.W && height == src.H {
		return src.Clone(), nil
	}

	current := src
	if width != src.W {
		contrib, err := makeContributors(src.W, width, filter)
		if err != nil {
			return nil, err
		}
		tmp := NewRGB(width, src.H)
		for y := 0; y < src.H; y++ {
			for x, c := range contrib {
				for channel := 0; channel < 3; channel++ {
					var sum int64
					for j, k := range c.coeff {
						sum += int64(src.Pix[y*src.Stride+3*(c.start+j)+channel]) * int64(k)
					}
					tmp.Pix[y*tmp.Stride+3*x+channel] = fixedToUint8(sum)
				}
			}
		}
		current = tmp
	}
	if height == current.H {
		return current, nil
	}
	contrib, err := makeContributors(current.H, height, filter)
	if err != nil {
		return nil, err
	}
	out := NewRGB(width, height)
	for y, c := range contrib {
		for x := 0; x < width; x++ {
			for channel := 0; channel < 3; channel++ {
				var sum int64
				for j, k := range c.coeff {
					sum += int64(current.Pix[(c.start+j)*current.Stride+3*x+channel]) * int64(k)
				}
				out.Pix[y*out.Stride+3*x+channel] = fixedToUint8(sum)
			}
		}
	}
	return out, nil
}

type contributors struct {
	start int
	coeff []int32
}

func makeContributors(input, output int, filter Resample) ([]contributors, error) {
	var support float64
	switch filter {
	case Bilinear:
		support = 1
	case Bicubic:
		support = 2
	case Lanczos:
		support = 3
	default:
		return nil, fmt.Errorf("vision: unsupported resample filter %d", filter)
	}
	scale := float64(input) / float64(output)
	filterScale := math.Max(scale, 1)
	support *= filterScale
	out := make([]contributors, output)
	for x := 0; x < output; x++ {
		center := (float64(x) + 0.5) * scale
		xmin := int(center - support + 0.5)
		if xmin < 0 {
			xmin = 0
		}
		xmax := int(center + support + 0.5)
		if xmax > input {
			xmax = input
		}
		coeff := make([]float64, xmax-xmin)
		var sum float64
		for j := range coeff {
			v := filterValue(filter, (float64(xmin+j)+0.5-center)/filterScale)
			coeff[j] = v
			sum += v
		}
		fixed := make([]int32, len(coeff))
		if sum != 0 {
			for j, v := range coeff {
				normalized := v / sum * (1 << precisionBits)
				// Pillow rounds signed coefficients away from zero before the C cast.
				if normalized < 0 {
					fixed[j] = int32(normalized - 0.5)
				} else {
					fixed[j] = int32(normalized + 0.5)
				}
			}
		}
		out[x] = contributors{start: xmin, coeff: fixed}
	}
	return out, nil
}

func filterValue(filter Resample, x float64) float64 {
	if x < 0 {
		x = -x
	}
	switch filter {
	case Bilinear:
		if x < 1 {
			return 1 - x
		}
	case Bicubic:
		const a = -0.5
		if x < 1 {
			return ((a+2)*x-(a+3))*x*x + 1
		}
		if x < 2 {
			return (((x-5)*x+8)*x - 4) * a
		}
	case Lanczos:
		if x == 0 {
			return 1
		}
		if x < 3 {
			x *= math.Pi
			return 3 * math.Sin(x) * math.Sin(x/3) / (x * x)
		}
	}
	return 0
}

func fixedToUint8(sum int64) uint8 {
	v := (sum + (1 << (precisionBits - 1))) >> precisionBits
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
