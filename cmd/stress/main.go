package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/kafuneri/captcha-tools-go/internal/config"
	"github.com/kafuneri/captcha-tools-go/internal/inference"
	"github.com/kafuneri/captcha-tools-go/internal/procmon"
	"github.com/kafuneri/captcha-tools-go/internal/server"
)

type sample struct {
	Cycle          int    `json:"cycle"`
	RSSBytes       uint64 `json:"rss_bytes"`
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes   uint64 `json:"heap_sys_bytes"`
}
type groupReport struct {
	Group      string   `json:"group"`
	Cycles     int      `json:"cycles"`
	Errors     []string `json:"errors,omitempty"`
	FirstCall  string   `json:"first_call"`
	MeanReload string   `json:"mean_reload"`
	MaxReload  string   `json:"max_reload"`
	FirstRSS   uint64   `json:"first_rss_bytes"`
	LastRSS    uint64   `json:"last_rss_bytes"`
	MinRSS     uint64   `json:"min_rss_bytes"`
	MaxRSS     uint64   `json:"max_rss_bytes"`
	Samples    []sample `json:"samples"`
}
type report struct {
	Groups     []groupReport `json:"groups"`
	InitialRSS uint64        `json:"initial_rss_bytes"`
	FinalRSS   uint64        `json:"final_rss_bytes"`
	Note       string        `json:"note"`
}

func main() {
	cycles := flag.Int("cycles", 12, "inference/unload cycles per model group")
	only := flag.String("only", "", "run only pdl, dfine, or multi")
	modelDir := flag.String("model-dir", "model", "model directory")
	library := flag.String("ort", "", "ONNX Runtime shared library")
	imagePath := flag.String("sample", "testdata/sample-nine.jpg", "sample image")
	output := flag.String("output", "testdata/memory-report.json", "report output")
	flag.Parse()
	if err := run(*cycles, *modelDir, *library, *imagePath, *output, *only); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cycles int, modelDir, library, imagePath, output, only string) error {
	if cycles < 1 {
		return fmt.Errorf("cycles must be positive")
	}
	if library == "" {
		if goruntime.GOOS == "windows" {
			library = ".onnxruntime/onnxruntime.dll"
		} else {
			library = ".onnxruntime/libonnxruntime.so"
		}
	}
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	registry := inference.NewRegistry(config.Config{ModelDir: modelDir, ONNXRuntimeLibrary: library,
		UsePDL: true, UseDFINE: true, UseMulti: true, DINORandomOffset: false}, logger)
	goruntime.GC()
	initial, _ := procmon.RSS()
	result := report{InitialRSS: initial, Note: "RSS includes native allocator/arena retention; growth alone is not classified as a leak."}
	var allErrors []error
	groups := []string{"pdl", "dfine", "multi"}
	if only != "" {
		groups = []string{only}
	}
	for _, group := range groups {
		report := stressGroup(registry, group, data, cycles)
		result.Groups = append(result.Groups, report)
		for _, message := range report.Errors {
			allErrors = append(allErrors, fmt.Errorf("%s: %s", group, message))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	allErrors = append(allErrors, registry.Close(ctx))
	cancel()
	goruntime.GC()
	result.FinalRSS, _ = procmon.RSS()
	allErrors = append(allErrors, inference.DestroyEnvironment())
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, 10)
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(output, encoded, 0o644); err != nil {
		return err
	}
	_, _ = os.Stdout.Write(encoded)
	return errors.Join(allErrors...)
}

func stressGroup(registry *inference.Registry, group string, data []byte, cycles int) groupReport {
	report := groupReport{Group: group, Cycles: cycles, MinRSS: math.MaxUint64}
	var reloadTotal time.Duration
	for cycle := 0; cycle < cycles; cycle++ {
		started := time.Now()
		var err error
		switch group {
		case "pdl":
			var result server.NineResult
			result, err = registry.Nine(context.Background(), data, true, nil)
			if err == nil && len(result.Cells) != 3 {
				err = fmt.Errorf("unexpected cell count %d", len(result.Cells))
			}
		case "dfine":
			var result server.IconResult
			result, err = registry.DFINE(context.Background(), data, false)
			if err == nil && len(result.Points) != 0 {
				err = fmt.Errorf("unexpected point count %d", len(result.Points))
			}
		case "multi":
			var result server.IconResult
			result, err = registry.Multi(context.Background(), data, false)
			if err == nil && len(result.Points) != 1 {
				err = fmt.Errorf("unexpected point count %d", len(result.Points))
			}
		}
		duration := time.Since(started)
		if cycle == 0 {
			report.FirstCall = duration.String()
		} else {
			reloadTotal += duration
		}
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
		}
		if _, unloadErr := registry.UnloadModel(context.Background(), group); unloadErr != nil {
			report.Errors = append(report.Errors, unloadErr.Error())
		}
		if cycle > 0 {
			report.MaxReload = maxDuration(report.MaxReload, duration)
		}
		rss, _ := procmon.RSS()
		var memory goruntime.MemStats
		goruntime.ReadMemStats(&memory)
		report.Samples = append(report.Samples, sample{Cycle: cycle + 1, RSSBytes: rss, HeapAllocBytes: memory.HeapAlloc, HeapSysBytes: memory.HeapSys})
		if report.FirstRSS == 0 {
			report.FirstRSS = rss
		}
		report.LastRSS = rss
		if rss < report.MinRSS {
			report.MinRSS = rss
		}
		if rss > report.MaxRSS {
			report.MaxRSS = rss
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cycles > 1 {
		report.MeanReload = (reloadTotal / time.Duration(cycles-1)).String()
	}
	return report
}

func maxDuration(current string, candidate time.Duration) string {
	parsed, _ := time.ParseDuration(current)
	if candidate > parsed {
		return candidate.String()
	}
	return current
}
