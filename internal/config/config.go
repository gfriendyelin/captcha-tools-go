package config

import (
	"fmt"
	"os"
	"runtime"
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
	SaveResult         bool
	LogDir             string
	LogLevel           string
	UsePDL             bool
	UseDFINE           bool
	UseMulti           bool
	UseGrid            bool
	Preload            []string
	ModelIdleTimeout   time.Duration
	HTTPTimeout        time.Duration
	ShutdownTimeout    time.Duration
	MaxImageBytes      int64
	DINORandomOffset   bool
	DisableDelays      bool
	ONNXRuntimeLibrary string
}

// Default returns the service defaults. PDL/D-FINE are enabled; Multi and Grid
// are also enabled but stay cold (not preloaded) until the corresponding route
// or MODEL_PRELOAD requests them.
func Default() Config {
	library := ""
	if runtime.GOOS == "windows" {
		library = ".onnxruntime/onnxruntime.dll"
	} else {
		library = ".onnxruntime/libonnxruntime.so"
	}
	return Config{
		Port:               DefaultPort,
		ModelDir:           "model",
		WorkDir:            "workspace",
		SaveDir:            "saves",
		SaveResult:         false,
		LogDir:             "logs",
		LogLevel:           "INFO",
		UsePDL:             true,
		UseDFINE:           true,
		UseMulti:           true,
		UseGrid:            true,
		Preload:            []string{},
		ModelIdleTimeout:   10 * time.Minute,
		HTTPTimeout:        10 * time.Second,
		ShutdownTimeout:    10 * time.Second,
		MaxImageBytes:      10 << 20,
		DINORandomOffset:   true,
		DisableDelays:      false,
		ONNXRuntimeLibrary: library,
	}
}

// FromEnv applies environment variables to cfg using the provided lookup. The
// lookup is a function for testability; Load() wires os.LookupEnv.
func FromEnv(lookup func(string) (string, bool)) (Config, error) {
	cfg := Default()
	get := func(names ...string) (string, bool) {
		for _, name := range names {
			if value, ok := lookup(name); ok {
				return value, true
			}
		}
		return "", false
	}
	if value, ok := get("PORT", "CAPTCHA_TOOLS_PORT"); ok {
		port, err := strconv.Atoi(value)
		if err != nil || port <= 0 || port > 65535 {
			return cfg, fmt.Errorf("PORT must be 1..65535: %q", value)
		}
		cfg.Port = port
	}
	if value, ok := get("MODEL_DIR"); ok {
		cfg.ModelDir = value
	}
	if value, ok := get("WORK_DIR"); ok {
		cfg.WorkDir = value
	}
	if value, ok := get("SAVE_DIR"); ok {
		cfg.SaveDir = value
	}
	if value, ok := get("SAVE_RESULT"); ok {
		parsed, err := parseBool(value)
		if err != nil {
			return cfg, err
		}
		cfg.SaveResult = parsed
	}
	if value, ok := get("LOG_DIR"); ok {
		cfg.LogDir = value
	}
	if value, ok := get("LOG_LEVEL"); ok {
		cfg.LogLevel = normalizeLogLevel(value)
	}
	for _, item := range []struct {
		names  []string
		target *bool
	}{
		{[]string{"use_pdl", "USE_PDL"}, &cfg.UsePDL},
		{[]string{"use_dfine", "USE_DFINE"}, &cfg.UseDFINE},
		{[]string{"use_multi", "USE_MULTI"}, &cfg.UseMulti},
		{[]string{"use_grid", "USE_GRID"}, &cfg.UseGrid},
		{[]string{"disable_delays", "DISABLE_DELAYS"}, &cfg.DisableDelays},
		{[]string{"dino_random_offset", "DINO_RANDOM_OFFSET"}, &cfg.DINORandomOffset},
	} {
		if value, ok := get(item.names...); ok {
			parsed, err := parseBool(value)
			if err != nil {
				return cfg, err
			}
			*item.target = parsed
		}
	}
	if value, ok := get("MODEL_IDLE_TIMEOUT"); ok {
		duration, err := parseDuration(value)
		if err != nil {
			return cfg, err
		}
		cfg.ModelIdleTimeout = duration
	}
	if value, ok := get("HTTP_TIMEOUT"); ok {
		duration, err := parseDuration(value)
		if err != nil {
			return cfg, err
		}
		cfg.HTTPTimeout = duration
	}
	if value, ok := get("SHUTDOWN_TIMEOUT"); ok {
		duration, err := parseDuration(value)
		if err != nil {
			return cfg, err
		}
		cfg.ShutdownTimeout = duration
	}
	if value, ok := get("MAX_IMAGE_BYTES"); ok {
		bytes, err := strconv.ParseInt(value, 10, 64)
		if err != nil || bytes <= 0 {
			return cfg, fmt.Errorf("MAX_IMAGE_BYTES must be positive: %q", value)
		}
		cfg.MaxImageBytes = bytes
	}
	if value, ok := get("MODEL_PRELOAD"); ok {
		switch strings.ToLower(value) {
		case "", "0", "false", "no", "off":
			cfg.Preload = []string{}
		case "1", "true", "yes", "on":
			// PDL/D-FINE stay hot; Multi/Grid stay cold unless listed.
			cfg.Preload = []string{"pdl", "dfine"}
		default:
			var names []string
			for _, name := range strings.Split(value, ",") {
				name = strings.ToLower(strings.TrimSpace(name))
				if name != "pdl" && name != "dfine" && name != "multi" && name != "grid" {
					return cfg, fmt.Errorf("MODEL_PRELOAD contains unsupported group %q", name)
				}
				names = append(names, name)
			}
			cfg.Preload = names
		}
	}
	if value, ok := get("ONNXRUNTIME_SHARED_LIBRARY_PATH"); ok {
		cfg.ONNXRuntimeLibrary = value
	}
	return cfg, nil
}

// Load builds the default configuration and applies the process environment.
func Load() (Config, error) {
	return FromEnv(os.LookupEnv)
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", value)
	}
}

// parseDuration accepts Go duration strings ("1500ms", "10m", "1h30m") and
// rejects bare numbers and non-positive values.
func parseDuration(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("duration must be positive: %q", value)
	}
	return duration, nil
}

// normalizeLogLevel canonicalizes a LOG_LEVEL value: it trims, uppercases and
// maps the WARNING synonym to WARN so callers see one canonical spelling.
func normalizeLogLevel(value string) string {
	level := strings.ToUpper(strings.TrimSpace(value))
	if level == "WARNING" {
		return "WARN"
	}
	return level
}
