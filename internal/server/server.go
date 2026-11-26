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
	v4Flows       V4FlowFactory
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
	s := &Server{predictor: options.Predictor, flows: options.Flows, v4Flows: options.V4Flows, models: options.Models,
		store: options.Store, level: level, verifyDelay: options.VerifyDelay, disableDelays: options.DisableDelays}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /pass_uni", s.passHandler(false))
	s.mux.HandleFunc("GET /pass_nine", s.passHandler(false))
	s.mux.HandleFunc("GET /pass_icon", s.passHandler(false))
	s.mux.HandleFunc("GET /pass_hutao", s.passHandler(true))
	s.mux.HandleFunc("GET /pass_v4", s.passV4Handler())
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
	status          int
	body            json.RawMessage
	pictureType     string
	verifyStatus    string
	verifyResult    string
	payloadProtocol int
	passed          bool
}

type passResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *passResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *passResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (s *Server) passHandler(hutao bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		writer := &passResponseWriter{ResponseWriter: w}
		w = writer
		logResult := response{}
		defer func() {
			logResult.status = writer.status
			if logResult.status == 0 {
				logResult.status = http.StatusInternalServerError
			}
			logPassResult(r.Context(), r.URL.Path, logResult, time.Since(started))
		}()

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
		logResult = result
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

func (s *Server) passV4Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		writer := &passResponseWriter{ResponseWriter: w}
		w = writer
		logResult := response{}
		defer func() {
			logResult.status = writer.status
			if logResult.status == 0 {
				logResult.status = http.StatusInternalServerError
			}
			logPassResult(r.Context(), r.URL.Path, logResult, time.Since(started))
		}()
		challenge, ok := requiredV4Challenge(w, r)
		if !ok {
			return
		}
		saveResult, valid := optionalBool(w, r, "save_result", false)
		if !valid {
			return
		}
		result := s.handlePassV4(r.Context(), challenge, saveResult)
		logResult = result
		writeRaw(w, result.status, result.body)
	}
}

func requiredV4Challenge(w http.ResponseWriter, r *http.Request) (V4Challenge, bool) {
	var challenge V4Challenge
	query := r.URL.Query()
	challenge.CaptchaID = query.Get("captcha_id")
	if challenge.CaptchaID == "" {
		challenge.CaptchaID = query.Get("gt")
	}
	if challenge.CaptchaID == "" {
		validationError(w, "captcha_id", "Field required")
		return challenge, false
	}
	challenge.ClientType = query.Get("client_type")
	challenge.RiskType = query.Get("risk_type")
	challenge.Lang = query.Get("lang")
	voice, valid := optionalBool(w, r, "voice", false)
	if !valid {
		return challenge, false
	}
	challenge.Voice = voice
	return challenge, true
}

func (s *Server) handlePassV4(ctx context.Context, challenge V4Challenge, saveResult bool) (result response) {
	started := time.Now()
	pictureType := ""
	verifyStatus := ""
	verifyResult := ""
	payloadProtocol := 0
	passed := false
	defer func() {
		result.pictureType = pictureType
		result.verifyStatus = verifyStatus
		result.verifyResult = verifyResult
		result.payloadProtocol = payloadProtocol
		result.passed = passed
	}()

	if s.v4Flows == nil || s.store == nil {
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
	flow, err := s.v4Flows(challenge)
	if err != nil {
		return errorResponse(err)
	}
	defer flow.Close()
	puzzle, err := flow.Load(ctx)
	if err != nil {
		return errorResponse(err)
	}
	pictureType = puzzle.Kind
	payloadProtocol = puzzle.PayloadProtocol
	var assets []string
	if saveResult {
		for i, asset := range puzzle.Assets {
			name, nameErr := storage.FilenameFromResource(asset.Name)
			if nameErr != nil {
				return errorResponse(nameErr)
			}
			if i == 0 {
				err = workspace.WriteOriginal(name, asset.Data)
			} else {
				err = workspace.WriteArtifact(name, asset.Data)
			}
			if err != nil {
				return errorResponse(err)
			}
			assets = append(assets, name)
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
	raw, proofPassed, err := flow.Submit(ctx)
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
	verifyStatus, _ = decoded["status"].(string)
	if data, ok := decoded["data"].(map[string]any); ok {
		verifyResult, _ = data["result"].(string)
	}
	passed = proofPassed
	if saveResult && len(assets) > 0 {
		if err := workspace.CommitFiles(passed, assets[1:]); err != nil {
			return errorResponse(err)
		}
	}
	return response{status: 200, body: append(json.RawMessage(nil), raw...)}
}

func (s *Server) handlePass(ctx context.Context, gt, challenge string, saveResult, useV3 bool, point *float64) (result response) {
	started := time.Now()
	pictureType := ""
	verifyStatus := ""
	verifyResult := ""
	passed := false
	defer func() {
		result.pictureType = pictureType
		result.verifyStatus = verifyStatus
		result.verifyResult = verifyResult
		result.passed = passed
	}()

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
	pictureType = picture.Type
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
	verifyStatus, verifyResult, passed = verifyOutcome(decoded)
	if saveResult {
		data, _ := decoded["data"].(map[string]any)
		_, savePassed := data["validate"]
		if err := workspace.Commit(picture.Type, savePassed); err != nil {
			return errorResponse(err)
		}
	}
	return response{status: 200, body: append(json.RawMessage(nil), raw...)}
}

func verifyOutcome(decoded map[string]any) (status, result string, passed bool) {
	status, _ = decoded["status"].(string)
	data, ok := decoded["data"].(map[string]any)
	if !ok {
		return status, "", false
	}
	result, _ = data["result"].(string)
	if status == "success" {
		_, passed = data["validate"]
	}
	return status, result, passed
}

func modelGroupForPicture(pictureType string) string {
	switch pictureType {
	case "nine":
		return "pdl"
	case "icon":
		return "multi"
	case "icon1":
		return "dfine"
	case "slide":
		return "slide"
	case "grid":
		return "grid"
	case "click":
		return "click"
	case "match", "gomoku":
		return "board"
	case "voice":
		return "voice"
	case "silent":
		return "silent"
	default:
		return "unknown"
	}
}

func logPassResult(ctx context.Context, route string, result response, duration time.Duration) {
	passed := result.passed && result.status == http.StatusOK
	level := slog.LevelInfo
	outcome := "success"
	switch {
	case result.status >= http.StatusInternalServerError:
		level = slog.LevelError
		outcome = "error"
	case result.status >= http.StatusBadRequest:
		level = slog.LevelWarn
		outcome = "rejected"
	case !passed:
		level = slog.LevelWarn
		outcome = "failed"
	}
	attrs := []any{
		"route", route,
		"http_status", result.status,
		"duration", duration,
		"picture_type", result.pictureType,
		"model_group", modelGroupForPicture(result.pictureType),
		"verify_status", result.verifyStatus,
		"verify_result", result.verifyResult,
		"passed", passed,
		"outcome", outcome,
	}
	if result.payloadProtocol > 0 {
		attrs = append(attrs, "payload_protocol", result.payloadProtocol)
	}
	slog.Log(ctx, level, "captcha request completed", attrs...)
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
