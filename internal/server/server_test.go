package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kafuneri/captcha-tools-go/internal/logging"
	"github.com/kafuneri/captcha-tools-go/internal/storage"
)

type fakePredictor struct {
	nine  NineResult
	icon  IconResult
	err   error
	useV3 bool
	point *float64
}

func (p *fakePredictor) Nine(_ context.Context, _ []byte, useV3 bool, point *float64) (NineResult, error) {
	p.useV3, p.point = useV3, point
	return p.nine, p.err
}
func (p *fakePredictor) DFINE(_ context.Context, _ []byte, _ bool) (IconResult, error) {
	return p.icon, p.err
}
func (p *fakePredictor) Multi(_ context.Context, _ []byte, _ bool) (IconResult, error) {
	return p.icon, p.err
}

type fakeFlow struct {
	picture  Picture
	response json.RawMessage
	err      error
	points   []string
	closed   bool
}

func (f *fakeFlow) Prepare(context.Context) (Picture, error) { return f.picture, f.err }
func (f *fakeFlow) Verify(_ context.Context, points []string) (json.RawMessage, error) {
	f.points = append([]string(nil), points...)
	return f.response, f.err
}
func (f *fakeFlow) Close() error { f.closed = true; return nil }

type fakeModels struct {
	mu     sync.Mutex
	loaded []string
}

func (m *fakeModels) ListModels() []string {
	return append([]string{"以下是当前加载的模型及其对应关键字"}, m.loaded...)
}
func (m *fakeModels) ListAllModels() []string {
	return []string{"以下是所有可用模型及其对应关键字"}
}
func (m *fakeModels) LoadModel(_ context.Context, name string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loaded = append(m.loaded, name)
	return m.ListModels(), nil
}
func (m *fakeModels) UnloadModel(_ context.Context, _ string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loaded = nil
	return m.ListModels(), nil
}

func TestModeRouteIsolation(t *testing.T) {
	_, level, closer, err := logging.New("INFO", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	v3 := New(Options{Mode: "v3"}, level)
	recorder := httptest.NewRecorder()
	v3.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pass_v4?captcha_id=x", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("v3 /pass_v4 = %d, want 404", recorder.Code)
	}

	v4 := New(Options{Mode: "v4"}, level)
	recorder = httptest.NewRecorder()
	v4.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pass_nine?gt=x&challenge=y", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("v4 /pass_nine = %d, want 404", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	v4.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pass_uni", nil))
	if recorder.Code == http.StatusNotFound {
		t.Fatalf("v4 /pass_uni should be the V4 alias, got 404")
	}
}

type fixture struct {
	handler   http.Handler
	predictor *fakePredictor
	flow      *fakeFlow
	models    *fakeModels
}

func newFixture(t *testing.T, pictureType string) *fixture {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "work"), filepath.Join(t.TempDir(), "saved"))
	if err != nil {
		t.Fatal(err)
	}
	predictor := &fakePredictor{nine: NineResult{Cells: [][2]int{{1, 2}, {1, 3}}}, icon: IconResult{Points: [][2]float64{{166.5, 333}}}}
	flow := &fakeFlow{picture: Picture{Data: []byte("image"), Name: "safe.jpg", Type: pictureType}, response: json.RawMessage(`{"status":"success","data":{"result":"success","validate":"token"}}`)}
	models := &fakeModels{}
	_, level, closer, err := logging.New("INFO", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	server := New(Options{Predictor: predictor, Models: models, Store: store, DisableDelays: true,
		Flows: func(_, _ string) (Flow, error) { return flow, nil }}, level)
	return &fixture{handler: server, predictor: predictor, flow: flow, models: models}
}

func request(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", target, nil))
	return recorder
}

