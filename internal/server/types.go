package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kafuneri/captcha-tools-go/internal/storage"
)

type Picture struct {
	Data []byte
	Name string
	Type string
}

type Artifact struct {
	Name string
	Data []byte
}

type NineResult struct {
	Cells     [][2]int
	Artifacts []Artifact
}
type IconResult struct {
	Points     [][2]float64
	Sequence   [][4]int
	YOLOTop    [][4]int
	YOLOBottom [][4]int
	Artifacts  []Artifact
}

type Predictor interface {
	Nine(context.Context, []byte, bool, *float64) (NineResult, error)
	DFINE(context.Context, []byte, bool) (IconResult, error)
	Multi(context.Context, []byte, bool) (IconResult, error)
}

type Flow interface {
	Prepare(context.Context) (Picture, error)
	Verify(context.Context, []string) (json.RawMessage, error)
	Close() error
}

type FlowFactory func(gt, challenge string) (Flow, error)

type ModelController interface {
	ListModels() []string
	ListAllModels() []string
	LoadModel(context.Context, string) ([]string, error)
	UnloadModel(context.Context, string) ([]string, error)
}

type Options struct {
	Predictor     Predictor
	Flows         FlowFactory
	Models        ModelController
	Store         *storage.Store
	VerifyDelay   time.Duration
	DisableDelays bool
}
