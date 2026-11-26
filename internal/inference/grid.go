package inference

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kafuneri/captcha-tools-go/internal/vision"
	ort "github.com/yalue/onnxruntime_go"
)

// GridClassifier runs the HGNet classifier over [cells..., prompt] and returns
// the matching cell indices. The baseline uses JPEG quality 75 without flip;
// when the baseline is empty and emptyFallback is true the Lotus alternative
// views (no-JPEG, 90, 95, flip) are tried and must agree. A nil result with
// emptyFallback reports the Lotus error string so callers can detect it.
func (r *Registry) GridClassifier(ctx context.Context, cells []*vision.RGB, prompt *vision.RGB, emptyFallback bool) ([]int, error) {
	if r == nil {
		return nil, fmt.Errorf("nil registry")
	}
	if len(cells) != 9 {
		return nil, fmt.Errorf("classifier needs 9 cells, got %d", len(cells))
	}
	lease, err := r.acquire(ctx, "pdl")
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*PDLResource)
	if !ok {
		return nil, fmt.Errorf("pdl resource has type %T", lease.Resource())
	}
	baseline, err := r.classify(ctx, resource, cells, prompt, intPtr(75), false)
	if err != nil {
		return nil, err
	}
	indices, err := vision.ClassifierIndices(baseline)
	if err != nil {
		return nil, err
	}
	if len(indices) > 0 || !emptyFallback {
		return indices, nil
	}
	var views [][]int
	for _, v := range vision.ClassifierViews() {
		labels, err := r.classify(ctx, resource, cells, prompt, v.Quality, v.Flip)
		if err != nil {
			return nil, err
		}
		idx, err := vision.ClassifierIndices(labels)
		if err != nil {
			return nil, err
		}
		views = append(views, idx)
	}
	agreed := vision.AgreeNonempty(views)
	if agreed == nil {
		return nil, fmt.Errorf("九宫格模型未识别到匹配项")
	}
	return agreed, nil
}

// GridSemanticRecovery mirrors Lotus GridSemanticRecovery.solve: run the
// classifier with empty fallback; when it reports no match, use the HGNet
// labels (prompt row dropped) and the SigLIP matcher logits to infer the
// answer by grouped recovery.
func (r *Registry) GridSemanticRecovery(ctx context.Context, cells []*vision.RGB, prompt *vision.RGB, minimumScore, minimumMargin float32) ([]int, error) {
	if r == nil {
		return nil, fmt.Errorf("nil registry")
	}
	if len(cells) != 9 {
		return nil, fmt.Errorf("semantic recovery needs 9 cells, got %d", len(cells))
	}
	indices, err := r.GridClassifier(ctx, cells, prompt, true)
	if err != nil && !strings.Contains(err.Error(), "未识别到匹配项") {
		return nil, err
	}
	if err == nil {
		return indices, nil
	}
	labels, labelErr := r.gridLabels(ctx, cells, prompt)
	if labelErr != nil {
		return nil, errors.Join(err, labelErr)
	}
	logits, logitErr := r.gridLogits(ctx, cells, prompt)
	if logitErr != nil {
		return nil, errors.Join(err, logitErr)
	}
	recovered := vision.GroupedRecovery(labels, logits, minimumScore, minimumMargin)
	if recovered == nil {
		return nil, fmt.Errorf("no confident semantic grid recovery")
	}
	return recovered, nil
}

// GridMatch runs the Lotus GridMatcher (grid-vision + grid-head) and returns
// the top-count cell indices sorted ascending. count is the number of matching
// cells expected (>=1).
func (r *Registry) GridMatch(ctx context.Context, prompt *vision.RGB, cells []*vision.RGB, count int) ([]int, error) {
	if r == nil {
		return nil, fmt.Errorf("nil registry")
	}
	if count < 1 || count > len(cells) {
		return nil, fmt.Errorf("grid count %d out of range for %d cells", count, len(cells))
	}
	logits, err := r.gridLogits(ctx, cells, prompt)
	if err != nil {
		return nil, err
	}
	return vision.SelectTopK(logits, count)
}

