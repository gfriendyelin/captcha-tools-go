package inference

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/kafuneri/captcha-tools-go/internal/config"
	"github.com/kafuneri/captcha-tools-go/internal/model"
)

const unsupportedModelMessage = "不支持的名称，可以使用便笺、原神、崩铁、绝区零表示"

var pdlAliases = []string{"paddle", "pdl", "nine", "原神", "genshin", "session"}
var dfineAliases = []string{"dfine", "click", "memo", "note", "便笺", "‌便笺‌", "session_dfine"}
var multiAliases = []string{"multi", "dino", "click2", "星穹铁道", "崩铁", "绝区零", "zzz", "hkrpg", "session_yolo11n", "session_dino3", "session_dino_cf"}
var gridAliases = []string{"grid", "九宫格", "nine_grid"}
var directionAliases = []string{"direction", "方向", "icon_direction"}
var symbolsAliases = []string{"symbols", "symbol", "图标", "icon"}
var wordAliases = []string{"word", "文字", "字符", "text", "单词"}
var phraseAliases = []string{"phrase", "短语", "短句", "成语", "词语", "phrase_detect"}

type Registry struct {
	pdl          *model.Group
	dfine        *model.Group
	multi        *model.Group
	grid         *model.Group
	direction    *model.Group
	symbols      *model.Group
	word         *model.Group
	phrase       *model.Group
	modelDir     string
	enabled      map[string]bool
	randomOffset bool
}

func NewRegistry(cfg config.Config, logger *slog.Logger) *Registry {
	mode := cfg.Mode
	if mode == "" {
		mode = config.ModeAll
	}
	enabled := map[string]bool{
		"pdl": cfg.UsePDL, "dfine": cfg.UseDFINE, "multi": cfg.UseMulti, "grid": cfg.UseGrid,
		"direction": true, "symbols": true, "word": true, "phrase": true,
	}
	switch mode {
	case config.ModeV3:
		enabled["direction"], enabled["symbols"], enabled["word"], enabled["phrase"] = false, false, false, false
	case config.ModeV4:
		enabled["pdl"], enabled["dfine"], enabled["multi"] = false, false, false
	}
	r := &Registry{
		enabled:      enabled,
		randomOffset: cfg.DINORandomOffset,
		modelDir:     cfg.ModelDir,
	}
	// Model groups stay loaded while the worker lives. The worker process exits
	// after WORKER_IDLE_TIMEOUT, which is the only supported way to reclaim RSS.
	r.pdl = model.NewGroup("pdl", func(ctx context.Context) (model.Resource, error) {
		return LoadPDL(ctx, cfg.ONNXRuntimeLibrary, cfg.ModelDir)
	}, 0, logger)
	r.dfine = model.NewGroup("dfine", func(ctx context.Context) (model.Resource, error) {
		return LoadDFINE(ctx, cfg.ONNXRuntimeLibrary, cfg.ModelDir)
	}, 0, logger)
	r.multi = model.NewGroup("multi", func(ctx context.Context) (model.Resource, error) {
		return LoadMulti(ctx, cfg.ONNXRuntimeLibrary, cfg.ModelDir)
	}, 0, logger)
	r.grid = model.NewGroup("grid", func(ctx context.Context) (model.Resource, error) {
		return LoadGrid(ctx, cfg.ONNXRuntimeLibrary, filepath.Join(cfg.ModelDir, "grid"))
	}, 0, logger)
	r.direction = model.NewGroup("direction", func(ctx context.Context) (model.Resource, error) {
		return LoadDirection(ctx, cfg.ONNXRuntimeLibrary, filepath.Join(cfg.ModelDir, "v4-direction"))
	}, 0, logger)
	r.symbols = model.NewGroup("symbols", func(ctx context.Context) (model.Resource, error) {
		return LoadSymbols(ctx, cfg.ONNXRuntimeLibrary, filepath.Join(cfg.ModelDir, "v4-symbols"))
	}, 0, logger)
	r.word = model.NewGroup("word", func(ctx context.Context) (model.Resource, error) {
		return LoadWord(ctx, cfg.ONNXRuntimeLibrary, filepath.Join(cfg.ModelDir, "v4-word"))
	}, 0, logger)
	r.phrase = model.NewGroup("phrase", func(ctx context.Context) (model.Resource, error) {
		return LoadPhrase(ctx, cfg.ONNXRuntimeLibrary, filepath.Join(cfg.ModelDir, "v4-phrase-core"), true)
	}, 0, logger)
	return r
}

func (r *Registry) group(name string) *model.Group {
	switch name {
	case "pdl":
		return r.pdl
	case "dfine":
		return r.dfine
	case "multi":
		return r.multi
	case "grid":
		return r.grid
	case "direction":
		return r.direction
	case "symbols":
		return r.symbols
	case "word":
		return r.word
	case "phrase":
		return r.phrase
	default:
		return nil
	}
}

