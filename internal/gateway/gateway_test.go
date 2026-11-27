package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kafuneri/captcha-tools-go/internal/config"
)

func testGateway(t *testing.T, mode config.Mode) *Gateway {
	t.Helper()
	g, err := New(Options{
		Mode:           mode,
		WorkerBinary:   "/bin/true",
		SocketDir:      t.TempDir(),
		IdleTimeout:    time.Minute,
		StartTimeout:   time.Second,
		RequestTimeout: time.Second,
		Logger:         slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGatewayRouteIsolation(t *testing.T) {
	g := testGateway(t, config.ModeV4)
	for _, path := range []string{"/pass_nine", "/pass_icon", "/pass_hutao", "/pass_uni?version=3&gt=x&challenge=y"} {
		recorder := httptest.NewRecorder()
		g.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d, want 404", path, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest("GET", "/list_model?version=4", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "[]" {
		t.Fatalf("list_model = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayUnifiedAmbiguousVersion(t *testing.T) {
	g := testGateway(t, config.ModeAll)
	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest("GET", "/pass_uni?gt=x", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous version = %d", recorder.Code)
	}
}

func TestGatewayAggregateListWhenWorkersStopped(t *testing.T) {
	g := testGateway(t, config.ModeAll)
	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest("GET", "/list_model?version=all", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("aggregate list = %d %s", recorder.Code, recorder.Body.String())
	}
	var body map[string][]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body["v3"]) != 0 || len(body["v4"]) != 0 {
		t.Fatalf("unexpected aggregate body: %v", body)
	}
}

func TestGatewayModeV3ListDefault(t *testing.T) {
	g := testGateway(t, config.ModeV3)
	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest("GET", "/list_model", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "[]" {
		t.Fatalf("list_model = %d %q", recorder.Code, recorder.Body.String())
	}
}
