package vision

import (
	"fmt"
	"math"
	"sort"
)

// DetectTensor is the result of letterboxing an image to a model input size.
type DetectTensor struct {
	Values []float32
	Scale  float64
	PadX   float64
	PadY   float64
	Width  int
	Height int
}

// Letterbox reproduces Lotus letterbox: scale = min(w/imgW, h/imgH), resize
// with linear interpolation, center-pad with 114/255, NCHW float32. No BGR
// swap (the YOLOX path does that separately).
func Letterbox(im *RGB, width, height int) (DetectTensor, error) {
	if im == nil || im.W <= 0 || im.H <= 0 || width <= 0 || height <= 0 {
		return DetectTensor{}, fmt.Errorf("vision: invalid letterbox input")
	}
	scale := math.Min(float64(width)/float64(im.W), float64(height)/float64(im.H))
	w := int(math.Round(float64(im.W) * scale))
	h := int(math.Round(float64(im.H) * scale))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	padX := math.Floor(float64(width-w) / 2)
	padY := math.Floor(float64(height-h) / 2)
	resized, err := Resize(im, w, h, Bilinear)
	if err != nil {
		return DetectTensor{}, err
	}
	values := make([]float32, 3*width*height)
	for i := range values {
		values[i] = 114.0 / 255.0
	}
	for c := 0; c < 3; c++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				values[c*width*height+(y+int(padY))*width+x+int(padX)] = float32(resized.Pix[(y*w+x)*3+c]) / 255.0
			}
		}
	}
	return DetectTensor{Values: values, Scale: scale, PadX: padX, PadY: padY, Width: width, Height: height}, nil
}

// DecodeDetect decodes a [1, channels, count] (transposed) or [1, count,
// channels] detection tensor, matching Lotus detector.ts detect(). boxFormat is
// "xyxy" or "" (cxcywh). Confidence threshold, then suppress(iou, limit), then
// trunc toward zero and drop degenerate boxes.
func DecodeDetect(data []float32, channels, count int, transposed bool, boxFormat string, scale, padX, padY float64, imageWidth, imageHeight int, confidence, iou float32, limit int) ([]SymbolBox, error) {
	if channels < 5 || count < 1 || len(data) != channels*count {
		return nil, fmt.Errorf("vision: invalid detection shape channels=%d count=%d len=%d", channels, count, len(data))
	}
	at := func(n, c int) float32 {
		if transposed {
			return data[c*count+n]
		}
		return data[n*channels+c]
	}
	clip := func(v, max float64) float64 {
		if v < 0 {
			return 0
		}
		if v > max {
			return max
		}
		return v
	}
	var detections []floatBox
	for n := 0; n < count; n++ {
		score := at(n, 4)
		label := 0
		for c := 5; c < channels; c++ {
			if at(n, c) > score {
				score = at(n, c)
				label = c - 4
			}
		}
		if score < confidence {
			continue
		}
		a, b, c, d := float64(at(n, 0)), float64(at(n, 1)), float64(at(n, 2)), float64(at(n, 3))
		var x1, y1, x2, y2 float64
		if boxFormat == "xyxy" {
			x1, y1, x2, y2 = a, b, c, d
		} else {
			x1, y1 = a-c/2, b-d/2
			x2, y2 = a+c/2, b+d/2
		}
		x1 = clip((x1-padX)/scale, float64(imageWidth))
		y1 = clip((y1-padY)/scale, float64(imageHeight))
		x2 = clip((x2-padX)/scale, float64(imageWidth))
		y2 = clip((y2-padY)/scale, float64(imageHeight))
		detections = append(detections, floatBox{X: x1, Y: y1, Width: x2 - x1, Height: y2 - y1, Score: score, Label: label})
	}
	kept := suppressFloats(detections, iou, limit)
	out := make([]SymbolBox, 0, len(kept))
	for _, b := range kept {
		x := int(math.Trunc(b.X))
		y := int(math.Trunc(b.Y))
		w := int(math.Trunc(b.X+b.Width)) - x
		h := int(math.Trunc(b.Y+b.Height)) - y
		if w > 0 && h > 0 {
			out = append(out, SymbolBox{X: x, Y: y, Width: w, Height: h, Score: b.Score, Label: b.Label})
		}
	}
	return out, nil
}

