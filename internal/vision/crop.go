package vision

import "fmt"

var v3Rects = [10]IntBox{
	{0, 0, 112, 112}, {116, 0, 228, 112}, {232, 0, 344, 112},
	{0, 116, 112, 228}, {116, 116, 228, 228}, {232, 116, 344, 228},
	{0, 232, 112, 344}, {116, 232, 228, 344}, {232, 232, 344, 344},
	{2, 344, 42, 384},
}

// V3CropRects returns the nine row-major cells followed by the target crop.
func V3CropRects() [10]IntBox { return v3Rects }

// CropBlack matches PIL crop behavior: pixels outside src remain black.
func CropBlack(src *RGB, r IntBox) (*RGB, error) {
	if src == nil {
		return nil, fmt.Errorf("vision: nil image")
	}
	if r.Width() < 0 || r.Height() < 0 {
		return nil, fmt.Errorf("vision: inverted crop rectangle %+v", r)
	}
	out := NewRGB(r.Width(), r.Height())
	for y := 0; y < out.H; y++ {
		sy := r.Y1 + y
		if sy < 0 || sy >= src.H {
			continue
		}
		for x := 0; x < out.W; x++ {
			sx := r.X1 + x
			if sx < 0 || sx >= src.W {
				continue
			}
			si := src.offset(sx, sy)
			di := out.offset(x, y)
			copy(out.Pix[di:di+3], src.Pix[si:si+3])
		}
	}
	return out, nil
}

// V3Crops contains the ten JPEG-ready crops and the synthesized nine preview.
type V3Crops struct {
	Cells [10]*RGB
	Nine  *RGB
}

// CropV3 implements crop_image_v3, including its 300x261 black canvas and
// BICUBIC 100x86 cell previews. Call EncodeJPEG on Cells/Nine to reproduce the
// file-format round trip performed by the Python service.
func CropV3(src *RGB) (V3Crops, error) {
	var out V3Crops
	for i, r := range v3Rects {
		cell, err := CropBlack(src, r)
		if err != nil {
			return out, err
		}
		out.Cells[i] = cell
	}
	out.Nine = NewRGB(300, 261)
	for i := 0; i < 9; i++ {
		cell, err := Resize(out.Cells[i], 100, 86, Bicubic)
		if err != nil {
			return V3Crops{}, err
		}
		out.Nine.Paste(cell, (i%3)*100, (i/3)*86)
	}
	return out, nil
}

// CropGrid performs the legacy equal-thirds crop in row-major order.
func CropGrid(src *RGB) ([9]*RGB, error) {
	var out [9]*RGB
	if src == nil {
		return out, fmt.Errorf("vision: nil image")
	}
	gw, gh := src.W/3, src.H/3
	for row := 0; row < 3; row++ {
		for col := 0; col < 3; col++ {
			left, upper := col*gw, row*gh
			cell, err := CropBlack(src, IntBox{left, upper, left + gw, upper + gh})
			if err != nil {
				return out, err
			}
			out[row*3+col] = cell
		}
	}
	return out, nil
}
