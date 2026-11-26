package inference

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kafuneri/captcha-tools-go/internal/vision"
	ort "github.com/yalue/onnxruntime_go"
)

//go:embed resources/ocr-characters.json
var embeddedOCRCharacters string

// recTensor builds the recognition tensor: height 48, width derived from the
// widest image and minRatio, BGR channel order, (v/255-0.5)/0.5 normalization,
// zero padding on the right (Lotus recTensor).
func recTensor(images []*vision.RGB, minRatio float64) ([]float32, int, error) {
	if len(images) == 0 {
		return nil, 0, fmt.Errorf("recTensor needs images")
	}
	const height = 48
	width := int(math.Floor(float64(height) * minRatio))
	for _, im := range images {
		if im.H <= 0 {
			continue
		}
		ratio := float64(im.W) / float64(im.H)
		if ratio > float64(width)/float64(height) {
			width = int(math.Floor(float64(height) * ratio))
		}
	}
	values := make([]float32, len(images)*3*height*width)
	for n, im := range images {
		resizedWidth := int(math.Min(float64(width), math.Ceil(float64(height)*float64(im.W)/float64(im.H))))
		if resizedWidth < 1 {
			resizedWidth = 1
		}
		resized, err := vision.Resize(im, resizedWidth, height, vision.Bilinear)
		if err != nil {
			return nil, 0, err
		}
		for c := 0; c < 3; c++ {
			for y := 0; y < height; y++ {
				for x := 0; x < resizedWidth; x++ {
					v := resized.Pix[(y*resizedWidth+x)*3+(2-c)]
					values[n*3*height*width+c*height*width+y*width+x] = (float32(v)/255 - 0.5) / 0.5
				}
			}
		}
	}
	return values, width, nil
}

// textPosterior runs the text-recognize model over prompts+crops in batches of
// 8 and returns one feature vector per input image (Lotus posterior()).
func textPosterior(ctx context.Context, session *Session, images []*vision.RGB) ([]float32, error) {
	var features []float32
	for start := 0; start < len(images); start += 8 {
		end := start + 8
		if end > len(images) {
			end = len(images)
		}
		chunk := images[start:end]
		values, width, err := recTensor(chunk, 2)
		if err != nil {
			return nil, err
		}
		tensor, err := ort.NewTensor(ort.Shape{int64(len(chunk)), 3, 48, int64(width)}, values)
		if err != nil {
			return nil, err
		}
		outputs, err := session.Run(ctx, []ort.Value{tensor})
		if err != nil {
			return nil, err
		}
		if len(outputs) != 1 || len(outputs[0].Float32) == 0 {
			return nil, fmt.Errorf("unexpected text-recognize output")
		}
		shape := outputs[0].Shape
		if len(shape) != 3 || shape[1] < 1 || shape[2] < 1 {
			return nil, fmt.Errorf("invalid text output dimensions %v", shape)
		}
		steps, classes := int(shape[1]), int(shape[2])
		data := outputs[0].Float32
		for n, im := range chunk {
			resizedWidth := int(math.Min(float64(width), math.Ceil(float64(48)*float64(im.W)/float64(im.H))))
			frames := int(math.Min(float64(steps), math.Max(1, math.Round(float64(resizedWidth)/8))))
			best, bestScore := 0, math.Inf(-1)
			for t := 0; t < frames; t++ {
				var score float64
				for c := 1; c < classes; c++ {
					score += float64(data[(n*steps+t)*classes+c])
				}
				if score > bestScore {
					best = t
					bestScore = score
				}
			}
			features = append(features, data[(n*steps+best)*classes:(n*steps+best+1)*classes]...)
		}
	}
	return features, nil
}

// permutations returns all ordered permutations of n indices taken count at a
// time (Lotus permutations()).
func permutations(n, count int) [][]int {
	if count <= 0 {
		return [][]int{{}}
	}
	var result [][]int
	var build func(used []bool, current []int)
	build = func(used []bool, current []int) {
		if len(current) == count {
			result = append(result, append([]int(nil), current...))
			return
		}
		for i := 0; i < n; i++ {
			if !used[i] {
				used[i] = true
				build(used, append(current, i))
				used[i] = false
			}
		}
	}
	build(make([]bool, n), nil)
	return result
}

