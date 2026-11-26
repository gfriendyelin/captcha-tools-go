// Package voice ports the Lotus VoiceSolver: decode the audio, split it into
// utterances, compute MFCC features and match them against the embedded digit
// centroids. It uses no ONNX models.
package voice

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"sync"

	"github.com/hajimehoshi/go-mp3"
)

const (
	sampleRate   = 16000
	fftSize      = 2048
	hop          = 512
	filters      = 128
	coefficients = 13
)

//go:embed resources/legacy-voice.json
var legacyVoice string

//go:embed resources/v4_voice_20261012-voice.json
var v4Voice string

var templateVersionPattern = regexp.MustCompile(`^[0-9A-Za-z_-]+$`)

var (
	window = buildWindow()
	mel    = buildMelFilterbank()
	dct    = buildDCT()
)

// Text mirrors Lotus VoiceSolver.solve and returns the recognized digits.
func Text(audio []byte, language, templateVersion string) (string, error) {
	if !templateVersionPattern.MatchString(templateVersion) {
		return "", fmt.Errorf("Invalid voice template")
	}
	table, err := loadTemplate(templateVersion)
	if err != nil {
		return "", err
	}
	samples, err := decode(audio)
	if err != nil {
		return "", err
	}
	spans := splitSpeech(samples)
	if len(spans) != 7 {
		return "", fmt.Errorf("Expected seven voice segments, got %d", len(spans))
	}
	segments := make([][]float32, len(spans))
	for i, span := range spans {
		segments[i] = samples[span[0]:span[1]]
	}
	if language == "" {
		prompt := voiceFeature(segments[0])
		if distance(prompt, table.prompts["zho"]) <= distance(prompt, table.prompts["eng"]) {
			language = "zho"
		} else {
			language = "eng"
		}
	}
	centroids, ok := table.centroids[language]
	if !ok || len(centroids) == 0 {
		return "", fmt.Errorf("Unsupported voice language: %s", language)
	}
	text := make([]byte, 0, len(segments)-1)
	for _, segment := range segments[1:] {
		feature := voiceFeature(segment)
		best, bestScore := -1, math.Inf(1)
		for i, centroid := range centroids {
			score := distance(feature, centroid)
			if score < bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 || best >= len(table.y) {
			return "", fmt.Errorf("Voice recognition failed")
		}
		text = append(text, strconv.FormatFloat(table.y[best], 'f', -1, 64)...)
	}
	return string(text), nil
}

// decode mirrors the mpg123 stage: decode to float samples, mix channels to
// mono and resample to 16 kHz with linear interpolation.
func decode(audio []byte) ([]float32, error) {
	decoder, err := mp3.NewDecoder(bytes.NewReader(audio))
	if err != nil {
		return nil, fmt.Errorf("Voice decoding failed")
	}
	sourceRate := decoder.SampleRate()
	if sourceRate <= 0 {
		return nil, fmt.Errorf("Voice decoding failed")
	}
	pcm, err := io.ReadAll(decoder)
	if err != nil || len(pcm) < 4 {
		return nil, fmt.Errorf("Voice decoding failed")
	}
	const channels = 2 // go-mp3 always yields stereo 16-bit frames
	frames := len(pcm) / (2 * channels)
	if frames == 0 {
		return nil, fmt.Errorf("Voice decoding failed")
	}
	length := int(math.Floor(float64(frames) * float64(sampleRate) / float64(sourceRate)))
	out := make([]float32, length)
	for i := 0; i < length; i++ {
		position := float64(i) * float64(sourceRate) / float64(sampleRate)
		index := int(math.Floor(position))
		fraction := position - float64(index)
		var sum float64
		for channel := 0; channel < channels; channel++ {
			sum += interpolate(pcm, frames, channels, index, fraction, channel)
		}
		out[i] = float32(sum / float64(channels))
	}
	return out, nil
}

func interpolate(pcm []byte, frames, channels, index int, fraction float64, channel int) float64 {
	sample := func(i int) float64 {
		if i < 0 || i >= frames {
			return 0
		}
		offset := (i*channels + channel) * 2
		return float64(int16(uint16(pcm[offset])|uint16(pcm[offset+1])<<8)) / 32768.0
	}
	next := index + 1
	if next >= frames {
		next = frames - 1
	}
	return sample(index)*(1-fraction) + sample(next)*fraction
}

// splitSpeech mirrors Lotus splitSpeech and returns [start, end) sample spans.
func splitSpeech(audio []float32) [][2]int {
	if len(audio) == 0 {
		return nil
	}
	const frameSize = 2048
	const step = 160
	frameCount := 1 + len(audio)/step
	energies := make([]float64, frameCount)
	for frame := 0; frame < frameCount; frame++ {
		var power float64
		for i := 0; i < frameSize; i++ {
			value := float64(sampleAt(audio, frame*step+i-frameSize/2))
			power += value * value
		}
		energies[frame] = 20 * math.Log10(math.Max(1e-10, math.Sqrt(power/frameSize)))
	}
	maxEnergy := math.Inf(-1)
	for _, energy := range energies {
		if energy > maxEnergy {
			maxEnergy = energy
		}
	}
	threshold := maxEnergy - 35
	var starts, ends []int
	previous := false
	for i := 0; i <= len(energies); i++ {
		speech := i < len(energies) && energies[i] > threshold
		if speech && !previous {
			starts = append(starts, i*step)
		}
		if !speech && previous {
			ends = append(ends, i*step)
		}
		previous = speech
	}
	type span struct{ start, end int }
	var spans []span
	for i := 0; i < len(starts) && i < len(ends); i++ {
		if len(spans) > 0 && starts[i]-spans[len(spans)-1].end < sampleRate*18/100 {
			spans[len(spans)-1].end = ends[i]
		} else {
			spans = append(spans, span{starts[i], ends[i]})
		}
	}
	result := make([][2]int, 0, len(spans))
	for _, s := range spans {
		if s.end-s.start < sampleRate*5/100 {
			continue
		}
		start, end := s.start, s.end
		if start < 0 {
			start = 0
		}
		if end > len(audio) {
			end = len(audio)
		}
		if end <= start {
			continue
		}
		segment := audio[start:end]
		var power float64
		for _, value := range segment {
			power += float64(value) * float64(value)
		}
		if math.Sqrt(power/float64(len(segment))) >= 0.02 {
			result = append(result, [2]int{start, end})
		}
	}
	return result
}

func sampleAt(audio []float32, index int) float32 {
	if index < 0 || index >= len(audio) {
		return 0
	}
	return audio[index]
}

// voiceFeature mirrors Lotus voiceFeature: 13 MFCCs plus their first and
// second deltas, reduced by averaging each row, for 39 values.
func voiceFeature(audio []float32) []float64 {
	if len(audio) < 2 {
		return make([]float64, 39)
	}
	frames := 1 + len(audio)/hop
	matrix := make([][]float32, coefficients)
	for k := range matrix {
		matrix[k] = make([]float32, frames)
	}
	input := make([]float64, fftSize)
	spectrum := make([]complex128, fftSize)
	power := make([]float64, fftSize/2+1)
	logs := make([]float64, filters)
	for frame := 0; frame < frames; frame++ {
		for i := 0; i < fftSize; i++ {
			input[i] = float64(float32(float64(reflectSample(audio, frame*hop+i-fftSize/2)) * window[i]))
		}
		fft(input, spectrum)
		for i := range power {
			re := float64(float32(real(spectrum[i])))
			im := float64(float32(imag(spectrum[i])))
			power[i] = float64(float32(re*re + im*im))
		}
		for i := 0; i < filters; i++ {
			var sum float64
			weights := mel[i]
			for j := range weights {
				sum += weights[j] * power[j]
			}
			logs[i] = float64(float32(10 * math.Log10(math.Max(1e-10, sum))))
		}
		for k := 0; k < coefficients; k++ {
			var value float64
			for i := 0; i < filters; i++ {
				value += dct[k][i] * logs[i]
			}
			matrix[k][frame] = float32(value)
		}
	}
	first := delta(matrix)
	second := delta(first)
	out := make([]float64, 0, 3*coefficients)
	for _, rows := range [][][]float32{matrix, first, second} {
		for _, row := range rows {
			var sum float64
			for _, value := range row {
				sum += float64(value)
			}
			out = append(out, float64(float32(sum/float64(len(row)))))
		}
	}
	return out
}

func reflectSample(audio []float32, index int) float32 {
	period := 2 * (len(audio) - 1)
	i := ((index % period) + period) % period
	if i >= len(audio) {
		i = period - i
	}
	return audio[i]
}

func buildWindow() []float64 {
	values := make([]float64, fftSize)
	for i := range values {
		values[i] = float64(float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1))))
	}
	return values
}

