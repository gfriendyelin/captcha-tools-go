// Package board ports the rule-based Lotus board solvers: a 3x3 match
// (连连看) and a 5x5 gomoku. It is protocol-free and needs no models.
package board

import "fmt"

// Kinds accepted by Solve.
const (
	KindMatch  = "match"
	KindGomoku = "gomoku"
)

// Solve mirrors Lotus BoardSolver.solve. It returns the two swapped cells that
// produce a winning line on the given board. The caller's board is not mutated.
func Solve(kind string, input [][]int) ([2][2]int, error) {
	var empty [2][2]int
	if kind != KindMatch && kind != KindGomoku {
		return empty, fmt.Errorf("Expected board")
	}
	size := 3
	if kind == KindGomoku {
		size = 5
	}
	grid := make([][]int, len(input))
	for r, row := range input {
		grid[r] = append([]int(nil), row...)
	}
	if len(grid) != size {
		return empty, fmt.Errorf("Invalid board")
	}
	for _, row := range grid {
		if len(row) != size {
			return empty, fmt.Errorf("Invalid board")
		}
	}
	inRange := func(v int) bool { return v >= 0 && v < size }
	wins := func() bool {
		lines := make([][]int, 0, 2*size+2)
		lines = append(lines, grid...)
		for col := 0; col < size; col++ {
			line := make([]int, size)
			for row := 0; row < size; row++ {
				line[row] = grid[row][col]
			}
			lines = append(lines, line)
		}
		if size == 5 {
			for _, diagonal := range [][2]int{{0, 1}, {size - 1, -1}} {
				start, step := diagonal[0], diagonal[1]
				line := make([]int, size)
				for i := 0; i < size; i++ {
					line[i] = grid[start+step*i][i]
				}
				lines = append(lines, line)
			}
		}
		for _, line := range lines {
			first := line[0]
			if size != 3 && first == 0 {
				continue
			}
			equal := true
			for _, value := range line[1:] {
				if value != first {
					equal = false
					break
				}
			}
			if equal {
				return true
			}
		}
		return false
	}

	type cell struct{ r, c int }
	cells := make([]cell, 0, size*size)
	for r := 0; r < size; r++ {
		for c := 0; c < size; c++ {
			cells = append(cells, cell{r, c})
		}
	}
	for _, from := range cells {
		var targets []cell
		if kind == KindMatch {
			targets = []cell{{from.r, from.c + 1}, {from.r + 1, from.c}}
		} else if grid[from.r][from.c] != 0 {
			for _, to := range cells {
				if grid[to.r][to.c] == 0 {
					targets = append(targets, to)
				}
			}
		}
		for _, to := range targets {
			if !inRange(to.r) || !inRange(to.c) {
				continue
			}
			grid[from.r][from.c], grid[to.r][to.c] = grid[to.r][to.c], grid[from.r][from.c]
			won := wins()
			grid[from.r][from.c], grid[to.r][to.c] = grid[to.r][to.c], grid[from.r][from.c]
			if won {
				return [2][2]int{{from.r, from.c}, {to.r, to.c}}, nil
			}
		}
	}
	return empty, fmt.Errorf("No winning move")
}
