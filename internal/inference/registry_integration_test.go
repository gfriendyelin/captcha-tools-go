package inference

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kafuneri/captcha-tools-go/internal/config"
)

func integrationRegistry(t *testing.T) *Registry {
	t.Helper()
	if os.Getenv("CAPTCHA_INTEGRATION") == "" {
		t.Skip("set CAPTCHA_INTEGRATION=1 to run real ONNX lifecycle tests")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewRegistry(config.Config{Mode: config.ModeV3, ModelDir: filepath.Join(root, "model"),
		ONNXRuntimeLibrary: filepath.Join(root, ".onnxruntime", "onnxruntime.dll"),
		UsePDL:             true, UseDFINE: true, UseMulti: true, UseGrid: true, DINORandomOffset: false}, logger)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = registry.Close(ctx)
		_ = DestroyEnvironment()
	})
	return registry
}

func TestRealONNXLazyReloadAndInflightProtection(t *testing.T) {
	registry := integrationRegistry(t)
	if registry.pdl.Loaded() || registry.dfine.Loaded() || registry.multi.Loaded() {
		t.Fatal("models loaded at construction")
	}
	lease, err := registry.acquire(context.Background(), "pdl")
	if err != nil {
		t.Fatal(err)
	}
	if !registry.pdl.Loaded() || registry.dfine.Loaded() || registry.multi.Loaded() {
		t.Fatal("unexpected groups loaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = registry.UnloadModel(ctx, "pdl")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unload with in-flight lease = %v", err)
	}
	lease.Release()
	unloadCtx, unloadCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer unloadCancel()
	if _, err := registry.UnloadModel(unloadCtx, "pdl"); err != nil {
		t.Fatal(err)
	}
	if registry.pdl.Loaded() {
		t.Fatal("PDL did not unload")
	}
	data, err := os.ReadFile("../../testdata/sample-nine.jpg")
	if err != nil {
		t.Fatal(err)
	}
	result, err := registry.Nine(context.Background(), data, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{2, 2}, {3, 2}, {3, 3}}
	if len(result.Cells) != len(want) {
		t.Fatalf("cells=%v, want %v", result.Cells, want)
	}
	for i := range want {
		if result.Cells[i] != want[i] {
			t.Fatalf("cells=%v, want %v", result.Cells, want)
		}
	}
	if !registry.pdl.Loaded() || registry.dfine.Loaded() || registry.multi.Loaded() {
		t.Fatal("reload loaded wrong groups")
	}
}
