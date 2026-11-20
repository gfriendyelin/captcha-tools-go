package vision

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

const (
	YOLOConfidenceThreshold = float32(0.5)
	YOLOIOUThreshold        = float32(0.55)
	YOLOInputSize           = float32(384)
)

var ErrNoYOLODetections = errors.New("vision: no YOLO detections above threshold")

type YOLOCrop struct {
	Image      *RGB
	BBox       IntBox
	ClassID    int
	Confidence float32
	Box        Box
}

type YOLOResult struct {
	Top        []YOLOCrop
	Bottom     []YOLOCrop
	Detections []Detection
}

// PostprocessYOLO consumes the current model's batch-one [1,6,anchors] output,
// rescales without letterbox, performs cross-class NMS, truncates coordinates
// toward zero, and crops with black out-of-bounds padding.
func PostprocessYOLO(output []float32, anchors int, image *RGB) (YOLOResult, error) {
	var result YOLOResult
	if image == nil || anchors <= 0 || len(output) != 6*anchors {
		return result, fmt.Errorf("vision: invalid YOLO input anchors=%d len=%d", anchors, len(output))
	}
	detections := make([]Detection, 0, anchors)
	for a := 0; a < anchors; a++ {
		s0, s1 := output[4*anchors+a], output[5*anchors+a]
		label, confidence := 0, s0
		if s1 > s0 {
			label, confidence = 1, s1
		}
		if !(confidence > YOLOConfidenceThreshold) {
			continue
		}
		cx := output[a] / YOLOInputSize * float32(image.W)
		cy := output[anchors+a] / YOLOInputSize * float32(image.H)
		w := output[2*anchors+a] / YOLOInputSize * float32(image.W)
		h := output[3*anchors+a] / YOLOInputSize * float32(image.H)
		detections = append(detections, Detection{
			Box:   Box{X1: cx - w/2, Y1: cy - h/2, X2: cx + w/2, Y2: cy + h/2},
			Label: label, Score: confidence,
		})
	}
	if len(detections) == 0 {
		return result, ErrNoYOLODetections
	}
	result.Detections = yoloNMS(detections, YOLOIOUThreshold)
	for _, det := range result.Detections {
		r := IntBox{X1: int(det.Box.X1), Y1: int(det.Box.Y1), X2: int(det.Box.X2), Y2: int(det.Box.Y2)}
		crop, err := CropBlack(image, r)
		if err != nil {
			return YOLOResult{}, err
		}
		item := YOLOCrop{Image: crop, BBox: r, ClassID: det.Label, Confidence: det.Score, Box: det.Box}
		if det.Label == 0 {
			result.Top = append(result.Top, item)
		} else {
			result.Bottom = append(result.Bottom, item)
		}
	}
	return result, nil
}

func yoloNMS(detections []Detection, threshold float32) []Detection {
	ordered := append([]Detection(nil), detections...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Score > ordered[j].Score })
	kept := make([]Detection, 0, len(ordered))
	for len(ordered) > 0 {
		best := ordered[0]
		kept = append(kept, best)
		rest := ordered[1:]
		next := make([]Detection, 0, len(rest))
		for _, det := range rest {
			iou := yoloIOU(best.Box, det.Box)
			if iou < threshold { // NaN is suppressed, as in NumPy.
				next = append(next, det)
			}
		}
		ordered = next
	}
	return kept
}

func yoloIOU(a, b Box) float32 {
	x1, y1 := max32(a.X1, b.X1), max32(a.Y1, b.Y1)
	x2, y2 := min32(a.X2, b.X2), min32(a.Y2, b.Y2)
	inter := max32(0, x2-x1) * max32(0, y2-y1)
	areaA := (a.X2 - a.X1) * (a.Y2 - a.Y1)
	areaB := (b.X2 - b.X1) * (b.Y2 - b.Y1)
	union := areaA + areaB - inter
	if union == 0 {
		return float32(math.NaN())
	}
	return inter / union
}