// gridLabels classifies at the baseline 75-quality view and drops the prompt
// row, returning the per-cell labels used by grouped recovery.
func (r *Registry) gridLabels(ctx context.Context, cells []*vision.RGB, prompt *vision.RGB) ([]int, error) {
	lease, err := r.acquire(ctx, "pdl")
	if err != nil {
		return nil, err
	}
	resource, ok := lease.Resource().(*PDLResource)
	if !ok {
		lease.Release()
		return nil, fmt.Errorf("pdl resource has type %T", lease.Resource())
	}
	labels, err := r.classify(ctx, resource, cells, prompt, intPtr(75), false)
	lease.Release()
	if err != nil {
		return nil, err
	}
	if len(labels) < 2 {
		return nil, fmt.Errorf("classifier returned %d rows", len(labels))
	}
	return labels[:len(labels)-1], nil
}

// gridLogits returns the raw per-cell logits from the SigLIP matcher for
// [prompt, ...cells].
func (r *Registry) gridLogits(ctx context.Context, cells []*vision.RGB, prompt *vision.RGB) ([]float32, error) {
	lease, err := r.acquire(ctx, "grid")
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*GridResource)
	if !ok {
		return nil, fmt.Errorf("grid resource has type %T", lease.Resource())
	}
	images := append([]*vision.RGB{prompt}, cells...)
	prep, err := vision.GridNormalizeResize(images, 256)
	if err != nil {
		return nil, err
	}
	tensor, err := ort.NewTensor(ort.Shape{int64(len(images)), 3, 256, 256}, prep.Values)
	if err != nil {
		return nil, err
	}
	outputs, err := resource.Vision.Run(ctx, []ort.Value{tensor})
	if err != nil {
		return nil, err
	}
	if len(outputs) != 1 || len(outputs[0].Float32) == 0 {
		return nil, fmt.Errorf("unexpected grid-vision output")
	}
	features := outputs[0].Float32
	batch := len(images)
	stride := len(features) / batch
	promptFeat, cellFeat, err := new(vision.GridMatcher).Scores(features, batch, stride, len(cells))
	if err != nil {
		return nil, err
	}
	promptTensor, err := ort.NewTensor(ort.Shape{int64(len(cells)), 576, 8, 8}, promptFeat)
	if err != nil {
		return nil, err
	}
	cellTensor, err := ort.NewTensor(ort.Shape{int64(len(cells)), 576, 8, 8}, cellFeat)
	if err != nil {
		promptTensor.Destroy()
		return nil, err
	}
	headOut, err := resource.Head.Run(ctx, []ort.Value{promptTensor, cellTensor})
	if err != nil {
		return nil, err
	}
	if len(headOut) != 1 || len(headOut[0].Float32) != len(cells) {
		return nil, fmt.Errorf("unexpected grid-head output: %d values for %d cells", len(headOut[0].Float32), len(cells))
	}
	return headOut[0].Float32, nil
}

// classify runs the PDL/HGNet session over [cells..., prompt] and returns the
// winning class per row.
func (r *Registry) classify(ctx context.Context, resource *PDLResource, cells []*vision.RGB, prompt *vision.RGB, quality *int, flip bool) ([]int, error) {
	images := append(append([]*vision.RGB(nil), cells...), prompt)
	prep, err := vision.PreprocessGridClassifier(images, quality, flip)
	if err != nil {
		return nil, err
	}
	tensor, err := ort.NewTensor(ort.Shape{int64(len(images)), 3, 224, 224}, prep.Values)
	if err != nil {
		return nil, err
	}
	outputs, err := resource.Session.Run(ctx, []ort.Value{tensor})
	if err != nil {
		return nil, err
	}
	if len(outputs) != 1 || len(outputs[0].Float32) == 0 {
		return nil, fmt.Errorf("unexpected PDL classifier output")
	}
	classes := len(outputs[0].Float32) / len(images)
	return vision.GridLabels(outputs[0].Float32, len(images), classes)
}

func intPtr(v int) *int { return &v }
