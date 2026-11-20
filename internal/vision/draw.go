package vision

import (
	"fmt"
	"image/color"
)

var drawPalette = [...]color.RGBA{
	{255, 0, 0, 255}, {0, 0, 255, 255}, {0, 128, 0, 255},
	{255, 255, 0, 255}, {255, 255, 255, 255}, {128, 0, 128, 255}, {255, 165, 0, 255},
}

// DrawDFINE draws retained groups, centers, zero-based class ordinals and score
// labels on a clone. Its tiny built-in bitmap font avoids external dependencies.
func DrawDFINE(src *RGB, result DFINEResult) (*RGB, error) {
	if src == nil {
		return nil, fmt.Errorf("vision: nil D-FINE drawing image")
	}
	out := src.Clone()
	for c1, group := range result.Groups {
		c := drawPalette[c1%len(drawPalette)]
		if c1 < len(result.Points) {
			p := result.Points[c1]
			out.Set(int(p.X), int(p.Y), c)
			drawText(out, int(p.X), int(p.Y), fmt.Sprintf("%d", c1), c)
		}
		for c2, det := range group.Detections {
			b := det.Box
			drawRect(out, IntBox{int(b.X1), int(b.Y1), int(b.X2), int(b.Y2)}, c)
			y := int(b.Y1) + 2
			if b.Y1 > 12 {
				y = int(b.Y1) - 12
			}
			drawText(out, int(b.X1)+2, y, fmt.Sprintf("%d_%d-%d: %.2f", group.ClassID, c1, c2, det.Score), c)
		}
	}
	return out, nil
}

// DrawMulti draws sequence boxes and one-based ordinals. Like the source, it
// errors when more than seven boxes exceed the fixed drawing palette.
func DrawMulti(src *RGB, sequence []IntBox) (*RGB, error) {
	if src == nil {
		return nil, fmt.Errorf("vision: nil Multi drawing image")
	}
	if len(sequence) > len(drawPalette) {
		return nil, fmt.Errorf("vision: Multi palette supports seven boxes, got %d", len(sequence))
	}
	out := src.Clone()
	white := color.RGBA{255, 255, 255, 255}
	for i, box := range sequence {
		drawRect(out, box, drawPalette[i])
		cx, cy := (box.X1+box.X2)/2, (box.Y1+box.Y2)/2
		drawText(out, cx, cy, fmt.Sprintf("%d", i+1), white)
	}
	return out, nil
}

func drawRect(im *RGB, b IntBox, c color.RGBA) {
	for x := b.X1; x <= b.X2; x++ {
		im.Set(x, b.Y1, c)
		im.Set(x, b.Y2, c)
	}
	for y := b.Y1; y <= b.Y2; y++ {
		im.Set(b.X1, y, c)
		im.Set(b.X2, y, c)
	}
}

// 5x7 glyphs cover every character emitted by the two visualization paths.
var glyphs = map[rune][7]uint8{
	'0': {14, 17, 19, 21, 25, 17, 14}, '1': {4, 12, 4, 4, 4, 4, 14},
	'2': {14, 17, 1, 2, 4, 8, 31}, '3': {30, 1, 1, 14, 1, 1, 30},
	'4': {2, 6, 10, 18, 31, 2, 2}, '5': {31, 16, 16, 30, 1, 1, 30},
	'6': {14, 16, 16, 30, 17, 17, 14}, '7': {31, 1, 2, 4, 8, 8, 8},
	'8': {14, 17, 17, 14, 17, 17, 14}, '9': {14, 17, 17, 15, 1, 1, 14},
	'_': {0, 0, 0, 0, 0, 0, 31}, '-': {0, 0, 0, 31, 0, 0, 0},
	':': {0, 4, 4, 0, 4, 4, 0}, '.': {0, 0, 0, 0, 0, 12, 12},
	' ': {0, 0, 0, 0, 0, 0, 0}, '?': {14, 17, 1, 2, 4, 0, 4},
}

func drawText(im *RGB, x, y int, text string, c color.RGBA) {
	for _, r := range text {
		glyph, ok := glyphs[r]
		if !ok {
			glyph = glyphs['?']
		}
		for row, bits := range glyph {
			for col := 0; col < 5; col++ {
				if bits&(1<<uint(4-col)) != 0 {
					im.Set(x+col, y+row, c)
				}
			}
		}
		x += 6
	}
}
