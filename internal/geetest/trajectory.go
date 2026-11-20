package geetest

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

//go:embed mousepath.json
var embeddedMousePath string

const mouseAlphabet = "()*,-./0123456789:?@ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz~"

type mouseEvent struct {
	kind      string
	x         int64
	y         int64
	timestamp int64
	dom       string
	fields    int
}

type preparedEvent struct {
	kind      string
	x         int64
	y         int64
	timestamp int64
	hasCoords bool
}

var (
	mousePathOnce sync.Once
	mousePathData []mouseEvent
	mousePathErr  error
)

func defaultMousePath() ([]mouseEvent, error) {
	mousePathOnce.Do(func() {
		mousePathData, mousePathErr = parseMousePath([]byte(embeddedMousePath))
	})
	return mousePathData, mousePathErr
}

func parseMousePath(data []byte) ([]mouseEvent, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var raw [][]any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("geetest: decode mousepath.json: %w", err)
	}
	path := make([]mouseEvent, 0, len(raw))
	for i, fields := range raw {
		if len(fields) < 2 {
			return nil, fmt.Errorf("geetest: mouse event %d has %d fields", i, len(fields))
		}
		kind, ok := fields[0].(string)
		if !ok {
			return nil, fmt.Errorf("geetest: mouse event %d kind is not a string", i)
		}
		e := mouseEvent{kind: kind, fields: len(fields)}
		var err error
		switch kind {
		case "move", "down", "up", "scroll":
			if len(fields) < 4 {
				return nil, fmt.Errorf("geetest: mouse event %d has too few fields", i)
			}
			if e.x, err = jsonInt64(fields[1]); err != nil {
				return nil, fmt.Errorf("geetest: mouse event %d x: %w", i, err)
			}
			if e.y, err = jsonInt64(fields[2]); err != nil {
				return nil, fmt.Errorf("geetest: mouse event %d y: %w", i, err)
			}
			if e.timestamp, err = jsonInt64(fields[3]); err != nil {
				return nil, fmt.Errorf("geetest: mouse event %d time: %w", i, err)
			}
		case "focus", "blur", "unload":
			if e.timestamp, err = jsonInt64(fields[1]); err != nil {
				return nil, fmt.Errorf("geetest: mouse event %d time: %w", i, err)
			}
		default:
			// Unknown records are retained for BGAB and discarded by preprocess.
		}
		if len(fields) > 4 {
			if fields[4] != nil {
				dom, ok := fields[4].(string)
				if !ok {
					return nil, fmt.Errorf("geetest: mouse event %d DOM type is not a string", i)
				}
				e.dom = dom
			}
		}
		path = append(path, e)
	}
	return path, nil
}

func jsonInt64(v any) (int64, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, fmt.Errorf("got %T, want integer", v)
	}
	i, err := n.Int64()
	if err != nil {
		return 0, err
	}
	return i, nil
}

func preprocessMousePath(path []mouseEvent) ([]preparedEvent, error) {
	if len(path) == 0 {
		return []preparedEvent{}, nil
	}
	filtered, err := filterMouseEvents(path)
	if err != nil {
		return nil, err
	}
	start := 0
	if len(filtered) >= 300 {
		start = len(filtered) - 300
	}
	var prevX, prevY, prevTime int64
	out := make([]preparedEvent, 0, len(filtered)-start)
	for _, event := range filtered[start:] {
		switch event.kind {
		case "down", "move", "up", "scroll":
			delta := int64(0)
			if prevTime != 0 {
				delta = clampMouseInt(event.timestamp - prevTime)
			}
			out = append(out, preparedEvent{
				kind: event.kind, x: event.x - prevX, y: event.y - prevY,
				timestamp: delta, hasCoords: true,
			})
			prevX, prevY, prevTime = event.x, event.y, event.timestamp
		case "blur", "focus", "unload":
			delta := int64(0)
			if prevTime != 0 {
				delta = clampMouseInt(event.timestamp - prevTime)
			}
			out = append(out, preparedEvent{kind: event.kind, timestamp: delta})
			prevTime = event.timestamp
		}
	}
	return out, nil
}

func filterMouseEvents(path []mouseEvent) ([]mouseEvent, error) {
	domType := ""
	for _, event := range path {
		// Python indexes event[4] for every truthy record until domType is nonempty.
		if event.fields <= 4 {
			return nil, fmt.Errorf("geetest: mouse event %q has no DOM event field", event.kind)
		}
		domType = event.dom
		if domType != "" {
			break
		}
	}
	if domType == "" {
		return append([]mouseEvent(nil), path...), nil
	}
	prefix := ""
	for _, candidate := range []string{"mouse", "touch", "pointer", "MSPointer"} {
		if strings.HasPrefix(domType, candidate) {
			prefix = candidate
		}
	}
	out := make([]mouseEvent, 0, len(path))
	for _, event := range path {
		switch event.kind {
		case "move", "down", "up":
			if !strings.HasPrefix(event.dom, prefix) {
				continue
			}
		}
		out = append(out, event)
	}
	return out, nil
}

func clampMouseInt(v int64) int64 {
	if v > 32767 {
		return 32767
	}
	if v < -32767 {
		return -32767
	}
	return v
}

