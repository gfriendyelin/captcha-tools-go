package v4

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"math"
	"math/rand"
	"time"
)

// Point is a normalized coordinate.
type Point [2]float64

// Track is the compressed pointer trajectory sent alongside an answer.
type Track struct {
	M int          `json:"m"`
	W float64      `json:"w"`
	H float64      `json:"h"`
	S int64        `json:"s"`
	E int64        `json:"e"`
	P [][4]float64 `json:"p"`
}

const (
	trackWidth   = 300.015625
	trackHeight  = 259.6015625
	slideHeight  = 261.5234375
	answerWidth  = 300.0
	answerHeight = 200.0
)

func uniform(a, b float64) float64 { return a + rand.Float64()*(b-a) }

func integer(a, b int) int { return a + rand.Intn(b-a+1) }

func rounded(x float64) float64 { return math.Round(x*10000) / 10000 }

func clip(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func jitter(point Point, amount float64) Point {
	return Point{clip(point[0] + uniform(-amount, amount)), clip(point[1] + uniform(-amount, amount))}
}

type trackBuilder struct {
	events  [][4]float64
	current Point
}

func newTrackBuilder(start Point) *trackBuilder {
	return &trackBuilder{
		current: start,
		events:  [][4]float64{{0, rounded(start[0]), rounded(start[1]), 0}},
	}
}

func (b *trackBuilder) down() *trackBuilder {
	b.events[len(b.events)-1][3] = 3
	return b
}

func (b *trackBuilder) end() *trackBuilder {
	b.events[len(b.events)-1][3] = 2
	return b
}

func (b *trackBuilder) move(target Point, duration int) *trackBuilder {
	x, y := b.current[0], b.current[1]
	dx, dy := target[0]-x, target[1]-y
	distance := math.Hypot(dx, dy)
	offset := math.Min(0.2, math.Max(0.05, distance*0.3))
	angle1 := uniform(-math.Pi, math.Pi)
	angle2 := uniform(-math.Pi, math.Pi)
	cp1 := Point{x, y}
	cp2 := target
	if distance >= 0.01 {
		cp1 = Point{
			x + dx*uniform(0.3, 0.7) + math.Cos(angle1)*offset*uniform(0.5, 1),
			y + dy*uniform(0.3, 0.7) + math.Sin(angle1)*offset*uniform(0.5, 1),
		}
		cp2 = Point{
			target[0] - dx*uniform(0.3, 0.7) + math.Cos(angle2)*offset*uniform(0.5, 1),
			target[1] - dy*uniform(0.3, 0.7) + math.Sin(angle2)*offset*uniform(0.5, 1),
		}
	}
	base := b.events[len(b.events)-1][0]
	for t := integer(17, 27); duration-t >= 17; t += integer(17, 27) {
		progress := float64(t) / float64(duration)
		q := 3*progress*progress - 2*progress*progress*progress
		inverse := 1 - q
		at := func(axis int) float64 {
			return inverse*inverse*inverse*b.current[axis] +
				3*inverse*inverse*q*cp1[axis] +
				3*inverse*q*q*cp2[axis] +
				q*q*q*target[axis]
		}
		b.events = append(b.events, [4]float64{
			base + float64(t),
			rounded(clip(at(0) + uniform(-0.002, 0.002))),
			rounded(clip(at(1) + uniform(-0.002, 0.002))),
			1,
		})
	}
	b.events = append(b.events, [4]float64{base + float64(duration), rounded(target[0]), rounded(target[1]), 1})
	b.current = target
	return b
}

func (b *trackBuilder) click(delay int) *trackBuilder {
	b.down()
	last := b.events[len(b.events)-1]
	b.events = append(b.events, [4]float64{last[0] + float64(delay), last[1], last[2], 2})
	return b
}

func (b *trackBuilder) build() [][4]float64 {
	if len(b.events) <= 150 {
		return b.events
	}
	first := b.events[0]
	rest := b.events[1:]
	var moves, actions [][4]float64
	for _, event := range rest {
		if event[3] == 1 {
			moves = append(moves, event)
		} else {
			actions = append(actions, event)
		}
	}
	if len(actions) >= 150 {
		selected := append([][4]float64{first}, actions[len(actions)-149:]...)
		return selected
	}
	need := 149 - len(actions)
	end := rest[len(rest)-1][0]
	var near, far [][4]float64
	for _, event := range moves {
		if end-event[0] <= 150 {
			near = append(near, event)
		} else {
			far = append(far, event)
		}
	}
	selected := near
	if len(near) > need {
		selected = near[len(near)-need:]
	}
	if len(selected) < need {
		take := need - len(selected)
		if take > len(far) {
			take = len(far)
		}
		selected = append(append([][4]float64{}, far[len(far)-take:]...), selected...)
	}
	return append(append([][4]float64{first}, selected...), actions...)
}

func origin() Point { return Point{uniform(0.4, 0.6), uniform(0.9, 1)} }

func payload(builder *trackBuilder, height float64) Track {
	points := builder.build()
	start := time.Now().UnixMilli()
	return Track{
		M: 1,
		W: trackWidth,
		H: height,
		S: start,
		E: start + int64(points[len(points)-1][0]),
		P: points,
	}
}

// sequence distributes the total duration across the targets, clicking each.
func sequence(targets []Point, minimum, maximum int) (Track, int) {
	if len(targets) == 0 {
		panic("v4: empty track targets")
	}
	delays := make([]int, len(targets))
	sumDelays := 0
	for i := range targets {
		delays[i] = integer(80, 120)
		sumDelays += delays[i]
	}
	total := integer(minimum, maximum) - sumDelays
	if total < 1 {
		total = 1
	}
	weights := make([]float64, len(targets))
	sumWeights := 0.0
	for i := range targets {
		weights[i] = rand.Float64()
		sumWeights += weights[i]
	}
	moves := make([]int, len(targets))
	used := 0
	for i, weight := range weights {
		moves[i] = int(math.Floor(float64(total) * weight / sumWeights))
		if moves[i] < 1 {
			moves[i] = 1
		}
		used += moves[i]
	}
	last := total - used + moves[len(moves)-1]
	if last < 1 {
		last = 1
	}
	moves[len(moves)-1] = last
	builder := newTrackBuilder(origin())
	for i, target := range targets {
		builder.move(target, moves[i]).click(delays[i])
	}
	track := payload(builder, trackHeight)
	return track, int(track.P[len(track.P)-1][0])
}

func clickTrack(points []Point) (Track, int) {
	targets := make([]Point, 0, len(points)+1)
	for _, point := range points {
		targets = append(targets, jitter(Point{point[0] * answerWidth / trackWidth, point[1] * answerHeight / trackHeight}, 0.02))
	}
	targets = append(targets, jitter(Point{0.5, 0.92}, 0.01))
	return sequence(targets, 2500, 4000)
}

func gridTrack(indices []int, columns int) (Track, int) {
	targets := make([]Point, len(indices))
	for i, index := range indices {
		targets[i] = jitter(Point{
			(float64(index%columns) + 0.5) / float64(columns),
			(float64(index/columns) + 0.5) / float64(columns),
		}, 0.05)
	}
	return sequence(targets, 2500, 4000)
}

func boardTrack(cells [][2]int, gomoku bool) (Track, int) {
	targets := make([]Point, len(cells))
	for i, cell := range cells {
		a, b := float64(cell[0]), float64(cell[1])
		if gomoku {
			targets[i] = jitter(Point{
				(20*b+3)/100 + 20.5/trackWidth,
				(19*a+4)/100 + 20.5/trackHeight,
			}, 0.04)
		} else {
			targets[i] = jitter(Point{
				(33.4*a + 16.7) / 100,
				(33.4*b + 16.7) / 100,
			}, 0.05)
		}
	}
	return sequence(targets, 4500, 9000)
}

// svgTrack builds the single click an SVG grid answer expects. duration is the
// interaction time taken from the visible window of the answer's frame.
func svgTrack(row, col, cols, duration int) (Track, int) {
	if cols < 1 {
		cols = 1
	}
	target := jitter(Point{
		(float64(col) - 0.5) / float64(cols),
		(float64(row) - 0.5) / float64(cols),
	}, 0.01)
	releaseDelay := integer(80, 120)
	move := duration - releaseDelay
	if move < 1 {
		move = 1
	}
	builder := newTrackBuilder(origin())
	builder.move(target, move).click(releaseDelay)
	track := payload(builder, trackHeight)
	return track, int(track.P[len(track.P)-1][0])
}

func slideTrack(offset int) (Track, int) {
	x := uniform(0.1, 0.15)
	duration := integer(600, 1400)
	builder := newTrackBuilder(Point{uniform(0.3, 0.8), uniform(0.85, 0.99)})
	builder.
		move(Point{x, uniform(0.88, 0.9)}, integer(800, 1400)).
		down().
		move(Point{x + float64(offset)/trackWidth, uniform(0.88, 0.9)}, duration).
		end()
	return payload(builder, slideHeight), duration
}

// zipTrack serializes, gzips and base64url-encodes a track.
func zipTrack(track Track) (string, error) {
	data, err := json.Marshal(track)
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	writer.Header.ModTime = time.Now()
	writer.Header.OS = 3
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer.Bytes()), nil
}
