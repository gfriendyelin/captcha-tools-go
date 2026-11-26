package inference

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"

	"github.com/kafuneri/captcha-tools-go/internal/vision"
	ort "github.com/yalue/onnxruntime_go"
)

// directionHashes maps the md5 of each supported direction prompt to its
// direction name, mirroring Lotus DirectionSolver.
var directionHashes = map[string]string{
	"8da090c135ff029f3b5e19f4c44f73c8": "u",
	"cb0eaa639b2117a69a81af3d8c1496a1": "d",
	"315ce8665e781dabcd1eb09d3e604803": "l",
	"38bd9dda695098c7dfad74c921923a7d": "lu",
	"502e51dbabf411beba2dcd55fd38ebbd": "ld",
	"2b2387f566f6a03ed594d4d7cfda471f": "r",
	"78dc29045d587ad054c7353732df53c5": "ru",
	"23ef93e6b0e0df0e15b66667c99a5fb4": "rd",
}

// DirectionPrompt reports whether an image is one of the known direction
// prompts, so protocol layers can skip the direction solver for other symbols.
func DirectionPrompt(prompt []byte) bool {
	sum := md5.Sum(prompt)
	_, ok := directionHashes[hex.EncodeToString(sum[:])]
	return ok
}

// DirectionSolve mirrors Lotus DirectionSolver.solve: each prompt's md5 selects
// a direction; the icon-direction detector finds boxes; the first unused box
// whose class name matches the direction is the answer, returned as a
// normalized center point.
func (r *Registry) DirectionSolve(ctx context.Context, image []byte, prompts []string) ([]vision.Point, error) {
	if r == nil {
		return nil, fmt.Errorf("nil registry")
	}
	if len(prompts) == 0 {
		return nil, fmt.Errorf("direction needs prompts")
	}
	lease, err := r.acquire(ctx, "direction")
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*DirectionResource)
	if !ok {
		return nil, fmt.Errorf("direction resource has type %T", lease.Resource())
	}
	img, err := vision.RGBFromBytes(image)
	if err != nil {
		return nil, fmt.Errorf("direction image: %w", err)
	}
	// The icon-direction model classes are fixed (metadata names
	// "{0: 'u', 1: 'd', 2: 'l', 3: 'r', 4: 'lu', 5: 'ld', 6: 'ru', 7: 'rd'}").
	names := []string{"u", "d", "l", "r", "lu", "ld", "ru", "rd"}
	tensor, err := vision.Letterbox(img, 640, 640)
	if err != nil {
		return nil, err
	}
	ortTensor, err := ort.NewTensor(ort.Shape{1, 3, 640, 640}, tensor.Values)
	if err != nil {
		return nil, err
	}
	outputs, err := resource.Detect.Run(ctx, []ort.Value{ortTensor})
	if err != nil {
		return nil, err
	}
	if len(outputs) != 1 || len(outputs[0].Float32) == 0 {
		return nil, fmt.Errorf("unexpected icon-direction output")
	}
	shape := outputs[0].Shape
	if len(shape) != 3 {
		return nil, fmt.Errorf("icon-direction output shape %v", shape)
	}
	transposed := shape[1] < shape[2]
	channels, count := int(shape[1]), int(shape[2])
	if !transposed {
		channels, count = int(shape[2]), int(shape[1])
	}
	boxes, err := vision.DecodeDetect(outputs[0].Float32, channels, count, transposed, "", tensor.Scale, tensor.PadX, tensor.PadY, img.W, img.H, 0.1, 0.45, 4)
	if err != nil {
		return nil, err
	}
	used := make([]bool, len(boxes))
	var points []vision.Point
	for _, prompt := range prompts {
		sum := md5.Sum([]byte(prompt))
		direction := directionHashes[hex.EncodeToString(sum[:])]
		if direction == "" {
			return nil, fmt.Errorf("unsupported direction question")
		}
		index := -1
		for i, box := range boxes {
			if !used[i] && names[box.Label] == direction {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, fmt.Errorf("direction not detected")
		}
		used[index] = true
		box := boxes[index]
		x := (float64(box.X) + float64(box.Width)/2) / float64(img.W)
		y := (float64(box.Y) + float64(box.Height)/2) / float64(img.H)
		points = append(points, vision.Point{X: x, Y: y})
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("empty directions")
	}
	return points, nil
}

// SymbolRegionsSolve mirrors Lotus symbolRegions: blur the image (sharp blur
// 0.8), run the symbol-detect YOLOX, NMS at 0.45/limit 12 (already inside
// DecodeYoloX), then final roundEven clamp + filter + slice 12. Without the
// embedded-detect model the ensemble reduces to the YOLOX path only.
func (r *Registry) SymbolRegionsSolve(ctx context.Context, image []byte) ([]vision.SymbolBox, error) {
	if r == nil {
		return nil, fmt.Errorf("nil registry")
	}
	lease, err := r.acquire(ctx, "symbols")
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*SymbolResource)
	if !ok {
		return nil, fmt.Errorf("symbols resource has type %T", lease.Resource())
	}
	img, err := vision.RGBFromBytes(image)
	if err != nil {
		return nil, fmt.Errorf("symbol regions image: %w", err)
	}
	blurred := vision.BlurRGB(img, 0.8)
	input, err := vision.YoloXInput(blurred)
	if err != nil {
		return nil, err
	}
	ortTensor, err := ort.NewTensor(ort.Shape{1, 3, 416, 416}, input.Values)
	if err != nil {
		return nil, err
	}
	outputs, err := resource.Detect.Run(ctx, []ort.Value{ortTensor})
	if err != nil {
		return nil, err
	}
	if len(outputs) != 1 || len(outputs[0].Float32) == 0 {
		return nil, fmt.Errorf("unexpected symbol-detect output")
	}
	shape := outputs[0].Shape
	if len(shape) != 3 || shape[1] != 3549 || shape[2] < 6 {
		return nil, fmt.Errorf("invalid symbol-detect output shape %v", shape)
	}
	rows, err := vision.DecodeYoloX(outputs[0].Float32, int(shape[2]), input.Scale, img.W, img.H, 0.5)
	if err != nil {
		return nil, err
	}
	nmsed := vision.NMSBoxes(rows, 0.35)
	final := make([]vision.SymbolBox, 0, len(nmsed))
	for _, b := range nmsed {
		x := int(vision.RoundEven(float64(b.X)))
		y := int(vision.RoundEven(float64(b.Y)))
		right := int(vision.RoundEven(float64(b.X + b.Width)))
		bottom := int(vision.RoundEven(float64(b.Y + b.Height)))
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		if right > img.W {
			right = img.W
		}
		if bottom > img.H {
			bottom = img.H
		}
		b.X, b.Y = x, y
		b.Width, b.Height = right-x, bottom-y
		final = append(final, b)
	}
	return vision.SymbolRegionFilter(final, img.W, img.H), nil
}

