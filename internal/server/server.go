package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kafuneri/captcha-tools-go/internal/logging"
	"github.com/kafuneri/captcha-tools-go/internal/storage"
)

type Server struct {
	predictor     Predictor
	flows         FlowFactory
	models        ModelController
	store         *storage.Store
	level         *slog.LevelVar
	verifyDelay   time.Duration
	disableDelays bool
	mux           *http.ServeMux
}

func New(options Options, level *slog.LevelVar) *Server {
	if options.VerifyDelay == 0 {
		options.VerifyDelay = 4 * time.Second
	}
	s := &Server{predictor: options.Predictor, flows: options.Flows, models: options.Models,
		store: options.Store, level: level, verifyDelay: options.VerifyDelay, disableDelays: options.DisableDelays}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /pass_uni", s.passHandler(false))
	s.mux.HandleFunc("GET /pass_nine", s.passHandler(false))
	s.mux.HandleFunc("GET /pass_icon", s.passHandler(false))
	s.mux.HandleFunc("GET /pass_hutao", s.passHandler(true))
	s.mux.HandleFunc("GET /list_model", s.listModel)
	s.mux.HandleFunc("GET /list_all_model", s.listAllModel)
	s.mux.HandleFunc("GET /load_model", s.loadModel)
	s.mux.HandleFunc("GET /unload_model", s.unloadModel)
	s.mux.HandleFunc("GET /set_log_level", s.setLogLevel)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

type response struct {
	status int
	body   json.RawMessage
}

func (s *Server) passHandler(hutao bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gt, challenge, ok := requiredGTChallenge(w, r)
		if !ok {
			return
		}
		saveResult, valid := optionalBool(w, r, "save_result", false)
		if !valid {
			return
		}
		useV3, valid := optionalBool(w, r, "use_v3_model", true)
		if !valid {
			return
		}
		var point *float64
		if values, exists := r.URL.Query()["point"]; exists {
			if len(values) != 1 {
				validationError(w, "point", "Input should be a valid number")
				return
			}
			value, err := strconv.ParseFloat(values[0], 64)
			if err != nil {
				validationError(w, "point", "Input should be a valid number")
				return
			}
			point = &value
		}
		result := s.handlePass(r.Context(), gt, challenge, saveResult, useV3, point)
		if !hutao {
			writeRaw(w, result.status, result.body)
			return
		}
		var original map[string]any
		_ = json.Unmarshal(result.body, &original)
		success := false
		var validate any
		if result.status == 200 && original != nil && original["status"] == "success" {
			if data, ok := original["data"].(map[string]any); ok {
				validate, success = data["validate"]
			}
		}
		if success {
			writeJSON(w, result.status, map[string]any{"code": 0, "data": map[string]any{"gt": gt, "challenge": challenge, "validate": validate}})
		} else {
			writeJSON(w, result.status, map[string]any{"code": 1, "data": map[string]any{"gt": gt, "challenge": challenge, "validate": original}})
		}
	}
}

func (s *Server) handlePass(ctx context.Context, gt, challenge string, saveResult, useV3 bool, point *float64) (result response) {
	started := time.Now()
	if s.predictor == nil || s.flows == nil || s.store == nil {
		return errorResponse(fmt.Errorf("service is not fully configured"))
	}
	workspace, err := s.store.Begin()
	if err != nil {
		return errorResponse(err)
	}
	defer func() {
		if cleanupErr := workspace.Cleanup(); cleanupErr != nil {
			result = errorResponse(fmt.Errorf("clean request workspace: %w", cleanupErr))
		}
	}()
	flow, err := s.flows(gt, challenge)
	if err != nil {
		return errorResponse(err)
	}
	defer flow.Close()
	picture, err := flow.Prepare(ctx)
	if err != nil {
		return errorResponse(err)
	}
	name, err := storage.FilenameFromResource(picture.Name)
	if err != nil {
		return errorResponse(err)
	}
	if err := workspace.WriteOriginal(name, picture.Data); err != nil {
		return errorResponse(err)
	}
	var points []string
	var artifacts []Artifact
	switch picture.Type {
	case "nine":
		predicted, predictErr := s.predictor.Nine(ctx, picture.Data, useV3, point)
		if predictErr != nil {
			return errorResponse(predictErr)
		}
		for _, cell := range predicted.Cells {
			points = append(points, fmt.Sprintf("%d_%d", cell[1], cell[0]))
		}
		artifacts = predicted.Artifacts
	case "icon":
		predicted, predictErr := s.predictor.Multi(ctx, picture.Data, saveResult)
		if predictErr != nil {
			return errorResponse(predictErr)
		}
		points = normalizedPoints(predicted.Points)
		artifacts = predicted.Artifacts
	case "icon1":
		predicted, predictErr := s.predictor.DFINE(ctx, picture.Data, saveResult)
		if predictErr != nil {
			return errorResponse(predictErr)
		}
		points = normalizedPoints(predicted.Points)
		artifacts = predicted.Artifacts
	default:
		return errorResponse(fmt.Errorf("Unknown picture type: %s", picture.Type))
	}
	for _, artifact := range artifacts {
		if err := workspace.WriteArtifact(artifact.Name, artifact.Data); err != nil {
			return errorResponse(err)
		}
	}
	if !s.disableDelays {
		remaining := s.verifyDelay - time.Since(started)
		if remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return errorResponse(ctx.Err())
			case <-timer.C:
			}
		}
	}
	raw, err := flow.Verify(ctx, points)
	if err != nil {
		return errorResponse(err)
	}
	if !json.Valid(raw) {
		return errorResponse(fmt.Errorf("invalid verify JSON response"))
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return errorResponse(err)
	}
	if decoded == nil {
		return errorResponse(fmt.Errorf("verify response must be a JSON object"))
	}
	if saveResult {
		data, _ := decoded["data"].(map[string]any)
		_, passed := data["validate"]
		if err := workspace.Commit(picture.Type, passed); err != nil {
			return errorResponse(err)
		}
	}
	return response{status: 200, body: append(json.RawMessage(nil), raw...)}
}