// WordSolve mirrors Lotus WordSolver.solve.
func (r *Registry) WordSolve(ctx context.Context, image []byte, prompts [][]byte) ([]vision.Point, error) {
	if r == nil {
		return nil, fmt.Errorf("nil registry")
	}
	if len(prompts) == 0 {
		return nil, fmt.Errorf("expected text prompts")
	}
	img, err := vision.RGBFromBytes(image)
	if err != nil {
		return nil, fmt.Errorf("word image: %w", err)
	}
	boxes, err := r.wordDetect(ctx, img, 0.4, 0.45, 5)
	if err != nil {
		return nil, err
	}
	if len(boxes) < len(prompts) {
		return nil, fmt.Errorf("insufficient character detections: %d < %d", len(boxes), len(prompts))
	}
	var promptImages []*vision.RGB
	for _, p := range prompts {
		pr, err := vision.RGBFromBytes(p)
		if err != nil {
			return nil, fmt.Errorf("word prompt: %w", err)
		}
		promptImages = append(promptImages, pr)
	}
	crops := make([]*vision.RGB, len(boxes))
	for i, b := range boxes {
		crops[i], err = vision.CropBlack(img, vision.IntBox{X1: b.X, Y1: b.Y, X2: b.X + b.Width, Y2: b.Y + b.Height})
		if err != nil {
			return nil, err
		}
	}
	lease, err := r.acquire(ctx, "word")
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*WordResource)
	if !ok {
		return nil, fmt.Errorf("word resource has type %T", lease.Resource())
	}
	all := append(append([]*vision.RGB(nil), promptImages...), crops...)
	features, err := textPosterior(ctx, resource.Recognize, all)
	if err != nil {
		return nil, err
	}
	if len(features) == 0 || len(features)%len(all) != 0 {
		return nil, fmt.Errorf("invalid posterior features")
	}
	stride := len(features) / len(all)
	featureAt := func(i int) []float32 { return features[i*stride : (i+1)*stride] }
	candidates := permutations(len(boxes), len(prompts))
	bestOrder, bestScore := []int(nil), math.Inf(-1)
	for _, order := range candidates {
		var score float64
		for i, box := range order {
			score += float64(vision.CosineSimilarity32(featureAt(i), featureAt(len(prompts)+box)))
		}
		if score > bestScore {
			bestScore = score
			bestOrder = order
		}
	}
	if bestOrder == nil {
		return nil, fmt.Errorf("no word assignment")
	}
	points := make([]vision.Point, len(bestOrder))
	for i, box := range bestOrder {
		points[i] = vision.Point{
			X: (float64(boxes[box].X) + float64(boxes[box].Width)/2) / float64(img.W),
			Y: (float64(boxes[box].Y) + float64(boxes[box].Height)/2) / float64(img.H),
		}
	}
	return points, nil
}

// wordDetect runs word-detect or phrase-detect (LibreYOLO9t, xyxy, transposed).
func (r *Registry) wordDetect(ctx context.Context, img *vision.RGB, confidence, iou float32, limit int, ids ...string) ([]vision.SymbolBox, error) {
	id := "word"
	if len(ids) > 0 {
		id = ids[0]
	}
	lease, err := r.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	var detect *Session
	switch id {
	case "word":
		resource, ok := lease.Resource().(*WordResource)
		if !ok {
			return nil, fmt.Errorf("word resource has type %T", lease.Resource())
		}
		detect = resource.Detect
	case "phrase":
		resource, ok := lease.Resource().(*PhraseResource)
		if !ok {
			return nil, fmt.Errorf("phrase resource has type %T", lease.Resource())
		}
		detect = resource.Detect
	default:
		return nil, fmt.Errorf("unknown detector %s", id)
	}
	tensor, err := vision.Letterbox(img, 320, 320)
	if err != nil {
		return nil, err
	}
	ortTensor, err := ort.NewTensor(ort.Shape{1, 3, 320, 320}, tensor.Values)
	if err != nil {
		return nil, err
	}
	outputs, err := detect.Run(ctx, []ort.Value{ortTensor})
	if err != nil {
		return nil, err
	}
	if len(outputs) != 1 || len(outputs[0].Float32) == 0 {
		return nil, fmt.Errorf("unexpected %s-detect output", id)
	}
	shape := outputs[0].Shape
	if len(shape) != 3 {
		return nil, fmt.Errorf("invalid %s-detect shape %v", id, shape)
	}
	transposed := shape[1] < shape[2]
	channels, count := int(shape[1]), int(shape[2])
	if !transposed {
		channels, count = int(shape[2]), int(shape[1])
	}
	return vision.DecodeDetect(outputs[0].Float32, channels, count, transposed, "xyxy", tensor.Scale, tensor.PadX, tensor.PadY, img.W, img.H, confidence, iou, limit)
}

