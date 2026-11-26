package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kafuneri/captcha-tools-go/internal/logging"
	"github.com/kafuneri/captcha-tools-go/internal/storage"
)

type fakeV4Flow struct {
	puzzle    V4Puzzle
	raw       json.RawMessage
	passed    bool
	err       error
	loaded    bool
	submitted bool
	closed    bool
}

func (f *fakeV4Flow) Load(context.Context) (V4Puzzle, error) {
	f.loaded = true
	return f.puzzle, f.err
}
func (f *fakeV4Flow) Submit(context.Context) (json.RawMessage, bool, error) {
	f.submitted = true
	return f.raw, f.passed, f.err
}
func (f *fakeV4Flow) Close() error { f.closed = true; return nil }

type v4Fixture struct {
	handler   http.Handler
	flow      *fakeV4Flow
	challenge V4Challenge
	saveRoot  string
}

func newV4Fixture(t *testing.T) *v4Fixture {
	t.Helper()
	saveRoot := filepath.Join(t.TempDir(), "saved")
	store, err := storage.New(filepath.Join(t.TempDir(), "work"), saveRoot)
	if err != nil {
		t.Fatal(err)
	}
	flow := &fakeV4Flow{
		puzzle: V4Puzzle{Kind: "slide", Assets: []Artifact{
			{Name: "background.jpg", Data: []byte("background")},
			{Name: "piece.png", Data: []byte("piece")},
		}},
		raw:    json.RawMessage(`{"status":"success","data":{"result":"success","seccode":{"captcha_output":"output"}}}`),
		passed: true,
	}
	_, level, closer, err := logging.New("INFO", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	fixture := &v4Fixture{flow: flow}
	server := New(Options{Models: &fakeModels{}, Store: store, DisableDelays: true,
		V4Flows: func(challenge V4Challenge) (V4Flow, error) {
			fixture.challenge = challenge
			return flow, nil
		}}, level)
	fixture.handler = server
	fixture.saveRoot = saveRoot
	return fixture
}

func TestPassV4Success(t *testing.T) {
	f := newV4Fixture(t)
	r := request(t, f.handler, "/pass_v4?captcha_id=site-1&risk_type=slide&client_type=web&lang=en")
	if r.Code != 200 {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "success" {
		t.Fatalf("unexpected body %v", body)
	}
	if f.challenge.CaptchaID != "site-1" || f.challenge.RiskType != "slide" ||
		f.challenge.ClientType != "web" || f.challenge.Lang != "en" {
		t.Fatalf("challenge not forwarded: %+v", f.challenge)
	}
	if !f.flow.loaded || !f.flow.submitted || !f.flow.closed {
		t.Fatalf("flow lifecycle incomplete: %+v", f.flow)
	}
}

func TestPassV4AcceptsGTAndSavesResult(t *testing.T) {
	f := newV4Fixture(t)
	r := request(t, f.handler, "/pass_v4?gt=site-2&save_result=1")
	if r.Code != 200 {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	if f.challenge.CaptchaID != "site-2" {
		t.Fatalf("gt alias not forwarded: %+v", f.challenge)
	}
	artifacts, err := filepath.Glob(filepath.Join(f.saveRoot, "img_pass", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) == 0 {
		t.Fatal("no artifacts were saved")
	}
	if _, err := os.Stat(filepath.Join(f.saveRoot, "background.jpg")); err != nil {
		t.Fatalf("original image was not saved: %v", err)
	}
}

func TestPassV4WithoutSaveWritesNothing(t *testing.T) {
	f := newV4Fixture(t)
	if r := request(t, f.handler, "/pass_v4?captcha_id=site-1"); r.Code != 200 {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	entries, err := os.ReadDir(filepath.Join(f.saveRoot, "img_pass"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("save_result=false wrote %d entries", len(entries))
	}
}

func TestPassV4SilentSkipsCommit(t *testing.T) {
	f := newV4Fixture(t)
	f.flow.puzzle = V4Puzzle{Kind: "silent"}
	r := request(t, f.handler, "/pass_v4?captcha_id=site-1&save_result=1")
	if r.Code != 200 {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestPassV4LogsPayloadProtocol(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	f := newV4Fixture(t)
	f.flow.puzzle.PayloadProtocol = 2
	if r := request(t, f.handler, "/pass_v4?captcha_id=site-1"); r.Code != 200 {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatalf("expected one JSON log entry, got %q: %v", output.String(), err)
	}
	if entry["payload_protocol"] != float64(2) {
		t.Fatalf("payload_protocol=%v, log=%s", entry["payload_protocol"], output.String())
	}
}

func TestPassV4ValidationAndErrors(t *testing.T) {
	f := newV4Fixture(t)
	for _, target := range []string{"/pass_v4", "/pass_v4?captcha_id=site-1&voice=maybe"} {
		r := request(t, f.handler, target)
		if r.Code != 422 || !strings.Contains(r.Body.String(), "detail") {
			t.Errorf("%s: code=%d body=%s", target, r.Code, r.Body.String())
		}
	}
	f.flow.err = errors.New("v4 upstream failed")
	r := request(t, f.handler, "/pass_v4?captcha_id=site-1")
	if r.Code != 500 || !strings.Contains(r.Body.String(), "v4 upstream failed") {
		t.Fatalf("upstream error: %d %s", r.Code, r.Body.String())
	}
	f.flow.err = nil
	f.flow.raw = json.RawMessage(`not json`)
	r = request(t, f.handler, "/pass_v4?captcha_id=site-1")
	if r.Code != 500 || !strings.Contains(r.Body.String(), "invalid verify JSON response") {
		t.Fatalf("invalid JSON: %d %s", r.Code, r.Body.String())
	}
}
