package vision

import (
	"math"
	"reflect"
	"testing"
)

func TestRoundEven(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0.5, 0}, {1.5, 2}, {2.5, 2}, {3.5, 4},
		{0.4, 0}, {1.6, 2}, {-0.5, 0},
	}
	for _, c := range cases {
		if got := RoundEven(c.in); got != c.want {
			t.Fatalf("RoundEven(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSymbolAssignment(t *testing.T) {
	// 3 prompts, 4 regions.
	scores := [][]float32{
		{0.9, 0.1, 0.2, 0.3},
		{0.2, 0.8, 0.1, 0.4},
		{0.3, 0.2, 0.7, 0.1},
	}
	got := SymbolAssignment(scores)
	want := []int{0, 1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SymbolAssignment = %v, want %v", got, want)
	}
	// Empty and oversized must be nil.
	if got := SymbolAssignment(nil); got != nil {
		t.Fatal("empty scores should be nil")
	}
	big := make([][]float32, 13)
	for i := range big {
		big[i] = make([]float32, 13)
	}
	if got := SymbolAssignment(big); got != nil {
		t.Fatal("oversized scores should be nil")
	}
	// Non-finite must abort.
	bad := [][]float32{{float32(math.NaN()), 0.5}}
	if got := SymbolAssignment(bad); got != nil {
		t.Fatal("NaN scores should be nil")
	}
}

func TestSymbolRegionFilter(t *testing.T) {
	boxes := []SymbolBox{
		{X: 0, Y: 0, Width: 10, Height: 10},   // too small
		{X: 0, Y: 0, Width: 100, Height: 100}, // > 60% area
		{X: 0, Y: 0, Width: 30, Height: 30},   // ok
	}
	got := SymbolRegionFilter(boxes, 100, 100)
	if len(got) != 1 || got[0].Width != 30 {
		t.Fatalf("SymbolRegionFilter = %+v, want 1 box of width 30", got)
	}
}

func TestNMSBoxes(t *testing.T) {
	boxes := []SymbolBox{
		{X: 0, Y: 0, Width: 10, Height: 10, Score: 0.9},
		{X: 2, Y: 2, Width: 10, Height: 10, Score: 0.8}, // overlaps
		{X: 50, Y: 50, Width: 10, Height: 10, Score: 0.7},
	}
	got := NMSBoxes(boxes, 0.35)
	if len(got) != 2 {
		t.Fatalf("NMSBoxes = %d boxes, want 2", len(got))
	}
}
