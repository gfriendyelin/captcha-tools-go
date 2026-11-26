package vision

import (
	"fmt"
	"math"
	"sort"
)

// GridCellIndex identifies one cell by its zero-based index in the nine-grid.
type GridCellIndex struct {
	Index int
	Score float32
}

// GridInput is a batch of RGB images prepared for the SigLIP-style vision
// backbone. The Lotus pipeline feeds [prompt, ...cells] in that order, resized
// to 256x256 with a linear filter and ImageNet-normalized in NCHW layout.
type GridInput struct {
	Images []*RGB
	Size   int
	Values []float32
}

// GridNormalizeResize reproduces the Lotus GridMatcher preprocess: flatten to
// white, sRGB, linear resize to 256x256, float32 /255 then ImageNet
// normalization, stored in NCHW order.
func GridNormalizeResize(images []*RGB, size int) (GridInput, error) {
	var out GridInput
	if len(images) == 0 {
		return out, fmt.Errorf("vision: empty grid image batch")
	}
	if size <= 0 {
		return out, fmt.Errorf("vision: invalid grid size %d", size)
	}
	out.Images = images
	out.Size = size
	pixels := size * size
	out.Values = make([]float32, len(images)*3*pixels)
	for n, im := range images {
		if im == nil || im.W <= 0 || im.H <= 0 {
			return GridInput{}, fmt.Errorf("vision: invalid grid image %d", n)
		}
		resized, err := Resize(im, size, size, Bilinear)
		if err != nil {
			return GridInput{}, err
		}
		for c := 0; c < 3; c++ {
			for i := 0; i < pixels; i++ {
				v := float32(resized.Pix[i*3+c]) / 255.0
				out.Values[n*3*pixels+c*pixels+i] = (v - ImageNetMean[c]) / ImageNetStd[c]
			}
		}
	}
	return out, nil
}

// GridMatcher implements the Lotus GridMatcher: it runs the SigLIP-style
// vision backbone over [prompt, ...cells], repeats the prompt features once
// per cell, and feeds prompt+cell features through the match head. The head
// returns one logit per cell; the caller applies its own selection policy.
type GridMatcher struct {
	Size int
}

// NewGridMatcher returns a matcher with the fixed 256px input size used by the
// Lotus grid-vision / grid-head pair.
func NewGridMatcher() *GridMatcher { return &GridMatcher{Size: 256} }

// Scores validates the backbone batch output and lays out the repeated prompt
// features exactly as Lotus does: prompt_features has shape [cells,576,8,8]
// with the prompt feature repeated, cell_features has shape [cells,576,8,8]
// from the cell slice. The caller feeds both to the head and returns logits.
func (m *GridMatcher) Scores(features []float32, batch, stride int, cells int) (promptFeatures, cellFeatures []float32, err error) {
	if m == nil || m.Size <= 0 {
		return nil, nil, fmt.Errorf("vision: uninitialized grid matcher")
	}
	if batch < 1 || stride <= 0 || cells < 1 || cells > batch-1 || len(features) != batch*stride {
		return nil, nil, fmt.Errorf("vision: invalid grid features batch=%d stride=%d cells=%d len=%d", batch, stride, cells, len(features))
	}
	promptFeatures = make([]float32, cells*stride)
	for i := 0; i < cells; i++ {
		copy(promptFeatures[i*stride:(i+1)*stride], features[:stride])
	}
	cellFeatures = make([]float32, cells*stride)
	copy(cellFeatures, features[stride:(cells+1)*stride])
	return promptFeatures, cellFeatures, nil
}

// SelectTopK reproduces the Lotus selection: sort logits descending, take the
// first count, then re-sort ascending by index (stable ascending index order).
func SelectTopK(logits []float32, count int) ([]int, error) {
	if count < 1 || count > len(logits) {
		return nil, fmt.Errorf("vision: invalid grid count %d for %d cells", count, len(logits))
	}
	type scored struct {
		index int
		value float32
	}
	rows := make([]scored, len(logits))
	for i, v := range logits {
		rows[i] = scored{index: i, value: v}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].value > rows[j].value })
	top := rows[:count]
	sort.SliceStable(top, func(i, j int) bool { return top[i].index < top[j].index })
	indices := make([]int, count)
	for i, row := range top {
		indices[i] = row.index
	}
	return indices, nil
}