// buildMelFilterbank mirrors the Lotus bins/mel construction.
func buildMelFilterbank() [][]float64 {
	bins := make([]int, filters+2)
	maxHz := float64(sampleRate) / 2
	for i := 0; i < filters+2; i++ {
		mel := 2595 * math.Log10(1+maxHz/700)
		hz := 700 * (math.Pow(10, mel*float64(i)/float64(filters+1)/2595) - 1)
		bins[i] = int(math.Floor((float64(fftSize+1) * hz) / float64(sampleRate)))
	}
	table := make([][]float64, filters)
	for i := 0; i < filters; i++ {
		weights := make([]float64, fftSize/2+1)
		for j := 0; j < fftSize/2+1; j++ {
			switch {
			case j >= bins[i] && j < bins[i+1]:
				weights[j] = float64(float32(float64(j-bins[i]) / float64(bins[i+1]-bins[i])))
			case j >= bins[i+1] && j < bins[i+2]:
				weights[j] = float64(float32(float64(bins[i+2]-j) / float64(bins[i+2]-bins[i+1])))
			}
		}
		table[i] = weights
	}
	return table
}

func buildDCT() [][]float64 {
	table := make([][]float64, coefficients)
	for k := 0; k < coefficients; k++ {
		row := make([]float64, filters)
		for n := 0; n < filters; n++ {
			value := math.Cos(math.Pi*float64(k)*float64(2*n+1)/(2*float64(filters))) * math.Sqrt(2/float64(filters))
			if k == 0 {
				value /= math.Sqrt(2)
			}
			row[n] = float64(float32(value))
		}
		table[k] = row
	}
	return table
}

