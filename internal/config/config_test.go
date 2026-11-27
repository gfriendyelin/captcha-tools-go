package config

import (
	"testing"
	"time"
)

func env(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestDefaults(t *testing.T) {
	cfg, err := FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeAll || cfg.Port != 9645 || !cfg.UsePDL || !cfg.UseDFINE || !cfg.UseMulti {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.WorkerIdleTimeout != time.Minute || len(cfg.PreloadV3) != 2 || len(cfg.PreloadV4) != 0 || !cfg.DINORandomOffset {
		t.Fatalf("unexpected lifecycle defaults: %+v", cfg)
	}
	if cfg.WorkDir != "img_2_val" || cfg.SaveDir != "img_saved" {
		t.Fatalf("unexpected storage defaults: %+v", cfg)
	}
}

func TestV3ModePreloadsPDLAndDFINE(t *testing.T) {
	cfg, err := FromEnv(env(map[string]string{"MODE": "v3"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeV3 || len(cfg.Preload) != 2 || cfg.Preload[0] != "pdl" || cfg.Preload[1] != "dfine" {
		t.Fatalf("unexpected v3 defaults: %+v", cfg)
	}
}

func TestV4ModeDoesNotPreload(t *testing.T) {
	cfg, err := FromEnv(env(map[string]string{"MODE": "v4"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeV4 || len(cfg.Preload) != 0 {
		t.Fatalf("unexpected v4 defaults: %+v", cfg)
	}
}

func TestCompatibilityEnvironment(t *testing.T) {
	cfg, err := FromEnv(env(map[string]string{
		"use_pdl": "0", "use_dfine": "1", "use_multi": "0", "LOG_LEVEL": "warning",
		"MODEL_IDLE_TIMEOUT": "1500ms", "MODEL_PRELOAD": "pdl,multi", "DINO_RANDOM_OFFSET": "0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UsePDL || !cfg.UseDFINE || cfg.UseMulti || cfg.LogLevel != "WARN" {
		t.Fatalf("unexpected flags: %+v", cfg)
	}
	if cfg.WorkerIdleTimeout != 1500*time.Millisecond || len(cfg.Preload) != 2 || cfg.DINORandomOffset {
		t.Fatalf("unexpected values: %+v", cfg)
	}
}

func TestZeroIdleTimeoutDisablesWorkerExit(t *testing.T) {
	for _, value := range []string{"0", "0s"} {
		cfg, err := FromEnv(env(map[string]string{"WORKER_IDLE_TIMEOUT": value}))
		if err != nil {
			t.Fatalf("WORKER_IDLE_TIMEOUT=%q: %v", value, err)
		}
		if cfg.WorkerIdleTimeout != 0 {
			t.Fatalf("WORKER_IDLE_TIMEOUT=%q: got %v, want 0", value, cfg.WorkerIdleTimeout)
		}
	}
}

func TestModeSpecificPreloadValidation(t *testing.T) {
	if _, err := FromEnv(env(map[string]string{"MODE": "v3", "MODEL_PRELOAD": "direction"})); err == nil {
		t.Fatal("v3 accepted a V4 model group")
	}
	if _, err := FromEnv(env(map[string]string{"MODE": "v4", "MODEL_PRELOAD": "dfine"})); err == nil {
		t.Fatal("v4 accepted a V3 model group")
	}
	cfg, err := FromEnv(env(map[string]string{"MODE": "v4", "MODEL_PRELOAD": "grid,direction"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Preload) != 2 {
		t.Fatalf("unexpected v4 preload: %v", cfg.Preload)
	}
	all, err := FromEnv(env(map[string]string{"MODE": "all", "MODEL_PRELOAD": "pdl,dfine,grid,direction"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(all.PreloadV3) != 3 || len(all.PreloadV4) != 2 {
		t.Fatalf("unexpected split preload: v3=%v v4=%v", all.PreloadV3, all.PreloadV4)
	}
}

func TestInvalidEnvironment(t *testing.T) {
	for _, values := range []map[string]string{
		{"WORKER_IDLE_TIMEOUT": "10"}, {"WORKER_IDLE_TIMEOUT": "-1s"}, {"use_pdl": "maybe"},
		{"PORT": "0"}, {"MODEL_PRELOAD": "unknown"}, {"HTTP_TIMEOUT": "0s"}, {"MODE": "v5"},
	} {
		if _, err := FromEnv(env(values)); err == nil {
			t.Errorf("FromEnv(%v) unexpectedly succeeded", values)
		}
	}
}