// PhraseDictionary mirrors Lotus PhraseDictionary.
type PhraseDictionary struct {
	Table   map[string]string   `json:"table"`
	Tried   map[string][]string `json:"tried"`
	Pending map[string]string   `json:"pending"`
}

var hanCharPattern = regexp.MustCompile(`[一-鿿]`)

// PhraseSolve mirrors Lotus PhraseSolver.solve.
func (r *Registry) PhraseSolve(ctx context.Context, image []byte, withLexicon bool) ([]vision.Point, error) {
	if r == nil {
		return nil, fmt.Errorf("nil registry")
	}
	img, err := vision.RGBFromBytes(image)
	if err != nil {
		return nil, fmt.Errorf("phrase image: %w", err)
	}
	boxes, err := r.wordDetect(ctx, img, 0.7, 0.45, 0, "phrase")
	if err != nil {
		return nil, err
	}
	if len(boxes) != 4 {
		return nil, fmt.Errorf("phrase must have four detections, got %d", len(boxes))
	}
	crops := make([]*vision.RGB, len(boxes))
	for i, b := range boxes {
		crops[i], err = vision.CropBlack(img, vision.IntBox{X1: b.X, Y1: b.Y, X2: b.X + b.Width, Y2: b.Y + b.Height})
		if err != nil {
			return nil, err
		}
	}
	lease, err := r.acquire(ctx, "phrase")
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	resource, ok := lease.Resource().(*PhraseResource)
	if !ok {
		return nil, fmt.Errorf("phrase resource has type %T", lease.Resource())
	}
	chars, err := r.phraseRecognize(ctx, resource, crops)
	if err != nil {
		return nil, err
	}
	for _, c := range chars {
		if c == "" {
			return nil, fmt.Errorf("phrase recognition failed")
		}
	}
	order, err := r.phraseOrder(ctx, resource, chars, withLexicon)
	if err != nil {
		return nil, err
	}
	points := make([]vision.Point, len(order))
	for i, box := range order {
		points[i] = vision.Point{
			X: (float64(boxes[box].X) + float64(boxes[box].Width)/2) / float64(img.W),
			Y: (float64(boxes[box].Y) + float64(boxes[box].Height)/2) / float64(img.H),
		}
	}
	return points, nil
}

// phraseRecognize mirrors Lotus PhraseSolver.recognize.
func (r *Registry) phraseRecognize(ctx context.Context, resource *PhraseResource, crops []*vision.RGB) ([]string, error) {
	values, _, err := recTensor(crops, 320.0/48.0)
	if err != nil {
		return nil, err
	}
	tensor, err := ort.NewTensor(ort.Shape{int64(len(crops)), 3, 48, int64(widthOf(crops, 320.0/48.0))}, values)
	if err != nil {
		return nil, err
	}
	outputs, err := resource.Recognize.Run(ctx, []ort.Value{tensor})
	if err != nil {
		return nil, err
	}
	if len(outputs) != 1 || len(outputs[0].Float32) == 0 {
		return nil, fmt.Errorf("unexpected phrase-recognize output")
	}
	shape := outputs[0].Shape
	if len(shape) != 3 || shape[1] < 1 || shape[2] < 1 {
		return nil, fmt.Errorf("invalid phrase output dimensions %v", shape)
	}
	steps, classes := int(shape[1]), int(shape[2])
	var dict struct {
		Character string `json:"character"`
	}
	if err := json.Unmarshal([]byte(embeddedOCRCharacters), &dict); err != nil {
		return nil, fmt.Errorf("decode ocr-characters: %w", err)
	}
	characters := append([]string{"blank"}, strings.Split(dict.Character, "\n")...)
	characters = append(characters, " ")
	data := outputs[0].Float32
	chars := make([]string, len(crops))
	for n := range crops {
		previous := -1
		var text strings.Builder
		for t := 0; t < steps; t++ {
			offset := (n*steps + t) * classes
			best := 0
			for c := 1; c < classes; c++ {
				if data[offset+c] > data[offset+best] {
					best = c
				}
			}
			if best != 0 && best != previous {
				if best < len(characters) {
					text.WriteString(characters[best])
				}
			}
			previous = best
		}
		m := hanCharPattern.FindString(text.String())
		chars[n] = m
	}
	return chars, nil
}

