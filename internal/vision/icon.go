package vision

import (
	"fmt"
	"math"
	"sort"
)

// SymbolBox is one detected symbol region in image coordinates.
type SymbolBox struct {
	X, Y, Width, Height int
	Score               float32
	Label               int
}

// Center returns the normalized center point [0,1] relative to image size.
func (b SymbolBox) Center(imageWidth, imageHeight int) (x, y float64) {
	if imageWidth <= 0 || imageHeight <= 0 {
		return 0, 0
	}
	return (float64(b.X) + float64(b.Width)/2) / float64(imageWidth),
		(float64(b.Y) + float64(b.Height)/2) / float64(imageHeight)
}

// SymbolEmbeddingInput reproduces Lotus symbolEmbeddingInput: resize each
// raster to 96x96 linear, then RGB /255 (NOT ImageNet normalized, and NOT the
// detector's BGR ordering) in NCHW.
func SymbolEmbeddingInput(images []*RGB, size int) ([]float32, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("vision: empty symbol embedding batch")
	}
	if size <= 0 {
		return nil, fmt.Errorf("vision: invalid embedding size %d", size)
	}
	pixels := size * size
	values := make([]float32, len(images)*3*pixels)
	for n, im := range images {
		if im == nil || im.W <= 0 || im.H <= 0 {
			return nil, fmt.Errorf("vision: invalid embedding image %d", n)
		}
		resized, err := Resize(im, size, size, Bilinear)
		if err != nil {
			return nil, err
		}
		for c := 0; c < 3; c++ {
			for i := 0; i < pixels; i++ {
				values[n*3*pixels+c*pixels+i] = float32(resized.Pix[i*3+c]) / 255.0
			}
		}
	}
	return values, nil
}

// CosineSimilarity32 is the cosine used by the Lotus numeric module: float64
// accumulation over float32 inputs. Returns NaN for empty/mismatched inputs.
func CosineSimilarity32(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return float32(math.NaN())
	}
	var dot, aa, bb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		aa += float64(a[i]) * float64(a[i])
		bb += float64(b[i]) * float64(b[i])
	}
	den := math.Sqrt(aa * bb)
	if den == 0 {
		return float32(math.NaN())
	}
	return float32(dot / den)
}

// SymbolAssignment solves the maximum-score bipartite assignment between prompts
// (rows) and regions (columns) by bitmask DP. It mirrors Lotus symbolAssignment:
// each prompt picks a distinct region. Returns nil for empty/oversized/non-finite.
func SymbolAssignment(scores [][]float32) []int {
	if len(scores) == 0 || len(scores[0]) == 0 || len(scores) > len(scores[0]) || len(scores[0]) > 12 {
		return nil
	}
	cols := len(scores[0])
	for _, row := range scores {
		if len(row) != cols {
			return nil
		}
		for _, v := range row {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return nil
			}
		}
	}
	type state struct {
		score   float32
		columns []int
	}
	states := map[int]state{0: {score: 0, columns: []int{}}}
	for _, row := range scores {
		next := map[int]state{}
		for mask, st := range states {
			for column := 0; column < cols; column++ {
				if mask&(1<<column) != 0 {
					continue
				}
				key := mask | (1 << column)
				score := st.score + row[column]
				existing, ok := next[key]
				if !ok || existing.score < score {
					colsCopy := append([]int(nil), st.columns...)
					next[key] = state{score: score, columns: append(colsCopy, column)}
				}
			}
		}
		states = next
	}
	best := state{score: float32(math.Inf(-1))}
	for _, st := range states {
		if st.score > best.score {
			best = st
		}
	}
	if len(best.columns) != len(scores) {
		return nil
	}
	return best.columns
}

// RoundEven mirrors Lotus roundEven (banker's rounding used on box bounds).
func RoundEven(v float64) float64 {
	f := math.Floor(v)
	if v-f == 0.5 {
		if math.Mod(f, 2) == 0 {
			return f
		}
		return f + 1
	}
	return math.Round(v)
}

// SymbolRegionFilter applies the Lotus region acceptance filter and the
// 12-region cap. Boxes must already be clamped to the image.
func SymbolRegionFilter(boxes []SymbolBox, imageWidth, imageHeight int) []SymbolBox {
	imageArea := imageWidth * imageHeight
	out := make([]SymbolBox, 0, len(boxes))
	for _, b := range boxes {
		if b.Width < 12 || b.Height < 12 {
			continue
		}
		if b.Width*b.Height >= imageArea*6/10 {
			continue
		}
		out = append(out, b)
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// NMSBoxes performs the Lotus nms (score-descending, class-agnostic IOU
// threshold) and returns surviving boxes.
func NMSBoxes(boxes []SymbolBox, iou float32) []SymbolBox {
	ordered := append([]SymbolBox(nil), boxes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Score > ordered[j].Score })
	kept := make([]SymbolBox, 0, len(ordered))
	for len(ordered) > 0 {
		best := ordered[0]
		kept = append(kept, best)
		rest := ordered[1:]
		next := make([]SymbolBox, 0, len(rest))
		for _, det := range rest {
			if IoUBox(best, det) < iou {
				next = append(next, det)
			}
		}
		ordered = next
	}
	return kept
}

// IoUBox computes intersection-over-union between two symbol boxes.
func IoUBox(a, b SymbolBox) float32 {
	xa, ya := maxI(a.X, b.X), maxI(a.Y, b.Y)
	xb, yb := minI(a.X+a.Width, b.X+b.Width), minI(a.Y+a.Height, b.Y+b.Height)
	inter := maxI(0, xb-xa) * maxI(0, yb-ya)
	areaA := a.Width * a.Height
	areaB := b.Width * b.Height
	union := areaA + areaB - inter
	if union == 0 {
		return 0
	}
	return float32(inter) / float32(union)
}

func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
