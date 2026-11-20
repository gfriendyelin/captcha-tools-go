package inference

import (
	"context"
	"fmt"

	"github.com/kafuneri/captcha-tools-go/internal/server"
	"github.com/kafuneri/captcha-tools-go/internal/vision"
	ort "github.com/yalue/onnxruntime_go"
)

func (r *Registry) Nine(ctx context.Context, data []byte, useV3 bool, point *float64) (server.NineResult, error) {
	var empty server.NineResult
	lease, err := r.acquire(ctx, "pdl")
	if err != nil {
		return empty, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*PDLResource)
	if !ok {
		return empty, fmt.Errorf("pdl resource has type %T", lease.Resource())
	}
	image, err := vision.RGBFromBytes(data)
	if err != nil {
		return empty, err
	}
	crops, err := vision.CropV3(image)
	if err != nil {
		return empty, err
	}
	result := server.NineResult{}
	roundTripped := make([]*vision.RGB, 10)
	for i, cell := range crops.Cells {
		encoded, encodeErr := vision.EncodeJPEG(cell)
		if encodeErr != nil {
			return empty, encodeErr
		}
		result.Artifacts = append(result.Artifacts, server.Artifact{Name: fmt.Sprintf("cropped_%d.jpg", i), Data: encoded})
		decoded, decodeErr := vision.RGBFromBytes(encoded)
		if decodeErr != nil {
			return empty, decodeErr
		}
		roundTripped[i] = decoded
	}
	var input vision.Tensor
	if useV3 {
		input, err = vision.PreprocessPDL(roundTripped)
	} else {
		nineData, encodeErr := vision.EncodeJPEG(crops.Nine)
		if encodeErr != nil {
			return empty, encodeErr
		}
		nine, decodeErr := vision.RGBFromBytes(nineData)
		if decodeErr != nil {
			return empty, decodeErr
		}
		cells, cropErr := vision.CropGrid(nine)
		if cropErr != nil {
			return empty, cropErr
		}
		images := []*vision.RGB{roundTripped[9]}
		for _, cell := range cells {
			images = append(images, cell)
		}
		input, err = vision.PreprocessLegacy(images)
	}
	if err != nil {
		return empty, err
	}
	tensor, err := ort.NewTensor(shape4(input.Shape), input.Data)
	if err != nil {
		return empty, err
	}
	outputs, err := resource.Session.Run(ctx, []ort.Value{tensor})
	if err != nil {
		return empty, err
	}
	if len(outputs) != 1 || len(outputs[0].Shape) != 2 {
		return empty, fmt.Errorf("unexpected PDL output shape")
	}
	batch, classes := int(outputs[0].Shape[0]), int(outputs[0].Shape[1])
	if useV3 {
		cells, postErr := vision.PDLCoordinates(outputs[0].Float32, batch, classes)
		if postErr != nil {
			return empty, postErr
		}
		for _, cell := range cells {
			result.Cells = append(result.Cells, [2]int{cell.Row, cell.Col})
		}
	} else {
		var threshold *float32
		if point != nil {
			converted := float32(*point)
			threshold = &converted
		}
		cells, postErr := vision.LegacyCoordinates(outputs[0].Float32, classes, threshold)
		if postErr != nil {
			return empty, postErr
		}
		for _, cell := range cells {
			result.Cells = append(result.Cells, [2]int{cell.Row, cell.Col})
		}
	}
	return result, nil
}

func (r *Registry) DFINE(ctx context.Context, data []byte, draw bool) (server.IconResult, error) {
	var empty server.IconResult
	lease, err := r.acquire(ctx, "dfine")
	if err != nil {
		return empty, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*DFINEResource)
	if !ok {
		return empty, fmt.Errorf("dfine resource has type %T", lease.Resource())
	}
	image, err := vision.RGBFromBytes(data)
	if err != nil {
		return empty, err
	}
	input, err := vision.PreprocessDFINE(image)
	if err != nil {
		return empty, err
	}
	images, err := ort.NewTensor(shape4(input.Images.Shape), input.Images.Data)
	if err != nil {
		return empty, err
	}
	sizes, err := ort.NewTensor(ort.NewShape(1, 2), []int64{input.OrigTargetSizes[0], input.OrigTargetSizes[1]})
	if err != nil {
		_ = images.Destroy()
		return empty, err
	}
	outputs, err := resource.Session.Run(ctx, []ort.Value{images, sizes})
	if err != nil {
		return empty, err
	}
	if len(outputs) != 3 {
		return empty, fmt.Errorf("D-FINE returned %d outputs", len(outputs))
	}
	labels, boxesRaw, scores := outputs[0].Int64, outputs[1].Float32, outputs[2].Float32
	if len(boxesRaw)%4 != 0 {
		return empty, fmt.Errorf("invalid D-FINE boxes length %d", len(boxesRaw))
	}
	boxes := make([]vision.Box, len(boxesRaw)/4)
	for i := range boxes {
		boxes[i] = vision.Box{X1: boxesRaw[4*i], Y1: boxesRaw[4*i+1], X2: boxesRaw[4*i+2], Y2: boxesRaw[4*i+3]}
	}
	processed, err := vision.PostprocessDFINE(labels, boxes, scores)
	if err != nil {
		return empty, err
	}
	result := server.IconResult{Points: convertPoints(processed.Points)}
	if draw {
		drawn, drawErr := vision.DrawDFINE(image, processed)
		if drawErr != nil {
			return empty, drawErr
		}
		encoded, encodeErr := vision.EncodeJPEG(drawn)
		if encodeErr != nil {
			return empty, encodeErr
		}
		result.Artifacts = []server.Artifact{{Name: "icon_result.jpg", Data: encoded}}
	}
	return result, nil
}

