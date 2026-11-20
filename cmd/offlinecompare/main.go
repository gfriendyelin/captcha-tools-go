package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kafuneri/captcha-tools-go/internal/config"
	"github.com/kafuneri/captcha-tools-go/internal/inference"
	"github.com/kafuneri/captcha-tools-go/internal/server"
)

type baseline struct {
	Group   string           `json:"group"`
	Records []baselineRecord `json:"records"`
}
type baselineRecord struct {
	File            string              `json:"file"`
	Cells           [][2]int            `json:"cells"`
	SimilarityCells [][2]int            `json:"similarity_cells"`
	Centers         [][2]float64        `json:"centers"`
	YOLO            map[string][][4]int `json:"yolo"`
	Error           string              `json:"error"`
}
type comparison struct {
	File           string   `json:"file"`
	Pass           bool     `json:"pass"`
	Duration       string   `json:"duration"`
	ExpectedError  string   `json:"expected_error,omitempty"`
	ActualError    string   `json:"actual_error,omitempty"`
	Differences    []string `json:"differences,omitempty"`
	MaxCenterDelta float64  `json:"max_center_delta"`
}
type report struct {
	Group       string       `json:"group"`
	Records     int          `json:"records"`
	Passed      int          `json:"passed"`
	Comparisons []comparison `json:"comparisons"`
}