func resolveModel(name string) string {
	for group, aliases := range map[string][]string{"pdl": pdlAliases, "dfine": dfineAliases, "multi": multiAliases, "grid": gridAliases, "direction": directionAliases, "symbols": symbolsAliases, "word": wordAliases, "phrase": phraseAliases} {
		for _, alias := range aliases {
			if name == alias {
				return group
			}
		}
	}
	return ""
}

func (r *Registry) ListModels() []string {
	result := []string{"以下是当前加载的模型及其对应关键字"}
	if r.pdl.Loaded() {
		result = append(result, " -session,关键词:['paddle', 'pdl', 'nine', '原神', 'genshin']")
	}
	if r.dfine.Loaded() {
		result = append(result, " -session_dfine,关键词:['dfine', 'click', 'memo', 'note', '便笺', '‌便笺‌']")
	}
	if r.multi.Loaded() {
		for _, session := range []string{"session_yolo11n", "session_dino3", "session_dino_cf"} {
			result = append(result, " -"+session+",关键词:['multi', 'dino', 'click2', '星穹铁道', '崩铁', '绝区零', 'zzz', 'hkrpg']")
		}
	}
	if r.grid.Loaded() {
		result = append(result, " -session_grid,关键词:['grid', '九宫格', 'nine_grid']")
	}
	if r.direction.Loaded() {
		result = append(result, " -session_direction,关键词:['direction', '方向', 'icon_direction']")
	}
	if r.symbols.Loaded() {
		result = append(result, " -session_symbols,关键词:['symbols', 'symbol', '图标', 'icon']")
	}
	if r.word.Loaded() {
		result = append(result, " -session_word,关键词:['word', '文字', '字符', 'text', '单词']")
	}
	if r.phrase.Loaded() {
		result = append(result, " -session_phrase,关键词:['phrase', '短语', '短句', '成语', '词语']")
	}
	return result
}

func (r *Registry) ListAllModels() []string {
	result := []string{"以下是所有可用模型及其对应关键字"}
	descriptions := []struct {
		group string
		text  string
	}{
		{"pdl", " -['session']关键词:['paddle', 'pdl', 'nine', '原神', 'genshin']"},
		{"dfine", " -['session_dfine']关键词:['dfine', 'click', 'memo', 'note', '便笺', '‌便笺‌']"},
		{"multi", " -['session_yolo11n', 'session_dino3', 'session_dino_cf']关键词:['multi', 'dino', 'click2', '星穹铁道', '崩铁', '绝区零', 'zzz', 'hkrpg']"},
		{"grid", " -['session_grid']关键词:['grid', '九宫格', 'nine_grid']"},
		{"direction", " -['session_direction']关键词:['direction', '方向', 'icon_direction']"},
		{"symbols", " -['session_symbols']关键词:['symbols', 'symbol', '图标', 'icon']"},
		{"word", " -['session_word']关键词:['word', '文字', '字符', 'text', '单词']"},
		{"phrase", " -['session_phrase']关键词:['phrase', '短语', '短句', '成语', '词语']"},
	}
	for _, item := range descriptions {
		if r.enabled[item.group] {
			result = append(result, item.text)
		}
	}
	return result
}

func (r *Registry) LoadModel(ctx context.Context, name string) ([]string, error) {
	resolved := resolveModel(name)
	if resolved == "" {
		return append([]string{unsupportedModelMessage}, r.ListModels()...), nil
	}
	if !r.enabled[resolved] {
		return nil, fmt.Errorf("model group %s is disabled by use_%s=0", resolved, resolved)
	}
	lease, err := r.group(resolved).Acquire(ctx)
	if err != nil {
		return nil, err
	}
	lease.Release()
	return r.ListModels(), nil
}

func (r *Registry) UnloadModel(ctx context.Context, name string) ([]string, error) {
	resolved := resolveModel(name)
	if resolved == "" {
		return append([]string{unsupportedModelMessage}, r.ListModels()...), nil
	}
	if err := r.group(resolved).Unload(ctx); err != nil {
		return nil, err
	}
	return r.ListModels(), nil
}

func (r *Registry) Preload(ctx context.Context, names []string) error {
	for _, name := range names {
		if !r.enabled[name] {
			continue
		}
		if _, err := r.LoadModel(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) Close(ctx context.Context) error {
	var errs []error
	for _, group := range []*model.Group{r.pdl, r.dfine, r.multi, r.grid, r.direction, r.symbols, r.word, r.phrase} {
		if err := group.Unload(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := group.Close(); err != nil && !errors.Is(err, model.ErrActiveLeases) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Registry) acquire(ctx context.Context, name string) (*model.Lease, error) {
	if !r.enabled[name] {
		return nil, fmt.Errorf("model group %s is disabled; set use_%s=1", name, name)
	}
	return r.group(name).Acquire(ctx)
}
