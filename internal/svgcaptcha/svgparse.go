// Package svgcaptcha solves the GeeTest V4 SVG grid puzzles (svg_seed and
// svg_icon). The SVG holds three frames, each split into a grid of cells; the
// hint image is the shape to locate, so every cell is rendered and scored
// against the hint's edges.
//
// The extraction and scoring strategy follows wulu007/geetest-bypass
// (src/wulu_geetest_bypass/solver/svg.py, MIT) and is reimplemented here with a
// pure Go SVG rasterizer plus small image-processing primitives.
package svgcaptcha

import (
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const svgNamespace = "http://www.w3.org/2000/svg"

var (
	shapeNames = map[string]bool{
		"path": true, "circle": true, "ellipse": true, "rect": true,
		"polygon": true, "polyline": true, "line": true,
	}

	translatePattern = regexp.MustCompile(`translate\([^)]*\)`)
	scalePattern     = regexp.MustCompile(`scale\(-?[\d.]+\)`)
	rotatePattern    = regexp.MustCompile(`rotate\(-?[\d.]+\)`)
	seedPattern      = regexp.MustCompile(`data-captcha-type\s*=\s*["']geetest-svg["']`)
	durationPattern  = regexp.MustCompile(`geetest_frame\d_animation_hash\s+([\d.]+)s`)
	keyframePattern  = regexp.MustCompile(`@keyframes geetest_frame(\d)_animation_hash\s*\{`)
	stepPattern      = regexp.MustCompile(`([\d.]+)%\s*\{\s*opacity:\s*([\d.]+)`)
)

type rawGroup struct {
	Attrs []xml.Attr `xml:",any,attr"`
	Inner string     `xml:",innerxml"`
}

type gridRect struct {
	width, height float64
}

type grid struct {
	svg    string
	width  int
	height int
}

// extractGrids returns one standalone SVG per grid cell, in document order.
func extractGrids(source string) ([]grid, error) {
	frames := 0
	var grids []grid
	var pending *gridRect

	decoder := xml.NewDecoder(strings.NewReader(source))
	depth := 0
	frameDepth := -1
	for {
		before := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse svg: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if frameDepth < 0 {
				if hasClass(element.Attr, "geetest_frame_hash") {
					frameDepth = depth
					frames++
				}
				continue
			}
			if hasClass(element.Attr, "geetest_grid_hash") && element.Name.Local == "rect" {
				pending = &gridRect{width: attrFloat(element.Attr, "width"), height: attrFloat(element.Attr, "height")}
				continue
			}
			if pending != nil && depth == frameDepth+1 && element.Name.Local == "g" {
				raw, _, err := captureElement(decoder, source, before, depth)
				if err != nil {
					return nil, err
				}
				grid, err := buildGridSVG(raw, *pending)
				if err != nil {
					return nil, err
				}
				grids = append(grids, grid)
				pending = nil
				// The whole subtree, including its end tag, was consumed.
				depth--
			}
		case xml.EndElement:
			if depth == frameDepth {
				frameDepth = -1
			}
			depth--
		}
	}
	if frames == 0 {
		return nil, fmt.Errorf("no geetest_frame_hash found in SVG")
	}
	if len(grids) == 0 {
		return nil, fmt.Errorf("no valid grids extracted from SVG")
	}
	return grids, nil
}

func captureElement(decoder *xml.Decoder, source string, start int64, depth int) (string, int64, error) {
	current := depth
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", 0, fmt.Errorf("parse svg element: %w", err)
		}
		switch token.(type) {
		case xml.StartElement:
			current++
		case xml.EndElement:
			current--
			if current == depth-1 {
				end := decoder.InputOffset()
				return source[start:end], end, nil
			}
		}
	}
}

func buildGridSVG(raw string, size gridRect) (grid, error) {
	var group rawGroup
	if err := xml.Unmarshal([]byte(raw), &group); err != nil {
		return grid{}, fmt.Errorf("parse grid group: %w", err)
	}
	if size.width == 0 || size.height == 0 {
		return grid{}, fmt.Errorf("grid rect has no size")
	}
	attributes := map[string]string{}
	order := make([]string, 0, len(group.Attrs))
	for _, attr := range group.Attrs {
		name := attr.Name.Local
		if _, seen := attributes[name]; !seen {
			order = append(order, name)
		}
		attributes[name] = attr.Value
	}
	centerX, centerY := size.width/2, size.height/2
	if transform, ok := attributes["transform"]; ok && strings.Contains(transform, "translate(") {
		transform = translatePattern.ReplaceAllString(transform, fmt.Sprintf("translate(%g,%g)", centerX, centerY))
		transform = scalePattern.ReplaceAllString(transform, "scale(1)")
		transform = rotatePattern.ReplaceAllString(transform, "rotate(0)")
		attributes["transform"] = transform
	}
	if !hasOwnStyle(group.Inner) {
		attributes["fill"] = "none"
		attributes["stroke"] = "#000"
		attributes["stroke-width"] = "2"
		attributes["stroke-linecap"] = "round"
		attributes["stroke-linejoin"] = "round"
	}
	inner, err := rewriteShapes(group.Inner)
	if err != nil {
		return grid{}, err
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, `<svg xmlns="%s" width="%g" height="%g" viewBox="0 0 %g %g"><g`,
		svgNamespace, size.width, size.height, size.width, size.height)
	for _, name := range order {
		fmt.Fprintf(&builder, ` %s="%s"`, name, escapeAttribute(attributes[name]))
	}
	for _, name := range []string{"fill", "stroke", "stroke-width", "stroke-linecap", "stroke-linejoin"} {
		if _, ok := attributes[name]; !ok {
			continue
		}
		printed := false
		for _, existing := range order {
			if existing == name {
				printed = true
				break
			}
		}
		if !printed {
			fmt.Fprintf(&builder, ` %s="%s"`, name, escapeAttribute(attributes[name]))
		}
	}
	builder.WriteString(">")
	builder.WriteString(inner)
	builder.WriteString("</g></svg>")
	return grid{
		svg:    builder.String(),
		width:  int(size.width + 0.5),
		height: int(size.height + 0.5),
	}, nil
}

