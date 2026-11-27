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

// V4Puzzle is the loaded puzzle kind and its saveable inputs.
type V4Puzzle struct {
	Kind   string
	Assets []Artifact
	// PayloadProtocol is the pt reported by /load; 0 when unknown.
	PayloadProtocol int
}

// V4Challenge identifies a GeeTest V4 site.
type V4Challenge struct {
	CaptchaID  string
	ClientType string
	RiskType   string
	Lang       string
	Voice      bool
}

// V4Flow loads one V4 challenge and submits the solved answer. Submit returns
// the upstream verify JSON and whether the proof passed.
type V4Flow interface {
	Load(context.Context) (V4Puzzle, error)
	Submit(context.Context) (json.RawMessage, bool, error)
	Close() error
}

type V4FlowFactory func(V4Challenge) (V4Flow, error)

type ModelController interface {
	ListModels() []string
	ListAllModels() []string
	LoadModel(context.Context, string) ([]string, error)
	UnloadModel(context.Context, string) ([]string, error)
}

type Options struct {
	Mode          string
	Predictor     Predictor
	Flows         FlowFactory
	V4Flows       V4FlowFactory
	Models        ModelController
	Store         *storage.Store
	SaveResult    bool
	VerifyDelay   time.Duration
	DisableDelays bool
}
