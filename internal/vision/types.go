package vision

// IntBox is an integer rectangle with an exclusive right and bottom edge,
// matching PIL.Image.crop semantics.
type IntBox struct {
	X1, Y1, X2, Y2 int
}

func (b IntBox) Width() int  { return b.X2 - b.X1 }
func (b IntBox) Height() int { return b.Y2 - b.Y1 }

// Box is a floating-point detector rectangle.
type Box struct {
	X1, Y1, X2, Y2 float32
}

// Point is an image-space point. IconPointStrings applies the service's
// fixed 333-pixel normalization to these coordinates.
type Point struct {
	X, Y float64
}

// GridPoint uses the original one-based row/column convention.
type GridPoint struct {
	Row, Col int
}

// Detection is shared by the D-FINE and YOLO post-processing stages.
type Detection struct {
	Box   Box
	Label int
	Score float32
}