// floatBox is a float64 intermediate for detection boxes before truncation.
type floatBox struct {
	X, Y, Width, Height float64
	Score               float32
	Label               int
}

// suppressFloats mirrors Lotus suppress() on float boxes with IOU threshold and
// limit (0 = unlimited).
func suppressFloats(boxes []floatBox, threshold float32, limit int) []floatBox {
	ordered := append([]floatBox(nil), boxes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Score > ordered[j].Score })
	kept := make([]floatBox, 0, len(ordered))
	for len(ordered) > 0 && (limit <= 0 || len(kept) < limit) {
		box := ordered[0]
		kept = append(kept, box)
		rest := ordered[1:]
		next := make([]floatBox, 0, len(rest))
		for _, other := range rest {
			interX := math.Max(0, math.Min(box.X+box.Width, other.X+other.Width)-math.Max(box.X, other.X))
			interY := math.Max(0, math.Min(box.Y+box.Height, other.Y+other.Height)-math.Max(box.Y, other.Y))
			intersection := interX * interY
			den := math.Max(1e-9, box.Width*box.Height+other.Width*other.Height-intersection)
			if intersection/den <= float64(threshold) {
				next = append(next, other)
			}
		}
		ordered = next
	}
	return kept
}

// suppressBoxes is the Lotus suppress: score-descending NMS with IOU threshold
// and a limit, using the symbolic area denominator.
func suppressBoxes(boxes []SymbolBox, threshold float32, limit int) []SymbolBox {
	ordered := append([]SymbolBox(nil), boxes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Score > ordered[j].Score })
	kept := make([]SymbolBox, 0, len(ordered))
	for len(ordered) > 0 && (limit <= 0 || len(kept) < limit) {
		box := ordered[0]
		kept = append(kept, box)
		rest := ordered[1:]
		next := make([]SymbolBox, 0, len(rest))
		for _, other := range rest {
			interX := math.Max(0, math.Min(float64(box.X+box.Width), float64(other.X+other.Width))-math.Max(float64(box.X), float64(other.X)))
			interY := math.Max(0, math.Min(float64(box.Y+box.Height), float64(other.Y+other.Height))-math.Max(float64(box.Y), float64(other.Y)))
			intersection := interX * interY
			den := math.Max(1e-9, float64(box.Width*box.Height+other.Width*other.Height)-intersection)
			if intersection/den <= float64(threshold) {
				next = append(next, other)
			}
		}
		ordered = next
	}
	return kept
}

// YoloXInput letterboxes to 416 and swaps BGR (Lotus yoloXInput).
func YoloXInput(im *RGB) (DetectTensor, error) {
	if im == nil || im.W <= 0 || im.H <= 0 {
		return DetectTensor{}, fmt.Errorf("vision: invalid yoloX input")
	}
	const size = 416
	scale := math.Min(float64(size)/float64(im.W), float64(size)/float64(im.H))
	w := int(math.Trunc(float64(im.W) * scale))
	h := int(math.Trunc(float64(im.H) * scale))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	resized, err := Resize(im, w, h, Bilinear)
	if err != nil {
		return DetectTensor{}, err
	}
	data := make([]float32, 3*size*size)
	for i := range data {
		data[i] = 114.0
	}
	for c := 0; c < 3; c++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				data[c*size*size+y*size+x] = float32(resized.Pix[(y*w+x)*3+2-c])
			}
		}
	}
	return DetectTensor{Values: data, Scale: scale, Width: size, Height: size}, nil
}

