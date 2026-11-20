package vision

import (
	"fmt"
	"math"
	"sort"
)

var gridCoordinates = [9]GridPoint{
	{1, 1}, {1, 2}, {1, 3},
	{2, 1}, {2, 2}, {2, 3},
	{3, 1}, {3, 2}, {3, 3},
}

func GridCoordinates() [9]GridPoint { return gridCoordinates }

// ArgMax returns the first maximum index, or -1 for an empty slice.
func ArgMax(values []float32) int {
	if len(values) == 0 {
		return -1
	}
	best := 0
	for i := 1; i < len(values); i++ {
		if values[i] > values[best] || math.IsNaN(float64(values[i])) && !math.IsNaN(float64(values[best])) {
			best = i
		}
	}
	return best
}

// CosineSimilarity uses float32 accumulation to stay close to NumPy's float32
// dot/norm path. Mismatched lengths and zero norms produce NaN.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return float32(math.NaN())
	}
	var dot, aa, bb float32
	for i := range a {
		dot += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	den := float32(math.Sqrt(float64(aa))) * float32(math.Sqrt(float64(bb)))
	return dot / den
}

// PDLCoordinates applies top-1 target matching to a [batch,classes] output.
// Batch must contain nine cells followed by the target. If there is no match,
// the original fallback compares the second-lowest class index (argsort[1]),
// not the second-highest score.
func PDLCoordinates(outputs []float32, batch, classes int) ([]GridPoint, error) {
	if batch < 10 || classes <= 0 || len(outputs) != batch*classes {
		return nil, fmt.Errorf("vision: invalid PDL output batch=%d classes=%d len=%d", batch, classes, len(outputs))
	}
	labels := make([]int, batch)
	for i := 0; i < batch; i++ {
		labels[i] = ArgMax(outputs[i*classes : (i+1)*classes])
	}
	target := labels[batch-1]
	answer := make([]GridPoint, 0, 9)
	for i := 0; i < 9; i++ {
		if labels[i] == target {
			answer = append(answer, gridCoordinates[i])
		}
	}
	if len(answer) != 0 {
		return answer, nil
	}
	row := make([]float32, classes)
	for i := 0; i < 9; i++ {
		copy(row, outputs[i*classes:(i+1)*classes])
		indices := make([]int, classes)
		for j := range indices {
			indices[j] = j
		}
		sort.Slice(indices, func(a, b int) bool { return row[indices[a]] < row[indices[b]] })
		if classes > 1 && indices[1] == target {
			answer = append(answer, gridCoordinates[i])
		}
	}
	return answer, nil
}

// GridScore retains the original zero-based cell index and similarity.
type GridScore struct {
	Point GridPoint
	Index int
	Score float32
}

// LegacyScores compares cells 1..9 with target row 0 and returns stable,
// descending score order without applying a top-K or threshold.
func LegacyScores(outputs []float32, classes int) ([]GridScore, error) {
	if classes <= 0 || len(outputs) != 10*classes {
		return nil, fmt.Errorf("vision: legacy output must be [10,C], classes=%d len=%d", classes, len(outputs))
	}
	target := outputs[:classes]
	scores := make([]GridScore, 9)
	for i := 0; i < 9; i++ {
		scores[i] = GridScore{Point: gridCoordinates[i], Index: i, Score: CosineSimilarity(target, outputs[(i+1)*classes:(i+2)*classes])}
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].Score > scores[j].Score })
	return scores, nil
}

// LegacyCoordinates returns the highest three similarities when point is nil;
// otherwise it retains scores strictly greater than *point.
func LegacyCoordinates(outputs []float32, classes int, point *float32) ([]GridPoint, error) {
	scores, err := LegacyScores(outputs, classes)
	if err != nil {
		return nil, err
	}
	limit := len(scores)
	if point == nil && limit > 3 {
		limit = 3
	}
	answer := make([]GridPoint, 0, limit)
	for i := 0; i < limit; i++ {
		if point != nil && !(scores[i].Score > *point) {
			continue
		}
		answer = append(answer, scores[i].Point)
	}
	return answer, nil
}