// rewriteShapes drops namespaces and gives every shape without an explicit
// fill the "none" default, so only the outline shows in the edge image.
func rewriteShapes(inner string) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(inner))
	var builder strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse grid shapes: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			builder.WriteString("<")
			builder.WriteString(element.Name.Local)
			styled := false
			for _, attr := range element.Attr {
				switch attr.Name.Local {
				case "fill", "stroke":
					styled = true
				case "style":
					if strings.Contains(attr.Value, "fill") || strings.Contains(attr.Value, "stroke") {
						styled = true
					}
				}
				fmt.Fprintf(&builder, ` %s="%s"`, attr.Name.Local, escapeAttribute(attr.Value))
			}
			if !styled && shapeNames[element.Name.Local] {
				builder.WriteString(` fill="none"`)
			}
			builder.WriteString(">")
		case xml.EndElement:
			builder.WriteString("</")
			builder.WriteString(element.Name.Local)
			builder.WriteString(">")
		case xml.CharData:
			builder.WriteString(escapeText(string(element)))
		}
	}
	return builder.String(), nil
}

func hasOwnStyle(inner string) bool {
	for _, marker := range []string{`style="`, `fill="`, `stroke="`, `style='`, `fill='`, `stroke='`} {
		if strings.Contains(inner, marker) {
			return true
		}
	}
	return false
}

func hasClass(attrs []xml.Attr, class string) bool {
	for _, attr := range attrs {
		if attr.Name.Local == "class" && strings.Contains(attr.Value, class) {
			return true
		}
	}
	return false
}

func attrFloat(attrs []xml.Attr, name string) float64 {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			value, err := strconv.ParseFloat(attr.Value, 64)
			if err == nil {
				return value
			}
			return 0
		}
	}
	return 0
}

func escapeAttribute(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return replacer.Replace(value)
}

func escapeText(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

// GridCols counts the distinct grid columns from the first frame's rect x
// coordinates, so it does not depend on the puzzle type.
func GridCols(source string) (int, error) {
	decoder := xml.NewDecoder(strings.NewReader(source))
	depth := 0
	frameDepth := -1
	columns := map[float64]bool{}
	done := false
	for !done {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("parse svg: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if frameDepth < 0 {
				if hasClass(element.Attr, "geetest_frame_hash") {
					frameDepth = depth
				}
				continue
			}
			if depth == frameDepth+1 && hasClass(element.Attr, "geetest_grid_hash") && element.Name.Local == "rect" {
				columns[roundTenth(attrFloat(element.Attr, "x"))] = true
			}
		case xml.EndElement:
			if depth == frameDepth && frameDepth >= 0 {
				frameDepth = -1
				done = len(columns) > 0
			}
			depth--
		}
	}
	if len(columns) == 0 {
		return 0, fmt.Errorf("no geetest_grid_hash found in SVG")
	}
	return len(columns), nil
}

func roundTenth(value float64) float64 {
	return float64(int64(value*10+0.5)) / 10
}

// FrameTimes parses each frame's visible window (milliseconds) from the CSS
// keyframes: the span in which its opacity is above zero.
func FrameTimes(source string) ([][2]int, error) {
	match := durationPattern.FindStringSubmatch(source)
	if match == nil {
		return nil, fmt.Errorf("no frame animation duration found in SVG")
	}
	seconds, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return nil, err
	}
	total := int(seconds*1000 + 0.5)
	frames := make([][2]int, 0, 3)
	for index := 1; index <= 3; index++ {
		block, err := keyframeBlock(source, index)
		if err != nil {
			return nil, err
		}
		steps := stepPattern.FindAllStringSubmatch(block, -1)
		low, high := -1.0, -1.0
		for _, step := range steps {
			opacity, err := strconv.ParseFloat(step[2], 64)
			if err != nil || opacity <= 0 {
				continue
			}
			percent, err := strconv.ParseFloat(step[1], 64)
			if err != nil {
				continue
			}
			if low < 0 || percent < low {
				low = percent
			}
			if high < 0 || percent > high {
				high = percent
			}
		}
		if low < 0 {
			return nil, fmt.Errorf("frame %d has no visible step", index)
		}
		frames = append(frames, [2]int{
			int(low/100*float64(total) + 0.5),
			int(high/100*float64(total) + 0.5),
		})
	}
	return frames, nil
}

func keyframeBlock(source string, index int) (string, error) {
	location := keyframePattern.FindStringSubmatchIndex(source)
	for location != nil {
		number, err := strconv.Atoi(source[location[2]:location[3]])
		if err == nil && number == index {
			start := location[1] - 1
			depth := 0
			for position := start; position < len(source); position++ {
				switch source[position] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						return source[start : position+1], nil
					}
				}
			}
			return "", fmt.Errorf("unbalanced @keyframes for frame %d in SVG", index)
		}
		next := keyframePattern.FindStringSubmatchIndex(source[location[1]:])
		if next == nil {
			break
		}
		offset := location[1]
		location = []int{next[0] + offset, next[1] + offset, next[2] + offset, next[3] + offset}
	}
	return "", fmt.Errorf("no @keyframes for frame %d in SVG", index)
}
