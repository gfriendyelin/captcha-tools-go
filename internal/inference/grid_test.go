package inference

import (
	"reflect"
	"testing"
)

func TestGridIndicesToCells(t *testing.T) {
	tests := []struct {
		name    string
		indices []int
		want    [][2]int
	}{
		{name: "top-left", indices: []int{0}, want: [][2]int{{1, 1}}},
		{name: "center", indices: []int{4}, want: [][2]int{{2, 2}}},
		{name: "bottom-right", indices: []int{8}, want: [][2]int{{3, 3}}},
		{name: "mixed", indices: []int{2, 5, 7}, want: [][2]int{{1, 3}, {2, 3}, {3, 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cells := gridIndicesToCells(tt.indices)
			got := make([][2]int, len(cells))
			for i, c := range cells {
				got[i] = [2]int{c.Row, c.Col}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("gridIndicesToCells(%v) = %v, want %v", tt.indices, got, tt.want)
			}
		})
	}
}