// DecodeYoloX decodes the [1, 3549, channels] YOLOX output with strides
// 8/16/32 (Lotus decodeYoloX), then suppress(0.45, 12).
func DecodeYoloX(data []float32, channels int, scale float64, imageWidth, imageHeight int, confidence float32) ([]SymbolBox, error) {
	if channels < 6 {
		return nil, fmt.Errorf("vision: invalid YOLOX channels %d", channels)
	}
	const size = 416
	var boxes []SymbolBox
	row := 0
	for _, stride := range []int{8, 16, 32} {
		side := size / stride
		for gy := 0; gy < side; gy++ {
			for gx := 0; gx < side; gx++ {
				offset := row * channels
				best := float32(0)
				label := 0
				for c := 5; c < channels; c++ {
					if data[offset+c] > best {
						best = data[offset+c]
						label = c - 5
					}
				}
				score := data[offset+4] * best
				if score <= confidence {
					row++
					continue
				}
				x := (float64(data[offset]) + float64(gx)) * float64(stride) / scale
				y := (float64(data[offset+1]) + float64(gy)) * float64(stride) / scale
				w := math.Exp(float64(data[offset+2])) * float64(stride) / scale
				h := math.Exp(float64(data[offset+3])) * float64(stride) / scale
				left := math.Max(0, x-w/2)
				top := math.Max(0, y-h/2)
				right := math.Min(float64(imageWidth), x+w/2)
				bottom := math.Min(float64(imageHeight), y+h/2)
				row++
				if math.IsNaN(left) || math.IsNaN(top) || math.IsNaN(right) || math.IsNaN(bottom) || math.IsInf(left, 0) || math.IsInf(top, 0) || math.IsInf(right, 0) || math.IsInf(bottom, 0) {
					continue
				}
				if right <= left || bottom <= top {
					continue
				}
				boxes = append(boxes, SymbolBox{
					X:      int(left),
					Y:      int(top),
					Width:  int(right - left),
					Height: int(bottom - top),
					Score:  score,
					Label:  label,
				})
			}
		}
	}
	return suppressBoxes(boxes, 0.45, 12), nil
}

// BlurRGB applies a separable Gaussian blur approximating sharp blur(sigma).
// sigma 0.8 uses a 5x5 kernel; the result is clamped to 8-bit.
func BlurRGB(im *RGB, sigma float64) *RGB {
	if im == nil || sigma <= 0 {
		return im
	}
	radius := int(math.Ceil(3 * sigma))
	if radius < 1 {
		radius = 1
	}
	// 5x5 for sigma 0.8 (sharp/libvips radius ~ 1.4*sigma -> 2 -> 5 wide).
	kernel := make([]float64, 2*radius+1)
	var sum float64
	for i := -radius; i <= radius; i++ {
		v := math.Exp(-float64(i*i) / (2 * sigma * sigma))
		kernel[i+radius] = v
		sum += v
	}
	for i := range kernel {
		kernel[i] /= sum
	}
	tmp := NewRGB(im.W, im.H)
	out := NewRGB(im.W, im.H)
	for c := 0; c < 3; c++ {
		// horizontal pass
		for y := 0; y < im.H; y++ {
			for x := 0; x < im.W; x++ {
				var acc float64
				for k := -radius; k <= radius; k++ {
					sx := x + k
					if sx < 0 {
						sx = 0
					}
					if sx >= im.W {
						sx = im.W - 1
					}
					acc += float64(im.Pix[y*im.Stride+3*sx+c]) * kernel[k+radius]
				}
				tmp.Pix[y*tmp.Stride+3*x+c] = uint8(clampByte(acc))
			}
		}
		// vertical pass
		for y := 0; y < im.H; y++ {
			for x := 0; x < im.W; x++ {
				var acc float64
				for k := -radius; k <= radius; k++ {
					sy := y + k
					if sy < 0 {
						sy = 0
					}
					if sy >= im.H {
						sy = im.H - 1
					}
					acc += float64(tmp.Pix[sy*tmp.Stride+3*x+c]) * kernel[k+radius]
				}
				out.Pix[y*out.Stride+3*x+c] = uint8(clampByte(acc))
			}
		}
	}
	return out
}

func clampByte(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