func (r *Registry) Multi(ctx context.Context, data []byte, draw bool) (server.IconResult, error) {
	var empty server.IconResult
	lease, err := r.acquire(ctx, "multi")
	if err != nil {
		return empty, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*MultiResource)
	if !ok {
		return empty, fmt.Errorf("multi resource has type %T", lease.Resource())
	}
	image, err := vision.RGBFromBytes(data)
	if err != nil {
		return empty, err
	}
	yoloInput, err := vision.PreprocessYOLO(image)
	if err != nil {
		return empty, err
	}
	yoloTensor, err := ort.NewTensor(shape4(yoloInput.Shape), yoloInput.Data)
	if err != nil {
		return empty, err
	}
	yoloOutputs, err := resource.YOLO.Run(ctx, []ort.Value{yoloTensor})
	if err != nil {
		return empty, err
	}
	if len(yoloOutputs) != 1 || len(yoloOutputs[0].Shape) != 3 || yoloOutputs[0].Shape[0] != 1 || yoloOutputs[0].Shape[1] != 6 {
		return empty, fmt.Errorf("unexpected YOLO output shape %v", yoloOutputs[0].Shape)
	}
	yolo, err := vision.PostprocessYOLO(yoloOutputs[0].Float32, int(yoloOutputs[0].Shape[2]), image)
	if err != nil {
		return empty, err
	}
	top, err := multiFeatures(ctx, resource, yolo.Top)
	if err != nil {
		return empty, err
	}
	bottom, err := multiFeatures(ctx, resource, yolo.Bottom)
	if err != nil {
		return empty, err
	}
	sequence, err := vision.GreedyMatch(bottom, top, func(a, b []float32) float32 { return vision.CosineSimilarity(a, b) })
	if err != nil {
		return empty, err
	}
	points := vision.MatchedPoints(sequence, r.randomOffset, nil)
	result := server.IconResult{Points: convertPoints(points), YOLOTop: convertBoxes(yolo.Top), YOLOBottom: convertBoxes(yolo.Bottom)}
	for _, box := range sequence {
		result.Sequence = append(result.Sequence, [4]int{box.X1, box.Y1, box.X2, box.Y2})
	}
	if draw {
		drawn, drawErr := vision.DrawMulti(image, sequence)
		if drawErr != nil {
			return empty, drawErr
		}
		encoded, encodeErr := vision.EncodeJPEG(drawn)
		if encodeErr != nil {
			return empty, encodeErr
		}
		result.Artifacts = []server.Artifact{{Name: "icon_result.jpg", Data: encoded}}
	}
	return result, nil
}

func multiFeatures(ctx context.Context, resource *MultiResource, crops []vision.YOLOCrop) ([]vision.MatchCandidate, error) {
	result := make([]vision.MatchCandidate, 0, len(crops))
	for _, crop := range crops {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		input, err := vision.PreprocessDINO(crop.Image)
		if err != nil {
			return nil, err
		}
		tensor, err := ort.NewTensor(shape4(input.Shape), input.Data)
		if err != nil {
			return nil, err
		}
		dino, err := resource.DINO.Run(ctx, []ort.Value{tensor})
		if err != nil {
			return nil, err
		}
		if len(dino) != 1 || len(dino[0].Shape) != 3 || dino[0].Shape[0] != 1 {
			return nil, fmt.Errorf("unexpected DINO output shape")
		}
		patches, err := vision.DINOPatchTokens(dino[0].Float32, int(dino[0].Shape[1]), int(dino[0].Shape[2]))
		if err != nil {
			return nil, err
		}
		patchTensor, err := ort.NewTensor(ort.NewShape(1, int64(dino[0].Shape[1]-5), dino[0].Shape[2]), patches)
		if err != nil {
			return nil, err
		}
		embedding, err := resource.Atten.Run(ctx, []ort.Value{patchTensor})
		if err != nil {
			return nil, err
		}
		if len(embedding) != 1 || len(embedding[0].Float32) == 0 {
			return nil, fmt.Errorf("unexpected atten output")
		}
		result = append(result, vision.MatchCandidate{BBox: crop.BBox, Feature: embedding[0].Float32})
	}
	return result, nil
}

func shape4(shape [4]int) ort.Shape {
	return ort.Shape{int64(shape[0]), int64(shape[1]), int64(shape[2]), int64(shape[3])}
}
func convertBoxes(crops []vision.YOLOCrop) [][4]int {
	result := make([][4]int, len(crops))
	for i, crop := range crops {
		result[i] = [4]int{crop.BBox.X1, crop.BBox.Y1, crop.BBox.X2, crop.BBox.Y2}
	}
	return result
}

func convertPoints(points []vision.Point) [][2]float64 {
	result := make([][2]float64, len(points))
	for i, point := range points {
		result[i] = [2]float64{point.X, point.Y}
	}
	return result
}
