package vision

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

func TestNMSAndDFINEOrdering(t *testing.T) {
	dets := []Detection{
		{Box: Box{0, 0, 100, 100}, Score: .9},
		{Box: Box{0, 0, 100, 100}, Score: .8},
		{Box: Box{200, 200, 220, 220}, Score: .7},
	}
	if got := NMS(dets, .01); len(got) != 2 || got[0].Score != .9 || got[1].Score != .7 {
		t.Fatalf("NMS %v", got)
	}

	labels := []int64{1, 1, 2, 2, 3, 4}
	boxes := []Box{
		{10, 10, 30, 30}, {12, 40, 32, 60},
		{100, 10, 120, 30}, {102, 40, 122, 60},
		{200, 10, 220, 30}, {170, 1, 190, 20},
	}
	scores := []float32{.5, .45, .6, .5, .9, .99}
	got, err := PostprocessDFINE(labels, boxes, scores)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 2 || got.Groups[0].ClassID != 1 || got.Groups[1].ClassID != 2 {
		t.Fatalf("D-FINE groups %+v", got.Groups)
	}
	want := []Point{{20, 20}, {110, 20}}
	if !reflect.DeepEqual(got.Points, want) {
		t.Fatalf("D-FINE points %v want %v", got.Points, want)
	}
	if got.Groups[0].Detections[0].Box.Y2 != 60 {
		t.Fatal("D-FINE detections must be bottom-y descending")
	}
}

func yoloAnchor(output []float32, anchors, a int, cx, cy, w, h, s0, s1 float32) {
	output[a], output[anchors+a] = cx, cy
	output[2*anchors+a], output[3*anchors+a] = w, h
	output[4*anchors+a], output[5*anchors+a] = s0, s1
}

func TestYOLOCrossClassNMSAndCoordinates(t *testing.T) {
	output := make([]float32, 12)
	yoloAnchor(output, 2, 0, 192, 192, 100, 100, .9, .1)
	yoloAnchor(output, 2, 1, 192, 192, 100, 100, .2, .8)
	got, err := PostprocessYOLO(output, 2, solidRGB(384, 384, 10, 20, 30))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Detections) != 1 || len(got.Top) != 1 || len(got.Bottom) != 0 {
		t.Fatalf("cross-class NMS %+v", got)
	}
	if got.Top[0].BBox != (IntBox{142, 142, 242, 242}) {
		t.Fatalf("YOLO box %+v", got.Top[0].BBox)
	}

	negative := make([]float32, 6)
	yoloAnchor(negative, 1, 0, 0, 0, 40, 40, .9, .1)
	padded, err := PostprocessYOLO(negative, 1, solidRGB(100, 100, 10, 20, 30))
	if err != nil {
		t.Fatal(err)
	}
	if padded.Top[0].BBox != (IntBox{-5, -5, 5, 5}) {
		t.Fatalf("negative box %+v", padded.Top[0].BBox)
	}
	if padded.Top[0].Image.W != 10 || !reflect.DeepEqual(padded.Top[0].Image.Pix[:3], []uint8{0, 0, 0}) {
		t.Fatal("YOLO crop did not preserve black padding")
	}
	if _, err := PostprocessYOLO(make([]float32, 6), 1, NewRGB(10, 10)); !errors.Is(err, ErrNoYOLODetections) {
		t.Fatalf("empty YOLO error %v", err)
	}
}

func TestGreedyMatchingAndNoJitter(t *testing.T) {
	bottom := []MatchCandidate{
		{BBox: IntBox{100, 0, 110, 10}, Feature: []float32{0}},
		{BBox: IntBox{0, 0, 10, 10}, Feature: []float32{1}},
	}
	top := []MatchCandidate{
		{BBox: IntBox{10, 20, 20, 30}, Feature: []float32{10}},
		{BBox: IntBox{20, 20, 30, 30}, Feature: []float32{20}},
		{BBox: IntBox{30, 20, 40, 30}, Feature: []float32{30}},
	}
	matrix := map[[2]int]float32{{1, 10}: .8, {1, 20}: .9, {1, 30}: .1, {0, 10}: .95, {0, 20}: .2, {0, 30}: .3}
	got, err := GreedyMatch(bottom, top, func(b, t []float32) float32 {
		return matrix[[2]int{int(b[0]), int(t[0])}]
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []IntBox{{20, 20, 30, 30}, {10, 20, 20, 30}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence %v want %v", got, want)
	}
	if _, err := GreedyMatch(bottom[:1], top[:1], nil); err != nil {
		t.Fatalf("single candidate should not need similarity: %v", err)
	}

	points := MatchedPoints([]IntBox{{0, 0, 10, 20}, {0, 0, 11, 21}}, false, nil)
	if !reflect.DeepEqual(points, []Point{{5, 10}, {5.5, 10.5}}) {
		t.Fatalf("no-jitter points %v", points)
	}
	jittered := MatchedPoints([]IntBox{{0, 0, 100, 100}}, true, rand.New(rand.NewSource(1)))
	if jittered[0].X < 30 || jittered[0].X > 70 || jittered[0].Y < 30 || jittered[0].Y > 70 {
		t.Fatalf("jitter outside inclusive range: %v", jittered)
	}
}

func TestPythonRoundAndCoordinateStrings(t *testing.T) {
	for input, want := range map[float64]int64{2.5: 2, 3.5: 4, -2.5: -2, -3.5: -4} {
		if got := PythonRound(input); got != want {
			t.Fatalf("round(%g)=%d want %d", input, got, want)
		}
	}
	if got := FormatGridPoints([]GridPoint{{1, 3}}); !reflect.DeepEqual(got, []string{"3_1"}) {
		t.Fatalf("grid strings %v", got)
	}
	if got := FormatIconPoints([]Point{{333, 166.5}}); !reflect.DeepEqual(got, []string{"10000_5000"}) {
		t.Fatalf("icon strings %v", got)
	}
}
