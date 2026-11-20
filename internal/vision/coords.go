package vision

import (
	"math"
	"strconv"
)

// PythonRound implements round(x) ties-to-even rather than math.Round's
// half-away-from-zero behavior.
func PythonRound(v float64) int64 { return int64(math.RoundToEven(v)) }

func FormatGridPoints(points []GridPoint) []string {
	out := make([]string, len(points))
	for i, p := range points {
		out[i] = strconv.Itoa(p.Col) + "_" + strconv.Itoa(p.Row)
	}
	return out
}

// FormatIconPoints applies the original fixed 333-pixel coordinate system.
func FormatIconPoints(points []Point) []string {
	out := make([]string, len(points))
	for i, p := range points {
		x := PythonRound(p.X / 333 * 10000)
		y := PythonRound(p.Y / 333 * 10000)
		out[i] = strconv.FormatInt(x, 10) + "_" + strconv.FormatInt(y, 10)
	}
	return out
}
