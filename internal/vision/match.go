package vision

import (
	"fmt"
	"math/rand"
	"sort"
	"time"
)

// MatchCandidate carries an ONNX-derived feature/embedding with its YOLO box.
type MatchCandidate struct {
	BBox    IntBox
	Feature []float32
}

// GreedyMatch sorts bottom boxes by x1, then matches each to one unused top.
// Similarity is evaluated on demand; a single available top needs no callback.
// Ties keep the first candidate, matching Python max.
func GreedyMatch(bottom, top []MatchCandidate, similarity func(bottom, top []float32) float32) ([]IntBox, error) {
	anchors := append([]MatchCandidate(nil), bottom...)
	sort.SliceStable(anchors, func(i, j int) bool { return anchors[i].BBox.X1 < anchors[j].BBox.X1 })
	used := make([]bool, len(top))
	sequence := make([]IntBox, 0, len(anchors))
	for _, anchor := range anchors {
		available := make([]int, 0, len(top))
		for i := range top {
			if !used[i] {
				available = append(available, i)
			}
		}
		if len(available) == 0 {
			break
		}
		best := available[0]
		if len(available) > 1 {
			if similarity == nil {
				return nil, fmt.Errorf("vision: nil DINO similarity callback")
			}
			bestScore := similarity(anchor.Feature, top[best].Feature)
			for _, index := range available[1:] {
				score := similarity(anchor.Feature, top[index].Feature)
				if score > bestScore {
					best, bestScore = index, score
				}
			}
		}
		sequence = append(sequence, top[best].BBox)
		used[best] = true
	}
	return sequence, nil
}

// MatchedPoints converts boxes to centers and optionally applies the original
// inclusive +/-20%% integer jitter. Passing false gives deterministic centers.
// A supplied rng makes jitter tests/replays deterministic; nil seeds a new RNG.
func MatchedPoints(sequence []IntBox, randomOffset bool, rng *rand.Rand) []Point {
	if randomOffset && rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	points := make([]Point, len(sequence))
	for i, box := range sequence {
		cx := float64(box.X1+box.X2) / 2
		cy := float64(box.Y1+box.Y2) / 2
		if randomOffset {
			w, h := absInt(box.X1-box.X2), absInt(box.Y1-box.Y2)
			minX, maxX := -w/5, w/5
			minY, maxY := -h/5, h/5
			cx += float64(minX + rng.Intn(maxX-minX+1))
			cy += float64(minY + rng.Intn(maxY-minY+1))
		}
		points[i] = Point{X: cx, Y: cy}
	}
	return points
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