func TestPassNineResponseAndCoordinates(t *testing.T) {
	f := newFixture(t, "nine")
	r := request(t, f.handler, "/pass_nine?gt=g&challenge=c&use_v3_model=false&point=0.75")
	if r.Code != 200 {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "success" {
		t.Fatalf("unexpected body: %v", body)
	}
	if got := strings.Join(f.flow.points, ","); got != "2_1,3_1" {
		t.Fatalf("points=%q", got)
	}
	if f.predictor.useV3 || f.predictor.point == nil || *f.predictor.point != 0.75 {
		t.Fatalf("nine options not forwarded")
	}
	if !f.flow.closed {
		t.Fatal("flow was not closed")
	}
}

func TestPassIconNormalization(t *testing.T) {
	for _, test := range []struct{ pictureType, route string }{{"icon", "/pass_uni"}, {"icon1", "/pass_icon"}} {
		f := newFixture(t, test.pictureType)
		r := request(t, f.handler, test.route+"?gt=g&challenge=c")
		if r.Code != 200 || strings.Join(f.flow.points, ",") != "5000_10000" {
			t.Fatalf("%s: code=%d points=%v body=%s", test.pictureType, r.Code, f.flow.points, r.Body.String())
		}
	}
}

func TestHutaoSuccessAndError(t *testing.T) {
	f := newFixture(t, "nine")
	r := request(t, f.handler, "/pass_hutao?gt=g&challenge=c")
	var success struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &success); err != nil {
		t.Fatal(err)
	}
	if success.Code != 0 || success.Data["validate"] != "token" || success.Data["gt"] != "g" {
		t.Fatalf("unexpected Hutao response: %s", r.Body.String())
	}
	f.flow.response = json.RawMessage(`{"status":"error","data":{"result":"fail"}}`)
	r = request(t, f.handler, "/pass_hutao?gt=g&challenge=c")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"code":1`) {
		t.Fatalf("unexpected Hutao failure: %d %s", r.Code, r.Body.String())
	}
}

func TestValidationAndInternalErrors(t *testing.T) {
	f := newFixture(t, "nine")
	for _, target := range []string{"/pass_uni?challenge=c", "/pass_uni?gt=g", "/pass_nine?gt=g&challenge=c&point=nope", "/load_model", "/unload_model"} {
		r := request(t, f.handler, target)
		if r.Code != 422 || !strings.Contains(r.Body.String(), "detail") {
			t.Errorf("%s: code=%d body=%s", target, r.Code, r.Body.String())
		}
	}
	f.predictor.err = errors.New("model is not loaded")
	r := request(t, f.handler, "/pass_uni?gt=g&challenge=c")
	if r.Code != 500 || !strings.Contains(r.Body.String(), "model is not loaded") {
		t.Fatalf("model error: %d %s", r.Code, r.Body.String())
	}
	f.predictor.err = nil
	f.flow.err = errors.New("recorded upstream error")
	r = request(t, f.handler, "/pass_uni?gt=g&challenge=c")
	if r.Code != 500 || !strings.Contains(r.Body.String(), "recorded upstream error") {
		t.Fatalf("upstream error: %d %s", r.Code, r.Body.String())
	}
}

func TestUnsafePictureName(t *testing.T) {
	f := newFixture(t, "nine")
	f.flow.picture.Name = ".."
	r := request(t, f.handler, "/pass_uni?gt=g&challenge=c")
	if r.Code != 500 || !strings.Contains(r.Body.String(), "unsafe server filename") {
		t.Fatalf("unsafe name: %d %s", r.Code, r.Body.String())
	}
}

func TestModelAndLogRoutes(t *testing.T) {
	f := newFixture(t, "nine")
	if r := request(t, f.handler, "/load_model?name=pdl"); r.Code != 200 || !strings.Contains(r.Body.String(), "pdl") {
		t.Fatalf("load: %s", r.Body.String())
	}
	if r := request(t, f.handler, "/list_model"); r.Code != 200 || !strings.Contains(r.Body.String(), "pdl") {
		t.Fatalf("list: %s", r.Body.String())
	}
	if r := request(t, f.handler, "/list_all_model"); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if r := request(t, f.handler, "/unload_model?name=pdl"); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if r := request(t, f.handler, "/set_log_level?level=DEBUG"); r.Code != 200 || r.Body.String() != `"Log level successfully set to DEBUG"` {
		t.Fatalf("log: %s", r.Body.String())
	}
	if r := request(t, f.handler, "/set_log_level?level=verbose"); r.Code != 400 {
		t.Fatalf("invalid log status=%d", r.Code)
	}
}

func TestHutaoPreservesHTTPError(t *testing.T) {
	f := newFixture(t, "nine")
	f.flow.err = fmt.Errorf("boom")
	r := request(t, f.handler, "/pass_hutao?gt=g&challenge=c")
	if r.Code != 500 || !strings.Contains(r.Body.String(), `"code":1`) || !strings.Contains(r.Body.String(), "boom") {
		t.Fatalf("error mapping: %d %s", r.Code, r.Body.String())
	}
}
