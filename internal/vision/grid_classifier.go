package vision

import (
	"fmt"
)

// GridClassifierInput mirrors Lotus GridClassifier.classify: images are
// [cells..., prompt] with the prompt last. Each image is decoded to natural
// size, optionally flipped, optionally JPEG round-tripped at the given quality
// (on the NATURAL size, before resizing), then short-side 232 BICUBIC resized
// and windowed to the top-left 224x224, then /255 + ImageNet normalized in
// NCHW order with float32 intermediates.
type GridClassifierInput struct {
	Images []*RGB
	Values []float32
	Shape  [4]int
}

// PreprocessGridClassifier builds the [N,3,224,224] classifier batch.
// quality==nil skips the JPEG round trip; flip mirrors horizontally.
func PreprocessGridClassifier(images []*RGB, quality *int, flip bool) (GridClassifierInput, error) {
	var out GridClassifierInput
	if len(images) == 0 {
		return out, fmt.Errorf("vision: empty classifier batch")
	}
	const size = 224
	out.Shape = [4]int{len(images), 3, size, size}
	pixels := size * size
	out.Values = make([]float32, len(images)*3*pixels)
	out.Images = make([]*RGB, len(images))
	for n, im := range images {
		if im == nil || im.W <= 0 || im.H <= 0 {
			return GridClassifierInput{}, fmt.Errorf("vision: invalid classifier image %d", n)
		}
		img := im
		// 1. Optional horizontal flip at natural size. Clone first so the
		// caller's images are never mutated (the multi-view classifier reuses
		// the same cells after the flip view).
		if flip {
			img = FlipHorizontal(img.Clone())
		}
		// 2. Optional JPEG round trip at natural size (Pillow default 4:2:0).
		if quality != nil {
			encoded, err := EncodeJPEGQuality(img, *quality)
			if err != nil {
				return GridClassifierInput{}, err
			}
			decoded, err := RGBFromBytes(encoded)
			if err != nil {
				return GridClassifierInput{}, err
			}
			img = decoded
		}
		// 3. Short-side 232 BICUBIC resize.
		var w, h int
		if img.W < img.H {
			w, h = 232, int(float64(232*img.H)/float64(img.W))
		} else {
			w, h = int(float64(232*img.W)/float64(img.H)), 232
		}
		resized, err := Resize(img, w, h, Bicubic)
		if err != nil {
			return GridClassifierInput{}, err
		}
		// 4. Top-left 224x224 window (out-of-bounds pixels stay black).
		cropped, err := CropBlack(resized, IntBox{0, 0, size, size})
		if err != nil {
			return GridClassifierInput{}, err
		}
		out.Images[n] = cropped
	}
	// 5. NCHW float32 /255 + ImageNet normalization (float32 intermediates).
	for n, im := range out.Images {
		for c := 0; c < 3; c++ {
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					v := float32(im.Pix[y*im.Stride+3*x+c]) / 255.0
					out.Values[((n*3+c)*size+y)*size+x] = (v - ImageNetMean[c]) / ImageNetStd[c]
				}
			}
		}
	}
	return out, nil
}

// FlipHorizontal mirrors an RGB image left-right in place and returns it.
func FlipHorizontal(im *RGB) *RGB {
	if im == nil {
		return nil
	}
	for y := 0; y < im.H; y++ {
		row := im.Pix[y*im.Stride : (y+1)*im.Stride]
		for x := 0; x < im.W/2; x++ {
			left, right := x*3, (im.W-1-x)*3
			for c := 0; c < 3; c++ {
				row[left+c], row[right+c] = row[right+c], row[left+c]
			}
		}
	}
	return im
}
