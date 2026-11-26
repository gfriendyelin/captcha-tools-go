package vision

import (
	"math"
	"reflect"
	"testing"
)

func TestSelectTopKOrdering(t *testing.T) {
	logits := []float32{0.1, 0.9, 0.5, 0.7}
	got, err := SelectTopK(logits, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 3} // top scores {0.9,0.7}, ascending index
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SelectTopK = %v, want %v", got, want)
	}
}

func TestSelectTopKInvalid(t *testing.T) {
	if _, err := SelectTopK([]float32{1, 2}, 0); err == nil {
		t.Fatal("count 0 should error")
	}
	if _, err := SelectTopK([]float32{1, 2}, 3); err == nil {
		t.Fatal("count > len should error")
	}
}

func TestGridLabelsArgmax(t *testing.T) {
	output := []float32{0.1, 0.4, 0.3, 0.9, 0.2, 0.1}
	got, err := GridLabels(output, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatalf("GridLabels = %v, want [1 0]", got)
	}
	if _, err := GridLabels(output, 2, 4); err == nil {
		t.Fatal("mismatched length should error")
	}
}

func TestClassifierIndices(t *testing.T) {
	// 9 cells + prompt target; prompt (last) label 2.
	labels := []int{0, 1, 2, 0, 2, 1, 2, 0, 1, 2}
	got, err := ClassifierIndices(labels)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{2, 4, 6}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClassifierIndices = %v, want %v", got, want)
	}
	if _, err := ClassifierIndices([]int{1}); err == nil {
		t.Fatal("single row should error")
	}
}

func TestAgreeNonempty(t *testing.T) {
	if got := AgreeNonempty(nil); got != nil {
		t.Fatalf("nil views = %v, want nil", got)
	}
	if got := AgreeNonempty([][]int{{}}); got != nil {
		t.Fatalf("empty view = %v, want nil", got)
	}
	views := [][]int{{1, 3}, {1, 3}}
	got := AgreeNonempty(views)
	if !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("AgreeNonempty = %v, want [1 3]", got)
	}
	if got := AgreeNonempty([][]int{{1, 3}, {1, 4}}); got != nil {
		t.Fatalf("discordant views = %v, want nil", got)
	}
}

func TestGroupedRecovery(t *testing.T) {
	labels := []int{5, 5, 7, 5, 7}
	scores := []float32{0.9, 0.8, 0.2, 0.7, 0.1}
	got := GroupedRecovery(labels, scores, 0, 1.5)
	want := []int{0, 1, 3} // label 5 wins, margin 0.9-0.2=0.7 < 1.5 -> nil actually
	if got != nil {
		t.Fatalf("GroupedRecovery = %v, want nil (margin too small)", got)
	}
	scores2 := []float32{2.0, 1.9, 0.2, 1.8, 0.1}
	got = GroupedRecovery(labels, scores2, 0, 1.5)
	want = []int{0, 1, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GroupedRecovery = %v, want %v", got, want)
	}
	// Non-finite score must abort.
	bad := []float32{2.0, float32(math.NaN()), 0.2, 1.8, 0.1}
	if got := GroupedRecovery(labels, bad, 0, 1.5); got != nil {
		t.Fatalf("GroupedRecovery with NaN = %v, want nil", got)
	}
}

func TestFormatGridPoints(t *testing.T) {
	points := []GridPoint{{Row: 1, Col: 1}, {Row: 2, Col: 3}}
	got := FormatGridPoints(points)
	want := []string{"1_1", "3_2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FormatGridPoints = %v, want %v", got, want)
	}
}

func TestGridCoordinatesFromIndex(t *testing.T) {
	// Lotus answer (i%3)+1_(floor(i/3)+1) == Col_Row.
	for i := 0; i < 9; i++ {
		col := i%3 + 1
		row := i/3 + 1
		got := FormatGridPoints([]GridPoint{{Row: row, Col: col}})
		want := []string{formatColRow(i)}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("index %d -> %v, want %v", i, got, want)
		}
	}
}

func formatColRow(i int) string {
	col := i%3 + 1
	row := i/3 + 1
	return string(rune('0'+col)) + "_" + string(rune('0'+row))
}
