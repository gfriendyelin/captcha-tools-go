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
	if cfg.Port != 9645 || !cfg.UsePDL || !cfg.UseDFINE || !cfg.UseMulti {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.ModelIdleTimeout != 10*time.Minute || len(cfg.Preload) != 0 || !cfg.DINORandomOffset {
		t.Fatalf("unexpected lifecycle defaults: %+v", cfg)
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
	if cfg.ModelIdleTimeout != 1500*time.Millisecond || len(cfg.Preload) != 2 || cfg.DINORandomOffset {
		t.Fatalf("unexpected values: %+v", cfg)
	}
}

// 0 must stay accepted: the README and model.Group treat it as "never unload".
func TestZeroIdleTimeoutDisablesAutoUnload(t *testing.T) {
	for _, value := range []string{"0", "0s"} {
		cfg, err := FromEnv(env(map[string]string{"MODEL_IDLE_TIMEOUT": value}))
		if err != nil {
			t.Fatalf("MODEL_IDLE_TIMEOUT=%q: %v", value, err)
		}
		if cfg.ModelIdleTimeout != 0 {
			t.Fatalf("MODEL_IDLE_TIMEOUT=%q: got %v, want 0", value, cfg.ModelIdleTimeout)
		}
	}
}

func TestInvalidEnvironment(t *testing.T) {
	for _, values := range []map[string]string{
		{"MODEL_IDLE_TIMEOUT": "10"}, {"MODEL_IDLE_TIMEOUT": "-1s"}, {"use_pdl": "maybe"},
		{"PORT": "0"}, {"MODEL_PRELOAD": "unknown"}, {"HTTP_TIMEOUT": "0s"},
	} {
		if _, err := FromEnv(env(values)); err == nil {
			t.Errorf("FromEnv(%v) unexpectedly succeeded", values)
		}
	}
}
