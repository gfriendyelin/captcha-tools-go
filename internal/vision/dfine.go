package vision

import (
	"fmt"
	"sort"
)

const (
	DFINEScoreThreshold = float32(0.4)
	DFINEIOUThreshold   = float32(0.01)
	DFINEPairThreshold  = float32(0.85)
)

// DFINEGroup contains the two retained detections after bottom-y sorting.
type DFINEGroup struct {
	ClassID    int
	Detections []Detection
}

// DFINEResult retains drawable groups as well as the service point sequence.
type DFINEResult struct {
	Groups []DFINEGroup
	Points []Point
}

func IoU(a, b Box) float32 {
	xA, yA := max32(a.X1, b.X1), max32(a.Y1, b.Y1)
	xB, yB := min32(a.X2, b.X2), min32(a.Y2, b.Y2)
	inter := max32(0, xB-xA) * max32(0, yB-yA)
	areaA := (a.X2 - a.X1) * (a.Y2 - a.Y1)
	areaB := (b.X2 - b.X1) * (b.Y2 - b.Y1)
	union := areaA + areaB - inter
	if union == 0 {
		return 0
	}
	return inter / union
}

// NMS performs stable score-descending, class-agnostic suppression. D-FINE
// calls it separately for each class; YOLO deliberately calls its equivalent
// once across all classes.
func NMS(detections []Detection, iouThreshold float32) []Detection {
	ordered := append([]Detection(nil), detections...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Score > ordered[j].Score })
	kept := make([]Detection, 0, len(ordered))
	for len(ordered) > 0 {
		best := ordered[0]
		kept = append(kept, best)
		rest := ordered[1:]
		next := make([]Detection, 0, len(rest))
		for _, det := range rest {
			if IoU(best.Box, det.Box) < iouThreshold {
				next = append(next, det)
			}
		}
		ordered = next
	}
	return kept
}

// PostprocessDFINE reproduces score filtering, the hard-coded top exclusion,
// per-class NMS, pair scoring, top-three classes, and upper-box center points.
func PostprocessDFINE(labels []int64, boxes []Box, scores []float32) (DFINEResult, error) {
	if len(labels) != len(boxes) || len(labels) != len(scores) {
		return DFINEResult{}, fmt.Errorf("vision: inconsistent D-FINE outputs")
	}
	byClass := make(map[int][]Detection)
	classOrder := make([]int, 0)
	for i, score := range scores {
		box := boxes[i]
		if !(score > DFINEScoreThreshold) || box.X2 > 160 && box.Y2 < 45 {
			continue
		}
		id := int(labels[i])
		if _, exists := byClass[id]; !exists {
			classOrder = append(classOrder, id)
		}
		byClass[id] = append(byClass[id], Detection{Box: box, Label: id, Score: score})
	}
	type classScore struct {
		id    int
		score float32
	}
	accepted := make([]classScore, 0, len(classOrder))
	groups := make(map[int]DFINEGroup)
	for _, id := range classOrder {
		dets := NMS(byClass[id], DFINEIOUThreshold)
		if len(dets) < 2 {
			continue
		}
		pairScore := dets[0].Score + dets[1].Score
		if pairScore < DFINEPairThreshold {
			continue
		}
		accepted = append(accepted, classScore{id: id, score: pairScore})
		groups[id] = DFINEGroup{ClassID: id, Detections: append([]Detection(nil), dets[:2]...)}
	}
	orderedIDs := make([]int, 0, len(accepted))
	if len(accepted) > 3 {
		sort.SliceStable(accepted, func(i, j int) bool { return accepted[i].score > accepted[j].score })
		accepted = accepted[:3]
	}
	for _, item := range accepted {
		orderedIDs = append(orderedIDs, item.id)
	}
	ordered := make([]DFINEGroup, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		group := groups[id]
		sort.SliceStable(group.Detections, func(i, j int) bool {
			return group.Detections[i].Box.Y2 > group.Detections[j].Box.Y2
		})
		ordered = append(ordered, group)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Detections[0].Box.X1 < ordered[j].Detections[0].Box.X1
	})
	result := DFINEResult{Groups: ordered, Points: make([]Point, 0, len(ordered))}
	for _, group := range ordered {
		box := group.Detections[len(group.Detections)-1].Box
		result.Points = append(result.Points, Point{X: float64((box.X1 + box.X2) / 2), Y: float64((box.Y1 + box.Y2) / 2)})
	}
	return result, nil
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