// GridLabels applies row-wise argmax over [batch, classes] and returns the
// winning class per row. This is the Lotus GridClassifier.classify() output
// used for both the baseline and multi-view alternatives.
func GridLabels(output []float32, batch, classes int) ([]int, error) {
	if batch < 1 || classes < 1 || len(output) != batch*classes {
		return nil, fmt.Errorf("vision: invalid classifier output batch=%d classes=%d len=%d", batch, classes, len(output))
	}
	labels := make([]int, batch)
	for row := 0; row < batch; row++ {
		base := row * classes
		best := 0
		for i := 1; i < classes; i++ {
			if output[base+i] > output[base+best] {
				best = i
			}
		}
		labels[row] = best
	}
	return labels, nil
}

// ClassifierIndices reproduces GridClassifier.predict: the target is the last
// row's label; matching earlier rows are returned in ascending index order.
func ClassifierIndices(labels []int) ([]int, error) {
	if len(labels) < 2 {
		return nil, fmt.Errorf("vision: classifier needs at least one cell and one target, got %d", len(labels))
	}
	target := labels[len(labels)-1]
	indices := make([]int, 0, 9)
	for i := 0; i < len(labels)-1; i++ {
		if labels[i] == target {
			indices = append(indices, i)
		}
	}
	return indices, nil
}

// AgreeNonempty mirrors Lotus agreeNonempty: views must all be non-empty and
// identical, otherwise nil is returned.
func AgreeNonempty(views [][]int) []int {
	if len(views) == 0 || len(views[0]) == 0 {
		return nil
	}
	first := views[0]
	for _, view := range views[1:] {
		if len(view) != len(first) {
			return nil
		}
		for i, index := range view {
			if index != first[i] {
				return nil
			}
		}
	}
	return append([]int(nil), first...)
}

// ClassifierView describes one (jpeg quality, flip) alternative tried by the
// Lotus GridClassifier multi-view fallback after the baseline 75-quality run.
type ClassifierView struct {
	Quality *int
	Flip    bool
}

// ClassifierViews returns the ordered alternative views after the baseline.
func ClassifierViews() []ClassifierView {
	return []ClassifierView{
		{Quality: nil, Flip: false},
		{Quality: intPtr(90), Flip: false},
		{Quality: intPtr(95), Flip: false},
		{Quality: nil, Flip: true},
	}
}

// GroupedRecovery mirrors Lotus groupedRecovery: labels and per-cell logits
// must be equal length; each label keeps its maximum logit; the top label must
// clear minimumScore and a minimumMargin over the runner-up; the answer is all
// cell indices whose label equals the top label.
func GroupedRecovery(labels []int, scores []float32, minimumScore, minimumMargin float32) []int {
	if len(labels) == 0 || len(labels) != len(scores) {
		return nil
	}
	groups := map[int]float32{}
	for i, label := range labels {
		score := scores[i]
		if math.IsNaN(float64(score)) || math.IsInf(float64(score), 0) {
			return nil
		}
		if current, ok := groups[label]; !ok || score > current {
			groups[label] = score
		}
	}
	type labelScore struct {
		label int
		score float32
	}
	ordered := make([]labelScore, 0, len(groups))
	for label, score := range groups {
		ordered = append(ordered, labelScore{label: label, score: score})
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].score > ordered[j].score })
	if len(ordered) == 0 {
		return nil
	}
	top := ordered[0]
	if top.score < minimumScore {
		return nil
	}
	if len(ordered) >= 2 && top.score-ordered[1].score < minimumMargin {
		return nil
	}
	answer := make([]int, 0, len(labels))
	for i, label := range labels {
		if label == top.label {
			answer = append(answer, i)
		}
	}
	return answer
}

// intPtr returns a pointer to v for the optional-quality classifier views.
func intPtr(v int) *int { return &v }
