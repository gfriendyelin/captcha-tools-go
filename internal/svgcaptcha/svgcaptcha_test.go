package svgcaptcha

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"sort"
	"testing"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

const targetCircle = `<circle cx="0" cy="0" r="18"/>`

// syntheticPuzzle builds the three-frame, 2x2 layout GeeTest serves: every
// frame holds four grid rects, each followed by its shape group. Shapes are
// drawn around the origin because the grid group's translate is rewritten to
// the cell center.
func syntheticPuzzle() string {
	shapes := []string{
		`<rect x="-18" y="-28" width="36" height="56"/>`,
		`<polygon points="-22,28 22,28 0,-28"/>`,
		`<ellipse cx="0" cy="0" rx="22" ry="14"/>`,
		`<path d="M-22 0 L22 0 L0 -28 Z"/>`,
	}
	frames := [][]string{
		{shapes[0], shapes[1], targetCircle, shapes[3]},
		{shapes[1], shapes[3], shapes[0], shapes[2]},
		{shapes[2], shapes[0], shapes[1], shapes[3]},
	}
	var body bytes.Buffer
	for frameIndex, frame := range frames {
		number := frameIndex + 1
		fmt.Fprintf(&body, `<g class="geetest_frame_hash geetest_frame%d">`, number)
		for cellIndex, shape := range frame {
			x := (cellIndex % 2) * 60
			y := (cellIndex / 2) * 80
			fmt.Fprintf(&body, `<rect class="geetest_grid_hash" x="%d" y="%d" width="60" height="80"/>`, x, y)
			fmt.Fprintf(&body, `<g transform="translate(0,0)"><g fill="none" stroke="#000" stroke-width="2">%s</g></g>`, shape)
		}
		body.WriteString(`</g>`)
	}
	return `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="160" viewBox="0 0 120 160">` +
		`<style>` +
		`@keyframes geetest_frame1_animation_hash { 10% {opacity:1} 33% {opacity:1} 34% {opacity:0} }` +
		`@keyframes geetest_frame2_animation_hash { 34% {opacity:1} 66% {opacity:1} 67% {opacity:0} }` +
		`@keyframes geetest_frame3_animation_hash { 67% {opacity:1} 99% {opacity:1} 100% {opacity:0} }` +
		`.frame1 { animation: geetest_frame1_animation_hash 1.5s infinite; }` +
		`</style>` + body.String() + `</svg>`
}

// hintPNG renders the hint the way GeeTest ships it: the shape alone on a
// transparent background.
func hintPNG(t *testing.T, width, height int, shape string) []byte {
	t.Helper()
	source := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d"><g transform="translate(%d,%d)"><g fill="none" stroke="#000" stroke-width="2">%s</g></g></svg>`,
		width, height, width, height, width/2, height/2, shape)
	icon, err := oksvg.ReadIconStream(bytes.NewReader([]byte(source)), oksvg.WarnErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	icon.SetTarget(0, 0, float64(width), float64(height))
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	raster := rasterx.NewDasher(width, height, rasterx.NewScannerGV(width, height, canvas, canvas.Bounds()))
	icon.Draw(raster, 1.0)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestExtractGridsAndColumns(t *testing.T) {
	grids, err := extractGrids(syntheticPuzzle())
	if err != nil {
		t.Fatal(err)
	}
	if len(grids) != 12 {
		t.Fatalf("grids = %d, want 12", len(grids))
	}
	if grids[0].width != 60 || grids[0].height != 80 {
		t.Fatalf("grid size = %dx%d, want 60x80", grids[0].width, grids[0].height)
	}
	cols, err := GridCols(syntheticPuzzle())
	if err != nil {
		t.Fatal(err)
	}
	if cols != 2 {
		t.Fatalf("cols = %d, want 2", cols)
	}
}

func TestFrameTimes(t *testing.T) {
	frames, err := FrameTimes(syntheticPuzzle())
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{150, 495}, {510, 990}, {1005, 1485}}
	if len(frames) != len(want) {
		t.Fatalf("frames = %v, want %v", frames, want)
	}
	for index := range want {
		if frames[index] != want[index] {
			t.Fatalf("frame %d = %v, want %v", index, frames[index], want[index])
		}
	}
}

func TestSolveFindsTargetCell(t *testing.T) {
	// The 2x2 layout ships a hint at twice the cell resolution, which the
	// reference shrinks by 0.5 before matching.
	result, err := Solve(syntheticPuzzle(), hintPNG(t, 120, 160, `<circle cx="0" cy="0" r="36"/>`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Layer != 0 || result.Row != 2 || result.Col != 1 || result.Cols != 2 {
		t.Fatalf("result = layer %d row %d col %d cols %d (scores %v)",
			result.Layer, result.Row, result.Col, result.Cols, roundScores(result.Scores))
	}
	if result.StartMS != 150 || result.EndMS != 495 {
		t.Fatalf("frame window = %d-%d, want 150-495", result.StartMS, result.EndMS)
	}
}

func roundScores(scores []float64) []float64 {
	rounded := append([]float64(nil), scores...)
	sort.Slice(rounded, func(i, j int) bool { return rounded[i] > rounded[j] })
	if len(rounded) > 4 {
		rounded = rounded[:4]
	}
	return rounded
}
