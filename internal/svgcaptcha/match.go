package svgcaptcha

import (
	"fmt"
	"math"
	"sort"
)

const (
	confidentScore  = 0.35
	confidentMargin = 0.06
)

// Result describes the winning grid cell. Row and Col are 1-based, matching the
// wire format, and Layer is the 0-based frame the cell lives in.
type Result struct {
	Layer   int
	Row     int
	Col     int
	Cols    int
	StartMS int
	EndMS   int
	Scores  []float64
}

// Solve locates the hint inside the puzzle: it renders every grid cell, scores
// the edge overlap with the hint and maps the best cell back to frame and
// row/column.
func Solve(source string, hint []byte) (Result, error) {
	grids, err := extractGrids(source)
	if err != nil {
		return Result{}, err
	}
	if len(grids) == 0 || len(grids)%3 != 0 {
		return Result{}, fmt.Errorf("expected three frames of grids, got %d", len(grids))
	}
	cols, err := GridCols(source)
	if err != nil {
		return Result{}, err
	}
	scores, err := matchGrids(source, grids, hint)
	if err != nil {
		return Result{}, err
	}
	best := argmax(scores)
	cellsPerFrame := len(grids) / 3
	result := Result{
		Layer:  best / cellsPerFrame,
		Row:    (best%cellsPerFrame)/cols + 1,
		Col:    (best%cellsPerFrame)%cols + 1,
		Cols:   cols,
		Scores: scores,
	}
	if times, err := FrameTimes(source); err == nil && result.Layer < len(times) {
		result.StartMS, result.EndMS = times[result.Layer][0], times[result.Layer][1]
	}
	return result, nil
}

func matchGrids(source string, grids []grid, hint []byte) ([]float64, error) {
	hintGray, err := decodeHint(hint)
	if err != nil {
		return nil, err
	}
	base := canny(bilateralFilter(medianBlur3(hintGray), 2, 50), 50, 150)

	angles, scales := layoutParams(source, len(grids)/3)
	edges := make([]*grayImage, len(grids))
	for index, item := range grids {
		rendered, err := renderSVG(item.svg, item.width, item.height)
		if err != nil {
			return nil, err
		}
		edges[index] = canny(rendered, 50, 150)
	}

	best := make([]float64, len(grids))
	for index := range best {
		best[index] = -1
	}
	combos := len(angles) * len(scales)
	index := 0
combos:
	for _, angle := range angles {
		for _, scale := range scales {
			template := newTemplate(resizeEdge(rotate(base, angle), scale))
			for gridIndex, edge := range edges {
				if template.g.w > edge.w || template.g.h > edge.h {
					continue
				}
				score, _, _ := template.match(edge)
				if score > best[gridIndex] {
					best[gridIndex] = score
				}
			}
			// One confident, clearly-leading candidate ends the search early.
			if index == 0 && combos > 1 && len(best) > 1 {
				sorted := append([]float64(nil), best...)
				sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
				if sorted[0] >= confidentScore && sorted[0]-sorted[1] >= confidentMargin {
					break combos
				}
			}
			index++
		}
	}
	any := false
	for _, score := range best {
		if score >= 0 {
			any = true
			break
		}
	}
	if !any {
		return nil, fmt.Errorf("hint is larger than every grid at all scales")
	}
	return best, nil
}

func layoutParams(source string, cellsPerFrame int) ([]float64, []float64) {
	if seedPattern.MatchString(source) {
		return []float64{12, 14, 16}, []float64{1.3, 1.4, 1.5}
	}
	if cellsPerFrame == 4 {
		return []float64{0}, []float64{0.5}
	}
	return []float64{0}, []float64{1.0}
}

func argmax(values []float64) int {
	best := 0
	for index, value := range values {
		if value > values[best] {
			best = index
		}
	}
	return best
}

// template scores an image with normalized cross correlation
// (TM_CCOEFF_NORMED in OpenCV).
type template struct {
	g    *grayImage
	mean float64
	norm float64
}

func newTemplate(g *grayImage) *template {
	count := float64(g.w * g.h)
	var sum, square float64
	for _, value := range g.pix {
		sum += value
		square += value * value
	}
	mean := sum / count
	norm := square - count*mean*mean
	if norm < 0 {
		norm = 0
	}
	return &template{g: g, mean: mean, norm: norm}
}

func (t *template) match(image *grayImage) (best float64, bestX, bestY int) {
	best = -2
	width := image.w + 1
	sums := make([]float64, (image.w+1)*(image.h+1))
	squares := make([]float64, (image.w+1)*(image.h+1))
	for y := 0; y < image.h; y++ {
		var rowSum, rowSquare float64
		for x := 0; x < image.w; x++ {
			value := image.pix[y*image.w+x]
			rowSum += value
			rowSquare += value * value
			sums[(y+1)*width+x+1] = sums[y*width+x+1] + rowSum
			squares[(y+1)*width+x+1] = squares[y*width+x+1] + rowSquare
		}
	}

	count := float64(t.g.w * t.g.h)
	for offsetY := 0; offsetY <= image.h-t.g.h; offsetY++ {
		for offsetX := 0; offsetX <= image.w-t.g.w; offsetX++ {
			var cross float64
			for ty := 0; ty < t.g.h; ty++ {
				imageRow := image.pix[(offsetY+ty)*image.w+offsetX:]
				templateRow := t.g.pix[ty*t.g.w:]
				for tx := 0; tx < t.g.w; tx++ {
					cross += imageRow[tx] * templateRow[tx]
				}
			}
			sum := rectangleSum(sums, width, offsetX, offsetY, t.g.w, t.g.h)
			square := rectangleSum(squares, width, offsetX, offsetY, t.g.w, t.g.h)
			mean := sum / count
			variance := square - count*mean*mean
			if variance <= 1e-9 || t.norm <= 1e-9 {
				continue
			}
			score := (cross - count*t.mean*mean) / math.Sqrt(variance*t.norm)
			if score > best {
				best, bestX, bestY = score, offsetX, offsetY
			}
		}
	}
	return best, bestX, bestY
}

func rectangleSum(integral []float64, width, x, y, w, h int) float64 {
	return integral[(y+h)*width+(x+w)] - integral[y*width+(x+w)] - integral[(y+h)*width+x] + integral[y*width+x]
}
