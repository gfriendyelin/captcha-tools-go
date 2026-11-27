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

// Mode selects which protocol workers a process serves.
//
//   - all: gateway manages both V3 and V4 workers (single-container default)
//   - v3:  only the V3 worker/routes
//   - v4:  only the V4 worker/routes
type Mode string

const (
	ModeAll Mode = "all"
	ModeV3  Mode = "v3"
	ModeV4  Mode = "v4"
)

func ParseMode(value string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all", "both":
		return ModeAll, nil
	case "v3", "3":
		return ModeV3, nil
	case "v4", "4":
		return ModeV4, nil
	default:
		return "", fmt.Errorf("MODE must be all, v3 or v4: %q", value)
	}
}

func (m Mode) IncludesV3() bool { return m == ModeAll || m == ModeV3 }

func (m Mode) IncludesV4() bool { return m == ModeAll || m == ModeV4 }

type Config struct {
	Mode       Mode
	Port       int
	ModelDir   string
	WorkDir    string
	SaveDir    string
	SaveResult bool
	LogDir     string
	LogLevel   string
	UsePDL     bool
	UseDFINE   bool
	UseMulti   bool
	UseGrid    bool
	Preload    []string
	PreloadV3  []string
	PreloadV4  []string
	// ModelIdleTimeout is deprecated and ignored. Workers release model memory
	// by exiting; use WorkerIdleTimeout instead.
	ModelIdleTimeout     time.Duration
	WorkerIdleTimeout    time.Duration
	WorkerStartTimeout   time.Duration
	WorkerRequestTimeout time.Duration
	WorkerSocket         string
	HTTPTimeout          time.Duration
	ShutdownTimeout      time.Duration
	MaxImageBytes        int64
	DINORandomOffset     bool
	DisableDelays        bool
	ONNXRuntimeLibrary   string
}

// Default returns the service defaults. A worker exits after one minute of no
// requests; model groups themselves are not unloaded in-process.
func Default() Config {
	library := ""
	if runtime.GOOS == "windows" {
		library = ".onnxruntime/onnxruntime.dll"
	} else {
		library = ".onnxruntime/libonnxruntime.so"
	}
	return Config{
		Mode:                 ModeAll,
		Port:                 DefaultPort,
		ModelDir:             "model",
		WorkDir:              "img_2_val",
		SaveDir:              "img_saved",
		SaveResult:           false,
		LogDir:               "logs",
		LogLevel:             "INFO",
		UsePDL:               true,
		UseDFINE:             true,
		UseMulti:             true,
		UseGrid:              true,
		Preload:              []string{"pdl", "dfine"},
		PreloadV3:            []string{"pdl", "dfine"},
		PreloadV4:            []string{},
		WorkerIdleTimeout:    time.Minute,
		WorkerStartTimeout:   2 * time.Minute,
		WorkerRequestTimeout: 3 * time.Minute,
		HTTPTimeout:          10 * time.Second,
		ShutdownTimeout:      10 * time.Second,
		MaxImageBytes:        10 << 20,
		DINORandomOffset:     true,
		DisableDelays:        false,
		ONNXRuntimeLibrary:   library,
	}
}

func defaultPreload(mode Mode) []string {
	if mode == ModeV3 {
		return []string{"pdl", "dfine"}
	}
	return []string{}
}

func allowedPreload(mode Mode, name string) bool {
	switch mode {
	case ModeV3:
		return name == "pdl" || name == "dfine" || name == "multi" || name == "grid"
	case ModeV4:
		return name == "grid" || name == "direction" || name == "symbols" || name == "word" || name == "phrase"
	default:
		switch name {
		case "pdl", "dfine", "multi", "grid", "direction", "symbols", "word", "phrase":
			return true
		}
		return false
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
	if value, ok := get("MODE"); ok {
		mode, err := ParseMode(value)
		if err != nil {
			return cfg, err
		}
		cfg.Mode = mode
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
	// WORKER_IDLE_TIMEOUT is the primary setting. MODEL_IDLE_TIMEOUT is kept as
	// a deprecated alias so existing deployments keep working; zero disables
	// the idle exit.
	if value, ok := get("WORKER_IDLE_TIMEOUT"); ok {
		duration, err := parseNonNegativeDuration(value)
		if err != nil {
			return cfg, fmt.Errorf("WORKER_IDLE_TIMEOUT: %w", err)
		}
		cfg.WorkerIdleTimeout = duration
	} else if value, ok := get("MODEL_IDLE_TIMEOUT"); ok {
		duration, err := parseNonNegativeDuration(value)
		if err != nil {
			return cfg, fmt.Errorf("MODEL_IDLE_TIMEOUT: %w", err)
		}
		cfg.WorkerIdleTimeout = duration
	}
	if value, ok := get("WORKER_START_TIMEOUT"); ok {
		duration, err := parseDuration(value)
		if err != nil {
			return cfg, fmt.Errorf("WORKER_START_TIMEOUT: %w", err)
		}
		cfg.WorkerStartTimeout = duration
	}
	if value, ok := get("WORKER_REQUEST_TIMEOUT"); ok {
		duration, err := parseDuration(value)
		if err != nil {
			return cfg, fmt.Errorf("WORKER_REQUEST_TIMEOUT: %w", err)
		}
		cfg.WorkerRequestTimeout = duration
	}
	if value, ok := get("WORKER_SOCKET"); ok {
		cfg.WorkerSocket = strings.TrimSpace(value)
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
	cfg.PreloadV3 = defaultPreload(ModeV3)
	cfg.PreloadV4 = defaultPreload(ModeV4)
	if value, ok := get("MODEL_PRELOAD"); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "0", "false", "no", "off":
			cfg.PreloadV3, cfg.PreloadV4 = []string{}, []string{}
		case "1", "true", "yes", "on":
			// true means the per-mode defaults.
		default:
			cfg.PreloadV3, cfg.PreloadV4 = []string{}, []string{}
			for _, name := range strings.Split(value, ",") {
				name = strings.ToLower(strings.TrimSpace(name))
				v3 := allowedPreload(ModeV3, name)
				v4 := allowedPreload(ModeV4, name)
				if !v3 && !v4 {
					return cfg, fmt.Errorf("MODEL_PRELOAD contains unsupported group %q", name)
				}
				if cfg.Mode == ModeV3 && !v3 {
					return cfg, fmt.Errorf("MODEL_PRELOAD contains %q, which is not valid for MODE=v3", name)
				}
				if cfg.Mode == ModeV4 && !v4 {
					return cfg, fmt.Errorf("MODEL_PRELOAD contains %q, which is not valid for MODE=v4", name)
				}
				if v3 {
					cfg.PreloadV3 = append(cfg.PreloadV3, name)
				}
				if v4 {
					cfg.PreloadV4 = append(cfg.PreloadV4, name)
				}
			}
		}
	}
	switch cfg.Mode {
	case ModeV3:
		cfg.Preload = append([]string(nil), cfg.PreloadV3...)
	case ModeV4:
		cfg.Preload = append([]string(nil), cfg.PreloadV4...)
	default:
		cfg.Preload = append(append([]string(nil), cfg.PreloadV3...), cfg.PreloadV4...)
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

func parseNonNegativeDuration(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	if duration < 0 {
		return 0, fmt.Errorf("duration must be non-negative: %q", value)
	}
	return duration, nil
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