func widthOf(images []*vision.RGB, minRatio float64) int {
	const height = 48
	width := int(math.Floor(float64(height) * minRatio))
	for _, im := range images {
		if im.H > 0 {
			if ratio := float64(im.W) / float64(im.H); ratio > float64(width)/float64(height) {
				width = int(math.Floor(float64(height) * ratio))
			}
		}
	}
	return width
}

// phraseOrder mirrors Lotus PhraseSolver.order.
func (r *Registry) phraseOrder(ctx context.Context, resource *PhraseResource, chars []string, withLexicon bool) ([]int, error) {
	dict, err := r.loadPhraseDictionary(ctx)
	if err != nil {
		return nil, err
	}
	key := strings.Join(sortedChars(chars), "")
	known := indices(chars, dict.Table[key])
	if known != nil && uniqueChars(chars) {
		return known, nil
	}
	if withLexicon {
		lexicon, err := r.loadPhraseLexicon(ctx)
		if err != nil {
			return nil, err
		}
		phrase := lexiconUnique(lexicon, chars, dict.Tried[key])
		if phrase != "" {
			if lexical := indices(chars, phrase); lexical != nil {
				return lexical, nil
			}
		}
	}
	if resource.Order == nil {
		return identityOrder(len(chars)), nil
	}
	vocab, err := r.loadPhraseTokens(ctx)
	if err != nil {
		return nil, err
	}
	return r.phraseOrderModel(ctx, resource.Order, dict, vocab, chars)
}

func sortedChars(chars []string) []string {
	out := append([]string(nil), chars...)
	sort.Strings(out)
	return out
}

func uniqueChars(chars []string) bool {
	seen := map[string]bool{}
	for _, c := range chars {
		if seen[c] {
			return false
		}
		seen[c] = true
	}
	return true
}

