package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const DefaultPort = 9645

type Config struct {
	Port               int
	ModelDir           string
	WorkDir            string
	SaveDir            string
	LogDir             string
	LogLevel           string
	UsePDL             bool
	UseDFINE           bool
	UseMulti           bool
	Preload            []string
	ModelIdleTimeout   time.Duration
	DINORandomOffset   bool
	HTTPTimeout        time.Duration
	ShutdownTimeout    time.Duration
	MaxImageBytes      int64
	ONNXRuntimeLibrary string
}

func Load() (Config, error) { return FromEnv(os.LookupEnv) }

func FromEnv(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		Port: DefaultPort, ModelDir: "model", WorkDir: "img_2_val", SaveDir: "img_saved",
		LogDir: "logs", LogLevel: "INFO", UsePDL: true, UseDFINE: true, UseMulti: true,
		ModelIdleTimeout: 10 * time.Minute, DINORandomOffset: true,
		HTTPTimeout: 30 * time.Second, ShutdownTimeout: 30 * time.Second,
		MaxImageBytes: 32 << 20,
	}
	get := func(keys ...string) (string, bool) {
		for _, key := range keys {
			if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value), true
			}
		}
		return "", false
	}
	var err error
	if value, ok := get("PORT"); ok {
		cfg.Port, err = strconv.Atoi(value)
		if err != nil || cfg.Port < 1 || cfg.Port > 65535 {
			return cfg, fmt.Errorf("PORT must be a valid TCP port: %q", value)
		}
	}
	for _, item := range []struct {
		keys   []string
		target *bool
	}{
		{[]string{"use_pdl", "USE_PDL"}, &cfg.UsePDL},
		{[]string{"use_dfine", "USE_DFINE"}, &cfg.UseDFINE},
		{[]string{"use_multi", "USE_MULTI"}, &cfg.UseMulti},
		{[]string{"DINO_RANDOM_OFFSET"}, &cfg.DINORandomOffset},
	} {
		if value, ok := get(item.keys...); ok {
			enabled, parseErr := parseBool(value)
			if parseErr != nil {
				return cfg, fmt.Errorf("%s: %w", item.keys[0], parseErr)
			}
			*item.target = enabled
		}
	}
	for _, item := range []struct {
		keys   []string
		target *string
	}{
		{[]string{"MODEL_DIR"}, &cfg.ModelDir}, {[]string{"WORK_DIR"}, &cfg.WorkDir},
		{[]string{"SAVE_DIR"}, &cfg.SaveDir}, {[]string{"LOG_DIR"}, &cfg.LogDir},
		{[]string{"ONNXRUNTIME_SHARED_LIBRARY_PATH"}, &cfg.ONNXRuntimeLibrary},
	} {
		if value, ok := get(item.keys...); ok {
			*item.target = value
		}
	}
	if value, ok := get("LOG_LEVEL"); ok {
		cfg.LogLevel = NormalizeLogLevel(value)
	}
	if value, ok := get("MODEL_IDLE_TIMEOUT"); ok {
		cfg.ModelIdleTimeout, err = time.ParseDuration(value)
		if err != nil || cfg.ModelIdleTimeout < 0 {
			return cfg, fmt.Errorf("MODEL_IDLE_TIMEOUT must be a non-negative Go duration: %q", value)
		}
	}
	for _, item := range []struct {
		key    string
		target *time.Duration
	}{
		{"HTTP_TIMEOUT", &cfg.HTTPTimeout}, {"SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout},
	} {
		if value, ok := get(item.key); ok {
			duration, parseErr := time.ParseDuration(value)
			if parseErr != nil || duration <= 0 {
				return cfg, fmt.Errorf("%s must be a positive Go duration: %q", item.key, value)
			}
			*item.target = duration
		}
	}
	if value, ok := get("MAX_IMAGE_BYTES"); ok {
		cfg.MaxImageBytes, err = strconv.ParseInt(value, 10, 64)
		if err != nil || cfg.MaxImageBytes <= 0 {
			return cfg, fmt.Errorf("MAX_IMAGE_BYTES must be positive: %q", value)
		}
	}
	if value, ok := get("MODEL_PRELOAD"); ok {
		switch strings.ToLower(value) {
		case "", "0", "false", "no", "off":
		case "1", "true", "yes", "on":
			cfg.Preload = []string{"pdl", "dfine", "multi"}
		default:
			for _, name := range strings.Split(value, ",") {
				name = strings.ToLower(strings.TrimSpace(name))
				if name != "pdl" && name != "dfine" && name != "multi" {
					return cfg, fmt.Errorf("MODEL_PRELOAD contains unsupported group %q", name)
				}
				cfg.Preload = append(cfg.Preload, name)
			}
		}
	}
	return cfg, nil
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("expected 0/1 or a Go boolean, got %q", value)
	}
}

func NormalizeLogLevel(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "WARNING" {
		return "WARN"
	}
	return value
}