// delta mirrors Lotus delta with its explicit float32 rounding.
func delta(matrix [][]float32) [][]float32 {
	out := make([][]float32, len(matrix))
	for i, row := range matrix {
		values := make([]float32, len(row))
		for t := range row {
			var value float32
			for step := 1; step <= 4; step++ {
				high := row[minInt(len(row)-1, t+step)]
				low := row[maxInt(0, t-step)]
				diff := float32(high - low)
				value = float32(float64(value) + float64(step)*float64(diff))
			}
			values[t] = value / 60
		}
		out[i] = values
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// fft computes the forward DFT of a real input into out. Both slices must be
// the same power-of-two length.
func fft(input []float64, out []complex128) {
	n := len(input)
	for i := 0; i < n; i++ {
		out[i] = complex(input[i], 0)
	}
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			out[i], out[j] = out[j], out[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		angle := -2 * math.Pi / float64(length)
		wLength := complex(math.Cos(angle), math.Sin(angle))
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			for j := 0; j < length/2; j++ {
				u := out[i+j]
				v := out[i+j+length/2] * w
				out[i+j] = u + v
				out[i+j+length/2] = u - v
				w *= wLength
			}
		}
	}
}

// template is the parsed voice template.
type template struct {
	y         []float64
	centroids map[string][][]float64
	prompts   map[string][]float64
}

var (
	templateMu    sync.Mutex
	templateCache = map[string]*template{}
)

func templateData(version string) ([]byte, error) {
	switch version {
	case "legacy":
		return []byte(legacyVoice), nil
	case "v4_voice_20261012":
		return []byte(v4Voice), nil
	default:
		return nil, fmt.Errorf("Invalid voice template")
	}
}

func parseTemplate(data []byte) (*template, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode voice template: %w", err)
	}
	table := &template{centroids: map[string][][]float64{}, prompts: map[string][]float64{}}
	if err := json.Unmarshal(raw["y"], &table.y); err != nil {
		return nil, fmt.Errorf("decode voice template y: %w", err)
	}
	var langs []string
	if err := json.Unmarshal(raw["langs"], &langs); err != nil {
		return nil, fmt.Errorf("decode voice template langs: %w", err)
	}
	for _, lang := range langs {
		if value, ok := raw["X_"+lang]; ok {
			var centroids [][]float64
			if err := json.Unmarshal(value, &centroids); err != nil {
				return nil, fmt.Errorf("decode voice template X_%s: %w", lang, err)
			}
			table.centroids[lang] = centroids
		}
		if value, ok := raw["prompt_"+lang]; ok {
			var prompt []float64
			if err := json.Unmarshal(value, &prompt); err != nil {
				return nil, fmt.Errorf("decode voice template prompt_%s: %w", lang, err)
			}
			table.prompts[lang] = prompt
		}
	}
	return table, nil
}

func loadTemplate(version string) (*template, error) {
	templateMu.Lock()
	defer templateMu.Unlock()
	if cached, ok := templateCache[version]; ok {
		return cached, nil
	}
	data, err := templateData(version)
	if err != nil {
		return nil, err
	}
	table, err := parseTemplate(data)
	if err != nil {
		return nil, err
	}
	templateCache[version] = table
	return table, nil
}

func distance(a, b []float64) float64 {
	var sum float64
	for i, x := range a {
		diff := x - b[i]
		sum += diff * diff
	}
	return sum
}
