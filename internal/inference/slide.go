package inference

import (
	"context"
	"fmt"

	"github.com/kafuneri/captcha-tools-go/internal/vision"
)

// SlideSolve mirrors Lotus SlideSolver.solve: convert the background with the
// "white" alpha mode (opaque JPEG) and the piece with the "zero" mode
// (transparent PNG pixels become black), bilateral-filter both, take the
// morphological gradient, restrict the search band with the hint y when valid,
// run TM_CCOEFF_NORMED, and return the best x offset.
func (r *Registry) SlideSolve(ctx context.Context, background, piece []byte, hintY int) (int, error) {
	if r == nil {
		return 0, fmt.Errorf("nil registry")
	}
	bgRGBA, err := vision.RGBAFromBytes(background)
	if err != nil {
		return 0, fmt.Errorf("slide background: %w", err)
	}
	pcRGBA, err := vision.RGBAFromBytes(piece)
	if err != nil {
		return 0, fmt.Errorf("slide piece: %w", err)
	}
	bg := vision.GrayFromRGBA(bgRGBA, "white")
	pc := vision.GrayFromRGBA(pcRGBA, "zero")
	if bg == nil || pc == nil {
		return 0, fmt.Errorf("slide: invalid gray conversion")
	}
	if pc.W > bg.W || pc.H > bg.H {
		return 0, fmt.Errorf("slide: piece %dx%d larger than background %dx%d", pc.W, pc.H, bg.W, bg.H)
	}
	bgEdge := vision.MorphGradient(vision.BilateralFilter5(bg))
	pcEdge := vision.MorphGradient(vision.BilateralFilter5(pc))
	search := bgEdge
	roiY, roiH := 0, bgEdge.H
	if hintY > 0 && hintY+pcEdge.H <= bgEdge.H {
		roiY, roiH = hintY, pcEdge.H
		search = vision.ROIGray(bgEdge, roiY, roiH)
	}
	scores := vision.TemplateMatchCCOEFFNormed(search, pcEdge)
	if scores == nil {
		return 0, fmt.Errorf("slide: template match failed")
	}
	bestX, bestVal := 0, float64(-1e18)
	outW := search.W - pcEdge.W + 1
	for i, v := range scores {
		if v > bestVal {
			bestVal = v
			bestX = i % outW
		}
	}
	return bestX, nil
}
