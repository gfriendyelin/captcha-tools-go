package inference

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/kafuneri/captcha-tools-go/internal/config"
	"github.com/kafuneri/captcha-tools-go/internal/model"
)

const unsupportedModelMessage = "不支持的名称，可以使用便笺、原神、崩铁、绝区零表示"

var pdlAliases = []string{"paddle", "pdl", "nine", "原神", "genshin", "session"}
var dfineAliases = []string{"dfine", "click", "memo", "note", "便笺", "‌便笺‌", "session_dfine"}
var multiAliases = []string{"multi", "dino", "click2", "星穹铁道", "崩铁", "绝区零", "zzz", "hkrpg", "session_yolo11n", "session_dino3", "session_dino_cf"}

type Registry struct {
	pdl          *model.Group
	dfine        *model.Group
	multi        *model.Group
	enabled      map[string]bool
	randomOffset bool
}

func NewRegistry(cfg config.Config, logger *slog.Logger) *Registry {
	r := &Registry{enabled: map[string]bool{"pdl": cfg.UsePDL, "dfine": cfg.UseDFINE, "multi": cfg.UseMulti}, randomOffset: cfg.DINORandomOffset}
	r.pdl = model.NewGroup("pdl", func(ctx context.Context) (model.Resource, error) {
		return LoadPDL(ctx, cfg.ONNXRuntimeLibrary, cfg.ModelDir)
	}, cfg.ModelIdleTimeout, logger)
	r.dfine = model.NewGroup("dfine", func(ctx context.Context) (model.Resource, error) {
		return LoadDFINE(ctx, cfg.ONNXRuntimeLibrary, cfg.ModelDir)
	}, cfg.ModelIdleTimeout, logger)
	r.multi = model.NewGroup("multi", func(ctx context.Context) (model.Resource, error) {
		return LoadMulti(ctx, cfg.ONNXRuntimeLibrary, cfg.ModelDir)
	}, cfg.ModelIdleTimeout, logger)
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
	default:
		return nil
	}
}

func resolveModel(name string) string {
	for group, aliases := range map[string][]string{"pdl": pdlAliases, "dfine": dfineAliases, "multi": multiAliases} {
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
	return result
}

func (r *Registry) ListAllModels() []string {
	return []string{
		"以下是所有可用模型及其对应关键字",
		" -['session']关键词:['paddle', 'pdl', 'nine', '原神', 'genshin']",
		" -['session_dfine']关键词:['dfine', 'click', 'memo', 'note', '便笺', '‌便笺‌']",
		" -['session_yolo11n', 'session_dino3', 'session_dino_cf']关键词:['multi', 'dino', 'click2', '星穹铁道', '崩铁', '绝区零', 'zzz', 'hkrpg']",
	}
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
	for _, group := range []*model.Group{r.pdl, r.dfine, r.multi} {
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
