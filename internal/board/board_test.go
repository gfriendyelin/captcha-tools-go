package board

import (
	"reflect"
	"testing"
)

func TestMatchHorizontalWin(t *testing.T) {
	grid := [][]int{
		{3, 7, 3},
		{1, 3, 2},
		{4, 5, 6},
	}
	got, err := Solve(KindMatch, grid)
	if err != nil {
		t.Fatal(err)
	}
	if got != [2][2]int{{0, 1}, {1, 1}} {
		t.Fatalf("cells = %v, want [[0 1] [1 1]]", got)
	}
}

func TestMatchColumnWin(t *testing.T) {
	grid := [][]int{
		{7, 3, 1},
		{2, 3, 4},
		{3, 9, 6},
	}
	got, err := Solve(KindMatch, grid)
	if err != nil {
		t.Fatal(err)
	}
	if got != [2][2]int{{2, 0}, {2, 1}} {
		t.Fatalf("cells = %v, want [[2 0] [2 1]]", got)
	}
}

func TestGomokuWin(t *testing.T) {
	grid := [][]int{
		{1, 1, 1, 0, 1},
		{1, 0, 0, 0, 0},
		{0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0},
	}
	got, err := Solve(KindGomoku, grid)
	if err != nil {
		t.Fatal(err)
	}
	if got != [2][2]int{{1, 0}, {0, 3}} {
		t.Fatalf("cells = %v, want [[1 0] [0 3]]", got)
	}
}

func TestNoWinningMove(t *testing.T) {
	grid := [][]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}
	if _, err := Solve(KindMatch, grid); err == nil {
		t.Fatal("expected an error for an unwinnable board")
	}
}

func TestInvalidBoard(t *testing.T) {
	for _, grid := range [][][]int{
		{{1, 2}, {3, 4}},
		{{1, 2, 3}, {4, 5}},
	} {
		if _, err := Solve(KindMatch, grid); err == nil {
			t.Fatalf("expected an error for %v", grid)
		}
	}
	if _, err := Solve(KindGomoku, [][]int{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}}); err == nil {
		t.Fatal("expected an error for a 3x3 gomoku board")
	}
}

func TestUnknownKind(t *testing.T) {
	if _, err := Solve("chess", [][]int{{1}}); err == nil {
		t.Fatal("expected an error for an unknown kind")
	}
}

func TestSolveDoesNotMutateInput(t *testing.T) {
	grid := [][]int{
		{3, 7, 3},
		{1, 3, 2},
		{4, 5, 6},
	}
	want := make([][]int, len(grid))
	for i, row := range grid {
		want[i] = append([]int(nil), row...)
	}
	if _, err := Solve(KindMatch, grid); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(grid, want) {
		t.Fatalf("Solve mutated the input: %v", grid)
	}
}