// SymbolEmbeddingSolve mirrors Lotus SymbolEmbeddingSolver.solve: rotate each
// prompt image (0..360 step 360/rotations), embed prompts+region crops, take
// the max cosine over rotations, assign one-to-one with a >=0.3 floor, and
// return normalized region centers.
func (r *Registry) SymbolEmbeddingSolve(ctx context.Context, image []byte, prompts [][]byte, rotations int) ([]vision.Point, error) {
	if r == nil {
		return nil, fmt.Errorf("nil registry")
	}
	if rotations < 1 {
		rotations = 1
	}
	if len(prompts) == 0 || len(prompts) > 6 {
		return nil, fmt.Errorf("expected ordered symbols, got %d", len(prompts))
	}
	regions, err := r.SymbolRegionsSolve(ctx, image)
	if err != nil {
		return nil, err
	}
	if len(regions) < len(prompts) {
		return nil, fmt.Errorf("insufficient symbol detections: %d < %d", len(regions), len(prompts))
	}
	img, err := vision.RGBFromBytes(image)
	if err != nil {
		return nil, fmt.Errorf("symbol embedding image: %w", err)
	}
	var promptRasters []*vision.RGB
	for _, bytes := range prompts {
		base, err := vision.RGBFromBytes(bytes)
		if err != nil {
			return nil, fmt.Errorf("symbol prompt: %w", err)
		}
		if rotations == 1 {
			promptRasters = append(promptRasters, base)
			continue
		}
		for angle := 0; angle < 360; angle += 360 / rotations {
			if angle == 0 {
				promptRasters = append(promptRasters, base)
				continue
			}
			promptRasters = append(promptRasters, vision.RotateRGBWhite(base, float64(angle)))
		}
	}
	var cropRasters []*vision.RGB
	for _, b := range regions {
		crop, err := vision.CropBlack(img, vision.IntBox{X1: b.X, Y1: b.Y, X2: b.X + b.Width, Y2: b.Y + b.Height})
		if err != nil {
			return nil, err
		}
		cropRasters = append(cropRasters, crop)
	}
	images := append(append([]*vision.RGB(nil), promptRasters...), cropRasters...)
	lease, err := r.acquire(ctx, "symbols")
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*SymbolResource)
	if !ok {
		return nil, fmt.Errorf("symbols resource has type %T", lease.Resource())
	}
	values, err := vision.SymbolEmbeddingInput(images, 96)
	if err != nil {
		return nil, err
	}
	tensor, err := ort.NewTensor(ort.Shape{int64(len(images)), 3, 96, 96}, values)
	if err != nil {
		return nil, err
	}
	outputs, err := resource.Embedding.Run(ctx, []ort.Value{tensor})
	if err != nil {
		return nil, err
	}
	if len(outputs) != 1 || len(outputs[0].Float32) == 0 {
		return nil, fmt.Errorf("unexpected symbol-embedding output")
	}
	features := outputs[0].Float32
	stride := len(features) / len(images)
	if stride == 0 || len(features)%len(images) != 0 {
		return nil, fmt.Errorf("invalid symbol embedding shape")
	}
	featureAt := func(i int) []float32 {
		return features[i*stride : (i+1)*stride]
	}
	scores := make([][]float32, len(prompts))
	for p := range prompts {
		row := make([]float32, len(regions))
		for i := range row {
			row[i] = -1
		}
		for rr := 0; rr < rotations; rr++ {
			promptIdx := p*rotations + rr
			for c := 0; c < len(regions); c++ {
				sim := vision.CosineSimilarity32(featureAt(promptIdx), featureAt(len(promptRasters)+c))
				if sim > row[c] {
					row[c] = sim
				}
			}
		}
		scores[p] = row
	}
	columns := vision.SymbolAssignment(scores)
	if columns == nil {
		return nil, fmt.Errorf("no symbol embedding assignment")
	}
	for i, column := range columns {
		if scores[i][column] < 0.3 {
			return nil, fmt.Errorf("no symbol embedding assignment")
		}
	}
	points := make([]vision.Point, len(columns))
	for i, column := range columns {
		points[i] = vision.Point{
			X: (float64(regions[column].X) + float64(regions[column].Width)/2) / float64(img.W),
			Y: (float64(regions[column].Y) + float64(regions[column].Height)/2) / float64(img.H),
		}
	}
	return points, nil
}