func main() {
	group := flag.String("group", "", "pdl, dfine, or multi")
	baselinePath := flag.String("baseline", "", "Python baseline JSON")
	images := flag.String("images", "", "directory containing original JPG samples")
	modelDir := flag.String("model-dir", "model", "ONNX model directory")
	library := flag.String("ort", ".onnxruntime/onnxruntime.dll", "ONNX Runtime shared library")
	output := flag.String("output", "", "write JSON report to this path")
	flag.Parse()
	if err := run(*group, *baselinePath, *images, *modelDir, *library, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(group, baselinePath, images, modelDir, library, output string) error {
	if group != "pdl" && group != "dfine" && group != "multi" {
		return fmt.Errorf("invalid -group %q", group)
	}
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		return err
	}
	var expected baseline
	if err := json.Unmarshal(data, &expected); err != nil {
		return err
	}
	if expected.Group != group {
		return fmt.Errorf("baseline group %q does not match %q", expected.Group, group)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	registry := inference.NewRegistry(config.Config{ModelDir: modelDir, ONNXRuntimeLibrary: library,
		UsePDL: true, UseDFINE: true, UseMulti: true, DINORandomOffset: false}, logger)
	result := report{Group: group, Records: len(expected.Records)}
	for _, record := range expected.Records {
		item := compareOne(registry, images, record, group)
		result.Comparisons = append(result.Comparisons, item)
		if item.Pass {
			result.Passed++
		}
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	closeErr := registry.Close(closeCtx)
	cancel()
	if closeErr != nil {
		return closeErr
	}
	if err := inference.DestroyEnvironment(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, 10)
	if output != "" {
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(output, encoded, 0o644); err != nil {
			return err
		}
	}
	_, _ = os.Stdout.Write(encoded)
	if result.Passed != result.Records {
		return fmt.Errorf("offline comparison failed: %d/%d passed", result.Passed, result.Records)
	}
	return nil
}

func compareOne(registry *inference.Registry, images string, expected baselineRecord, group string) comparison {
	started := time.Now()
	item := comparison{File: expected.File, ExpectedError: expected.Error}
	data, err := os.ReadFile(filepath.Join(images, expected.File))
	if err != nil {
		item.ActualError = err.Error()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		switch group {
		case "pdl":
			var actual server.NineResult
			actual, err = registry.Nine(ctx, data, true, nil)
			compareCellSet(&item, expected.Cells, actual.Cells, "PDL")
			if err == nil {
				legacy, legacyErr := registry.Nine(ctx, data, false, nil)
				err = legacyErr
				compareCellOrder(&item, expected.SimilarityCells, legacy.Cells, "legacy")
			}
		case "dfine":
			var actual server.IconResult
			actual, err = registry.DFINE(ctx, data, false)
			compareCenters(&item, expected.Centers, actual.Points)
		case "multi":
			var actual server.IconResult
			actual, err = registry.Multi(ctx, data, false)
			compareCenters(&item, expected.Centers, actual.Points)
			compareBoxes(&item, expected.YOLO["top"], actual.YOLOTop, "top")
			compareBoxes(&item, expected.YOLO["bottom"], actual.YOLOBottom, "bottom")
			compareBoxes(&item, expectedSequence(expected), actual.Sequence, "matched sequence")
		}
		cancel()
	}
	item.Duration = time.Since(started).String()
	if err != nil {
		item.ActualError = err.Error()
	}
	if (expected.Error == "") != (item.ActualError == "") {
		item.Differences = append(item.Differences, fmt.Sprintf("error presence: Python %q, Go %q", expected.Error, item.ActualError))
	}
	item.Pass = len(item.Differences) == 0
	return item
}

func compareCellSet(item *comparison, expected [][2]int, actual [][2]int, label string) {
	expected = canonicalCells(expected)
	actual = canonicalCells(actual)
	compareCellOrder(item, expected, actual, label+" set")
}
func canonicalCells(cells [][2]int) [][2]int {
	out := append([][2]int(nil), cells...)
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] == out[j][0] {
			return out[i][1] < out[j][1]
		}
		return out[i][0] < out[j][0]
	})
	return out
}
func compareCellOrder(item *comparison, expected, actual [][2]int, label string) {
	if len(expected) != len(actual) {
		item.Differences = append(item.Differences, fmt.Sprintf("%s length: Python %v, Go %v", label, expected, actual))
		return
	}
	for i := range expected {
		if expected[i] != actual[i] {
			item.Differences = append(item.Differences, fmt.Sprintf("%s index %d: Python %v, Go %v", label, i, expected[i], actual[i]))
		}
	}
}
func compareCenters(item *comparison, expected [][2]float64, actual [][2]float64) {
	if len(expected) != len(actual) {
		item.Differences = append(item.Differences, fmt.Sprintf("point count: Python %v, Go %v", expected, actual))
		return
	}
	for i := range expected {
		dx := math.Abs(expected[i][0] - actual[i][0])
		dy := math.Abs(expected[i][1] - actual[i][1])
		delta := math.Max(dx, dy)
		if delta > item.MaxCenterDelta {
			item.MaxCenterDelta = delta
		}
		if delta > 2 {
			item.Differences = append(item.Differences, fmt.Sprintf("point %d delta=(%.6g,%.6g), Python=%v Go=%v", i, dx, dy, expected[i], actual[i]))
		}
	}
}
func compareBoxes(item *comparison, expected, actual [][4]int, label string) {
	if len(expected) != len(actual) {
		item.Differences = append(item.Differences, fmt.Sprintf("%s box count: Python %v, Go %v", label, expected, actual))
		return
	}
	for i := range expected {
		if expected[i] != actual[i] {
			item.Differences = append(item.Differences, fmt.Sprintf("%s box %d: Python %v, Go %v", label, i, expected[i], actual[i]))
		}
	}
}
func expectedSequence(record baselineRecord) [][4]int {
	var result [][4]int
	remaining := append([][4]int(nil), record.YOLO["top"]...)
	for _, center := range record.Centers {
		for i, box := range remaining {
			x := float64(box[0]+box[2]) / 2
			y := float64(box[1]+box[3]) / 2
			if math.Abs(x-center[0]) <= 0.51 && math.Abs(y-center[1]) <= 0.51 {
				result = append(result, box)
				remaining = append(remaining[:i], remaining[i+1:]...)
				break
			}
		}
	}
	return result
}
