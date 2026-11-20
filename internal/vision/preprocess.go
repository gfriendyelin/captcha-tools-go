package vision

import "fmt"

var (
	ImageNetMean = [3]float32{0.485, 0.456, 0.406}
	ImageNetStd  = [3]float32{0.229, 0.224, 0.225}
)

// Tensor is a contiguous NCHW float32 tensor suitable for an ONNX Runtime Go
// binding. Shape is [batch,channels,height,width].
type Tensor struct {
	Shape [4]int
	Data  []float32
}

func (t Tensor) At(n, c, y, x int) float32 {
	return t.Data[((n*t.Shape[1]+c)*t.Shape[2]+y)*t.Shape[3]+x]
}

// PreprocessPDL applies short-side 232 BICUBIC resize, top-left 224 crop,
// float32 /255 and ImageNet normalization to each image. Target is last.
func PreprocessPDL(images []*RGB) (Tensor, error) {
	resized := make([]*RGB, len(images))
	for i, im := range images {
		if im == nil || im.W <= 0 || im.H <= 0 {
			return Tensor{}, fmt.Errorf("vision: invalid PDL image %d", i)
		}
		var w, h int
		if im.W < im.H {
			w, h = 232, int(232*float64(im.H)/float64(im.W))
		} else {
			w, h = int(232*float64(im.W)/float64(im.H)), 232
		}
		r, err := Resize(im, w, h, Bicubic)
		if err != nil {
			return Tensor{}, err
		}
		resized[i], err = CropBlack(r, IntBox{0, 0, 224, 224})
		if err != nil {
			return Tensor{}, err
		}
	}
	return batchCHW(resized, true)
}

// PreprocessLegacy applies the old direct 224x224 BICUBIC transformation.
func PreprocessLegacy(images []*RGB) (Tensor, error) {
	resized := make([]*RGB, len(images))
	for i, im := range images {
		r, err := Resize(im, 224, 224, Bicubic)
		if err != nil {
			return Tensor{}, fmt.Errorf("vision: legacy image %d: %w", i, err)
		}
		resized[i] = r
	}
	return batchCHW(resized, true)
}

// DFINEInput contains both ONNX inputs in source order: images then [w,h].
type DFINEInput struct {
	Images          Tensor
	OrigTargetSizes [2]int64
}

func PreprocessDFINE(im *RGB) (DFINEInput, error) {
	if im == nil {
		return DFINEInput{}, fmt.Errorf("vision: nil D-FINE image")
	}
	r, err := Resize(im, 320, 320, Bilinear)
	if err != nil {
		return DFINEInput{}, err
	}
	t, err := batchCHW([]*RGB{r}, false)
	return DFINEInput{Images: t, OrigTargetSizes: [2]int64{int64(im.W), int64(im.H)}}, err
}

func PreprocessYOLO(im *RGB) (Tensor, error) {
	r, err := Resize(im, 384, 384, Bilinear)
	if err != nil {
		return Tensor{}, err
	}
	return batchCHW([]*RGB{r}, false)
}

func PreprocessDINO(im *RGB) (Tensor, error) {
	r, err := Resize(im, 224, 224, Lanczos)
	if err != nil {
		return Tensor{}, err
	}
	return batchCHW([]*RGB{r}, true)
}

func batchCHW(images []*RGB, normalize bool) (Tensor, error) {
	if len(images) == 0 {
		return Tensor{}, fmt.Errorf("vision: empty image batch")
	}
	h, w := images[0].H, images[0].W
	t := Tensor{Shape: [4]int{len(images), 3, h, w}, Data: make([]float32, len(images)*3*h*w)}
	for n, im := range images {
		if im == nil || im.W != w || im.H != h {
			return Tensor{}, fmt.Errorf("vision: inconsistent image at batch index %d", n)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				for c := 0; c < 3; c++ {
					v := float32(im.Pix[y*im.Stride+3*x+c]) / 255.0
					if normalize {
						v = (v - ImageNetMean[c]) / ImageNetStd[c]
					}
					t.Data[((n*3+c)*h+y)*w+x] = v
				}
			}
		}
	}
	return t, nil
}

// DINOPatchTokens drops the first five tokens from a batch-one [1,T,D]
// last_hidden_state and returns a contiguous [1,T-5,D] slice.
func DINOPatchTokens(lastHidden []float32, tokens, dim int) ([]float32, error) {
	if tokens < 5 || dim <= 0 || len(lastHidden) != tokens*dim {
		return nil, fmt.Errorf("vision: invalid DINO shape tokens=%d dim=%d len=%d", tokens, dim, len(lastHidden))
	}
	out := make([]float32, (tokens-5)*dim)
	copy(out, lastHidden[5*dim:])
	return out, nil
}