func normalizedPoints(points [][2]float64) []string {
	result := make([]string, 0, len(points))
	for _, point := range points {
		result = append(result, fmt.Sprintf("%d_%d", int(math.RoundToEven(point[0]/333*10000)), int(math.RoundToEven(point[1]/333*10000))))
	}
	return result
}

func (s *Server) listModel(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.models.ListModels())
}
func (s *Server) listAllModel(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.models.ListAllModels())
}
func (s *Server) loadModel(w http.ResponseWriter, r *http.Request) {
	name, ok := requiredQuery(w, r, "name")
	if !ok {
		return
	}
	result, err := s.models.LoadModel(r.Context(), name)
	if err != nil {
		writeRaw(w, 500, errorResponse(err).body)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) unloadModel(w http.ResponseWriter, r *http.Request) {
	name, ok := requiredQuery(w, r, "name")
	if !ok {
		return
	}
	result, err := s.models.UnloadModel(r.Context(), name)
	if err != nil {
		writeRaw(w, 500, errorResponse(err).body)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) setLogLevel(w http.ResponseWriter, r *http.Request) {
	value, ok := requiredQuery(w, r, "level")
	if !ok {
		return
	}
	level, err := logging.ParseLevel(value)
	if err != nil {
		writeJSON(w, 400, map[string]string{"detail": fmt.Sprintf("无效的日志级别: %s", value)})
		return
	}
	if s.level != nil {
		s.level.Set(level)
	}
	writeJSON(w, 200, "Log level successfully set to "+strings.ToUpper(value))
}

func requiredGTChallenge(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	gt, ok := requiredQuery(w, r, "gt")
	if !ok {
		return "", "", false
	}
	challenge, ok := requiredQuery(w, r, "challenge")
	if !ok {
		return "", "", false
	}
	return gt, challenge, true
}
func requiredQuery(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	values, exists := r.URL.Query()[name]
	if !exists || len(values) == 0 {
		validationError(w, name, "Field required")
		return "", false
	}
	return values[0], true
}
func optionalBool(w http.ResponseWriter, r *http.Request, name string, defaultValue bool) (bool, bool) {
	values, exists := r.URL.Query()[name]
	if !exists {
		return defaultValue, true
	}
	if len(values) != 1 {
		validationError(w, name, "Input should be a valid boolean")
		return false, false
	}
	switch strings.ToLower(values[0]) {
	case "1", "true", "t", "yes", "y", "on":
		return true, true
	case "0", "false", "f", "no", "n", "off":
		return false, true
	default:
		validationError(w, name, "Input should be a valid boolean")
		return false, false
	}
}
func validationError(w http.ResponseWriter, field, message string) {
	kind := "missing"
	if message != "Field required" {
		kind = "value_error"
	}
	writeJSON(w, 422, map[string]any{"detail": []any{map[string]any{"type": kind, "loc": []string{"query", field}, "msg": message, "input": nil}}})
}
func errorResponse(err error) response {
	body, _ := json.Marshal(map[string]string{"error": "An internal server error occurred.", "detail": err.Error()})
	return response{status: 500, body: body}
}
func writeRaw(w http.ResponseWriter, status int, body json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeRaw(w, 500, errorResponse(err).body)
		return
	}
	writeRaw(w, status, body)
}
