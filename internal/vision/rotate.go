package vision

import (
	"math"
)

// RotateRGBWhite rotates an image clockwise by the given angle in degrees,
// filling the background with white, mirroring sharp's
// .flatten({ background: '#fff' }).rotate(angle, { background: '#fff' }).
// The output canvas is the same size as the input (sharp keeps the original
// dimensions when expand is not requested). Exact 90-degree steps use the
// transposition fast paths; other angles inverse-map each output pixel with
// bilinear sampling and white fill outside the source.
func RotateRGBWhite(im *RGB, angle float64) *RGB {
	if im == nil {
		return nil
	}
	normalized := math.Mod(angle, 360)
	if normalized < 0 {
		normalized += 360
	}
	switch normalized {
	case 0:
		return im.Clone()
	case 90:
		return rotate90CW(im)
	case 180:
		return rotate180(im)
	case 270:
		return rotate90CCW(im)
	}
	// General rotation: sharp rotates clockwise for positive angles.
	rad := -normalized * math.Pi / 180 // inverse mapping uses the negative
	cos := math.Cos(rad)
	sin := math.Sin(rad)
	cx := float64(im.W-1) / 2
	cy := float64(im.H-1) / 2
	out := NewRGB(im.W, im.H)
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			// Map output (x,y) back to source coordinates.
			dx := float64(x) - cx
			dy := float64(y) - cy
			sx := cos*dx - sin*dy + cx
			sy := sin*dx + cos*dy + cy
			r, g, b := sampleBilinear(im, sx, sy)
			i := y*out.Stride + 3*x
			out.Pix[i], out.Pix[i+1], out.Pix[i+2] = r, g, b
		}
	}
	return out
}

func rotate90CW(im *RGB) *RGB {
	out := NewRGB(im.H, im.W)
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			si := (im.H-1-y)*im.Stride + 3*x
			di := x*out.Stride + 3*y
			out.Pix[di], out.Pix[di+1], out.Pix[di+2] = im.Pix[si], im.Pix[si+1], im.Pix[si+2]
		}
	}
	return out
}

func rotate90CCW(im *RGB) *RGB {
	out := NewRGB(im.H, im.W)
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			si := y*im.Stride + 3*(im.W-1-x)
			di := x*out.Stride + 3*y
			out.Pix[di], out.Pix[di+1], out.Pix[di+2] = im.Pix[si], im.Pix[si+1], im.Pix[si+2]
		}
	}
	return out
}

func rotate180(im *RGB) *RGB {
	out := NewRGB(im.W, im.H)
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			si := (im.H-1-y)*im.Stride + 3*(im.W-1-x)
			di := y*out.Stride + 3*x
			out.Pix[di], out.Pix[di+1], out.Pix[di+2] = im.Pix[si], im.Pix[si+1], im.Pix[si+2]
		}
	}
	return out
}

// sampleBilinear reads an RGB pixel with bilinear interpolation; out-of-bounds
// samples yield white (255,255,255) to match sharp's white background fill.
func sampleBilinear(im *RGB, x, y float64) (uint8, uint8, uint8) {
	if x < 0 || y < 0 || x > float64(im.W-1) || y > float64(im.H-1) {
		return 255, 255, 255
	}
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	x1 := x0 + 1
	y1 := y0 + 1
	if x1 >= im.W {
		x1 = im.W - 1
	}
	if y1 >= im.H {
		y1 = im.H - 1
	}
	fx := x - float64(x0)
	fy := y - float64(y0)
	var r, g, b float64
	for dy := 0; dy <= 1; dy++ {
		for dx := 0; dx <= 1; dx++ {
			w := (1 - fx) * (1 - fy)
			if dx == 1 {
				w = fx * (1 - fy)
			}
			if dy == 1 {
				w = (1 - fx) * fy
			}
			if dx == 1 && dy == 1 {
				w = fx * fy
			}
			i := (y0+dy)*im.Stride + 3*(x0+dx)
			r += float64(im.Pix[i]) * w
			g += float64(im.Pix[i+1]) * w
			b += float64(im.Pix[i+2]) * w
		}
	}
	return uint8(clampByte(r)), uint8(clampByte(g)), uint8(clampByte(b))
}