func processMousePath(path []preparedEvent) (string, error) {
	types := make([]string, 0, len(path))
	times := make([]int64, 0, len(path))
	xs := make([]int64, 0, len(path))
	ys := make([]int64, 0, len(path))
	for _, event := range path {
		types = append(types, event.kind)
		times = append(times, event.timestamp)
		if event.hasCoords {
			xs = append(xs, event.x)
			ys = append(ys, event.y)
		}
	}
	typeBits, err := encodeEventTypes(types)
	if err != nil {
		return "", err
	}
	bits := typeBits + encodeMouseValues(times, false) +
		encodeMouseValues(xs, true) + encodeMouseValues(ys, true)
	if len(bits)%6 != 0 {
		bits += binaryPadded(0, 6-len(bits)%6)
	}
	var out strings.Builder
	out.Grow(len(bits) / 6)
	for i := 0; i+6 <= len(bits); i += 6 {
		v, _ := strconv.ParseUint(bits[i:i+6], 2, 6)
		out.WriteByte(mouseAlphabet[v])
	}
	return out.String(), nil
}

func binaryPadded(value int64, width int) string {
	var digits string
	if value < 0 {
		// Matches Python bin(value)[2:] for completeness; valid paths never need it.
		digits = "b" + strconv.FormatInt(-value, 2)
	} else {
		digits = strconv.FormatInt(value, 2)
	}
	if len(digits) < width {
		return strings.Repeat("0", width-len(digits)) + digits
	}
	return digits
}

func encodeEventTypes(types []string) (string, error) {
	codes := map[string]int64{
		"move": 0, "down": 1, "up": 2, "scroll": 3,
		"focus": 4, "blur": 5, "unload": 6, "unknown": 7,
	}
	compressed := make([]int64, 0, len(types))
	for r := 0; r < len(types); {
		kind := types[r]
		code, ok := codes[kind]
		if !ok {
			return "", fmt.Errorf("geetest: unknown mouse event %q", kind)
		}
		s := 0
		for s < 16 && r+s+1 < len(types) && types[r+s+1] == kind {
			s++
		}
		r += 1 + s
		if s != 0 {
			compressed = append(compressed, code|8, int64(s-1))
		} else {
			compressed = append(compressed, code)
		}
	}
	var out strings.Builder
	out.WriteString(binaryPadded(int64(len(types))|32768, 16))
	for _, value := range compressed {
		out.WriteString(binaryPadded(value, 4))
	}
	return out.String(), nil
}

func encodeMouseValues(values []int64, withSigns bool) string {
	compressed := make([]int64, 0, len(values))
	for r := 0; r < len(values); {
		s := 1
		value := values[r]
		magnitude := absInt64(value)
		for r+s < len(values) && values[r+s] == value && magnitude < 127 && s < 127 {
			s++
		}
		if s > 1 {
			base := int64(32768)
			if value < 0 {
				base = 49152
			}
			compressed = append(compressed, base|(int64(s)<<7)|magnitude)
		} else {
			compressed = append(compressed, value)
		}
		r += s
	}
	var lengths, magnitudes, signs strings.Builder
	for _, value := range compressed {
		magnitude := absInt64(value)
		t := int(math.Ceil(math.Log(float64(magnitude)+1) / math.Log(16)))
		if t == 0 {
			t = 1
		}
		lengths.WriteString(binaryPadded(int64(t-1), 2))
		magnitudes.WriteString(binaryPadded(magnitude, t*4))
	}
	if withSigns {
		for _, value := range compressed {
			if value != 0 && value>>15 != 1 {
				if value < 0 {
					signs.WriteByte('1')
				} else {
					signs.WriteByte('0')
				}
			}
		}
	}
	return binaryPadded(int64(len(compressed))|32768, 16) +
		lengths.String() + magnitudes.String() + signs.String()
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func encodeMousePath(path []mouseEvent, c []int64, s string) (string, error) {
	prepared, err := preprocessMousePath(path)
	if err != nil {
		return "", err
	}
	processed, err := processMousePath(prepared)
	if err != nil {
		return "", err
	}
	return postprocessMouse(processed, c, s)
}

func postprocessMouse(input string, c []int64, s string) (string, error) {
	if len(c) < 5 {
		return "", fmt.Errorf("geetest: c must contain at least five integers")
	}
	chars := []rune(input)
	originalLength := int64(len(chars))
	for i := 0; i < len(s); i += 2 {
		end := i + 2
		if end > len(s) {
			end = len(s)
		}
		v, err := strconv.ParseInt(s[i:end], 16, 64)
		if err != nil {
			return "", fmt.Errorf("geetest: invalid s hex %q: %w", s[i:end], err)
		}
		if originalLength == 0 {
			return "", fmt.Errorf("geetest: insertion into empty mouse encoding")
		}
		position := (c[0]*v*v + c[2]*v + c[4]) % originalLength
		chars = insertRune(chars, position, rune(v))
	}
	return string(chars), nil
}

func transformAjaxMouse(input string, c []int64, s string) (string, error) {
	if len(c) == 0 || s == "" {
		return input, nil
	}
	if len(c) < 5 {
		return "", fmt.Errorf("geetest: c must contain at least five integers")
	}
	chars := []rune(input)
	for i := 0; i < len(s); i += 2 {
		end := i + 2
		if end > len(s) {
			end = len(s)
		}
		v, err := strconv.ParseInt(s[i:end], 16, 64)
		if err != nil {
			return "", fmt.Errorf("geetest: invalid s hex %q: %w", s[i:end], err)
		}
		if len(chars) == 0 {
			return "", fmt.Errorf("geetest: insertion into empty mouse encoding")
		}
		position := (c[0]*v*v + c[2]*v + c[4]) % int64(len(chars))
		chars = insertRune(chars, position, rune(v))
	}
	return string(chars), nil
}

func insertRune(chars []rune, position int64, value rune) []rune {
	chars = append(chars, 0)
	copy(chars[position+1:], chars[position:])
	chars[position] = value
	return chars
}