func identityOrder(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// indices maps each character of phrase to a distinct position in chars.
func indices(chars []string, phrase string) []int {
	if len(phrase) != len(chars) {
		return nil
	}
	used := make([]bool, len(chars))
	var out []int
	for _, char := range phrase {
		idx := -1
		for i, c := range chars {
			if c == string(char) && !used[i] {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil
		}
		used[idx] = true
		out = append(out, idx)
	}
	return out
}

// lexiconUnique mirrors PhraseLexicon.unique.
func lexiconUnique(lexicon map[string][]string, chars []string, rejected []string) string {
	if len(chars) != 4 || !uniqueChars(chars) {
		return ""
	}
	key := strings.Join(sortedChars(chars), "")
	rejectedSet := map[string]bool{}
	for _, w := range rejected {
		rejectedSet[w] = true
	}
	var candidates []string
	for _, word := range lexicon[key] {
		if !rejectedSet[word] {
			candidates = append(candidates, word)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

// phraseOrderModel runs the phrase-order transformer over all permutations.
func (r *Registry) phraseOrderModel(ctx context.Context, orderModel *Session, dict *PhraseDictionary, vocab map[string]int64, chars []string) ([]int, error) {
	ids := make([]int64, 0, len(chars)+2)
	cls, clsOK := vocab["[CLS]"]
	sep, sepOK := vocab["[SEP]"]
	unk, unkOK := vocab["[UNK]"]
	if !clsOK || !sepOK || !unkOK {
		return nil, fmt.Errorf("phrase-tokens missing special tokens")
	}
	ids = append(ids, cls)
	for _, char := range chars {
		if id, ok := vocab[char]; ok {
			ids = append(ids, id)
		} else {
			ids = append(ids, unk)
		}
	}
	ids = append(ids, sep)
	attention := make([]int64, len(ids))
	for i := range attention {
		attention[i] = 1
	}
	t1, err := ort.NewTensor(ort.Shape{1, int64(len(ids))}, ids)
	if err != nil {
		return nil, err
	}
	t2, err := ort.NewTensor(ort.Shape{1, int64(len(ids))}, attention)
	if err != nil {
		return nil, err
	}
	outputs, err := orderModel.Run(ctx, []ort.Value{t1, t2})
	if err != nil {
		return nil, err
	}
	if len(outputs) != 1 || len(outputs[0].Float32) == 0 {
		return nil, fmt.Errorf("unexpected phrase-order output")
	}
	data := outputs[0].Float32
	shape := outputs[0].Shape
	classes := int(shape[len(shape)-1])
	if classes < 1 {
		return nil, fmt.Errorf("invalid phrase-order classes %v", shape)
	}
	key := strings.Join(sortedChars(chars), "")
	bad := map[string]bool{}
	for _, p := range dict.Tried[key] {
		if idx := indices(chars, p); idx != nil {
			bad[fmt.Sprint(idx)] = true
		}
	}
	type candidate struct {
		order []int
		score float64
	}
	var candidates []candidate
	for _, order := range permutations(len(chars), len(chars)) {
		if bad[fmt.Sprint(order)] {
			continue
		}
		var score float64
		for position, char := range order {
			score += float64(data[(char+1)*classes+position])
		}
		candidates = append(candidates, candidate{order: order, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	hint := indices(chars, dict.Pending[key])
	if hint != nil {
		for _, c := range candidates {
			if equalInts(c.order, hint) {
				return hint, nil
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0].order, nil
	}
	return identityOrder(len(chars)), nil
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// loadPhraseDictionary reads phrase-dictionary.json from the phrase model dir.
func (r *Registry) loadPhraseDictionary(ctx context.Context) (*PhraseDictionary, error) {
	dir, err := r.phraseDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "phrase-dictionary.json"))
	if err != nil {
		return nil, err
	}
	var dict PhraseDictionary
	if err := json.Unmarshal(data, &dict); err != nil {
		return nil, err
	}
	return &dict, nil
}

// loadPhraseLexicon reads phrase-lexicon.txt from the lexicon dir.
func (r *Registry) loadPhraseLexicon(ctx context.Context) (map[string][]string, error) {
	dir, err := r.lexiconDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "phrase-lexicon.txt"))
	if err != nil {
		return nil, err
	}
	lexicon := map[string][]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		word := fields[0]
		if !isFourHan(word) {
			continue
		}
		key := strings.Join(sortedChars([]string{string([]rune(word)[0]), string([]rune(word)[1]), string([]rune(word)[2]), string([]rune(word)[3])}), "")
		lexicon[key] = append(lexicon[key], word)
	}
	return lexicon, nil
}

func isFourHan(word string) bool {
	if len([]rune(word)) != 4 {
		return false
	}
	for _, rr := range word {
		if rr < 0x4e00 || rr > 0x9fff {
			return false
		}
	}
	return true
}

// loadPhraseTokens reads phrase-tokens.json vocab.
func (r *Registry) loadPhraseTokens(ctx context.Context) (map[string]int64, error) {
	dir, err := r.phraseDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "phrase-tokens.json"))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Model struct {
			Vocab map[string]int64 `json:"vocab"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Model.Vocab, nil
}

// phraseDir returns the phrase model directory.
func (r *Registry) phraseDir() (string, error) {
	return filepath.Join(r.modelDir, "v4-phrase-core"), nil
}

// lexiconDir returns the lexicon directory.
func (r *Registry) lexiconDir() (string, error) {
	return filepath.Join(r.modelDir, "v4-phrase-lexicon"), nil
}
