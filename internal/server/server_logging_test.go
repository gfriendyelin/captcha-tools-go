package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestPassRequestLogging(t *testing.T) {
	tests := []struct {
		name             string
		pictureType      string
		target           string
		response         string
		predictorErr     error
		wantStatus       int
		wantOutcome      string
		wantLevel        string
		wantGroup        string
		wantPassed       bool
		wantVerifyStatus string
	}{
		{
			name:             "successful verification",
			pictureType:      "nine",
			target:           "/pass_nine?gt=secret-gt&challenge=secret-challenge",
			wantStatus:       200,
			wantOutcome:      "success",
			wantLevel:        "INFO",
			wantGroup:        "pdl",
			wantPassed:       true,
			wantVerifyStatus: "success",
		},
		{
			name:             "upstream rejects verification",
			pictureType:      "icon1",
			target:           "/pass_icon?gt=secret-gt&challenge=secret-challenge",
			response:         `{"status":"error","data":{"result":"fail"}}`,
			wantStatus:       200,
			wantOutcome:      "failed",
			wantLevel:        "WARN",
			wantGroup:        "dfine",
			wantPassed:       false,
			wantVerifyStatus: "error",
		},
		{
			name:         "internal inference error",
			pictureType:  "icon",
			target:       "/pass_uni?gt=secret-gt&challenge=secret-challenge",
			predictorErr: errors.New("sensitive-model-error"),
			wantStatus:   500,
			wantOutcome:  "error",
			wantLevel:    "ERROR",
			wantGroup:    "multi",
			wantPassed:   false,
		},
		{
			name:        "invalid request",
			pictureType: "nine",
			target:      "/pass_nine?challenge=secret-challenge",
			wantStatus:  422,
			wantOutcome: "rejected",
			wantLevel:   "WARN",
			wantGroup:   "unknown",
			wantPassed:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			f := newFixture(t, test.pictureType)
			if test.response != "" {
				f.flow.response = json.RawMessage(test.response)
			}
			f.predictor.err = test.predictorErr
			r := request(t, f.handler, test.target)
			if r.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
			}

			var entry map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
				t.Fatalf("expected one JSON log entry, got %q: %v", output.String(), err)
			}
			checks := map[string]any{
				"msg":           "captcha request completed",
				"outcome":       test.wantOutcome,
				"level":         test.wantLevel,
				"model_group":   test.wantGroup,
				"passed":        test.wantPassed,
				"verify_status": test.wantVerifyStatus,
				"http_status":   float64(test.wantStatus),
			}
			for field, want := range checks {
				if got := entry[field]; got != want {
					t.Errorf("%s=%v, want %v; log=%s", field, got, want, output.String())
				}
			}
			if entry["route"] != strings.SplitN(test.target, "?", 2)[0] {
				t.Errorf("route=%v, log=%s", entry["route"], output.String())
			}
			if _, ok := entry["duration"]; !ok {
				t.Error("duration is missing")
			}
			for _, secret := range []string{"secret-gt", "secret-challenge", "token", "sensitive-model-error"} {
				if strings.Contains(output.String(), secret) {
					t.Errorf("log contains sensitive value %q: %s", secret, output.String())
				}
			}
		})
	}
}
