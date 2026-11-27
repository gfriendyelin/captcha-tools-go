// Package gateway owns the public HTTP listener and the lifecycle of the
// mode-specific model workers. Workers are separate OS processes so that
// exiting a worker returns all ONNX Runtime memory to the operating system.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kafuneri/captcha-tools-go/internal/config"
)

type Options struct {
	Mode           config.Mode
	WorkerBinary   string
	SocketDir      string
	IdleTimeout    time.Duration
	StartTimeout   time.Duration
	RequestTimeout time.Duration
	PreloadV3      []string
	PreloadV4      []string
	Logger         *slog.Logger
}

type Gateway struct {
	mode    config.Mode
	workers map[string]*worker
	logger  *slog.Logger
	mux     *http.ServeMux
}

func New(options Options) (*Gateway, error) {
	if options.Mode == "" {
		options.Mode = config.ModeAll
	}
	if options.WorkerBinary == "" {
		options.WorkerBinary = "captcha-tools"
	}
	if options.SocketDir == "" {
		options.SocketDir = os.TempDir()
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.PreloadV3 == nil {
		options.PreloadV3 = []string{"pdl", "dfine"}
	}
	if options.PreloadV4 == nil {
		options.PreloadV4 = []string{}
	}
	g := &Gateway{mode: options.Mode, workers: map[string]*worker{}, logger: options.Logger}
	for _, mode := range []string{"v3", "v4"} {
		if !modeEnabled(options.Mode, mode) {
			continue
		}
		socket := filepath.Join(options.SocketDir, fmt.Sprintf("captcha-%s-%d.sock", mode, os.Getpid()))
		preload := options.PreloadV3
		if mode == "v4" {
			preload = options.PreloadV4
		}
		g.workers[mode] = newWorker(mode, options.WorkerBinary, socket, preload, options)
	}
	g.mux = http.NewServeMux()
	g.mux.HandleFunc("GET /pass_uni", g.handleUnified)
	g.mux.HandleFunc("GET /pass_nine", g.handleV3)
	g.mux.HandleFunc("GET /pass_icon", g.handleV3)
	g.mux.HandleFunc("GET /pass_hutao", g.handleV3)
	g.mux.HandleFunc("GET /pass_v4", g.handleV4)
	g.mux.HandleFunc("GET /list_model", g.handleListModel)
	g.mux.HandleFunc("GET /list_all_model", g.handleListAllModel)
	g.mux.HandleFunc("GET /load_model", g.handleLoadModel)
	g.mux.HandleFunc("GET /unload_model", g.handleUnloadModel)
	g.mux.HandleFunc("GET /set_log_level", g.handleSetLogLevel)
	g.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "mode": g.mode})
	})
	g.mux.HandleFunc("GET /readyz", g.handleReady)
	return g, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) { g.mux.ServeHTTP(w, r) }

// Start eagerly launches the V3 worker, which is configured to preload PDL and
// D-FINE. V4 starts on its first request and preloads nothing by default.
func (g *Gateway) Start(ctx context.Context) error {
	if worker := g.workers["v3"]; worker != nil {
		if err := worker.ensure(ctx); err != nil {
			return fmt.Errorf("start v3 worker: %w", err)
		}
	}
	return nil
}

func (g *Gateway) Close(ctx context.Context) error {
	var errs []error
	for _, worker := range g.workers {
		if err := worker.stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("stop workers: %v", errs)
	}
	return nil
}

func (g *Gateway) handleUnified(w http.ResponseWriter, r *http.Request) {
	version, err := resolveVersion(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": err.Error()})
		return
	}
	g.proxyVersion(w, r, version)
}

func (g *Gateway) handleV3(w http.ResponseWriter, r *http.Request) { g.proxyVersion(w, r, "v3") }
func (g *Gateway) handleV4(w http.ResponseWriter, r *http.Request) { g.proxyVersion(w, r, "v4") }

func (g *Gateway) proxyVersion(w http.ResponseWriter, r *http.Request, version string) {
	worker := g.workers[version]
	if worker == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": version + " is not enabled by MODE"})
		return
	}
	worker.proxy(w, r)
}

func (g *Gateway) handleListModel(w http.ResponseWriter, r *http.Request) {
	g.handleList(w, r, false)
}

func (g *Gateway) handleListAllModel(w http.ResponseWriter, r *http.Request) {
	g.handleList(w, r, true)
}

func (g *Gateway) handleList(w http.ResponseWriter, r *http.Request, all bool) {
	version := normalizeVersion(r.URL.Query().Get("version"))
	if version == "" {
		version = g.defaultVersion()
	}
	if version == "all" {
		result := map[string]any{}
		for name, worker := range g.workers {
			items, err := worker.list(r.Context(), all)
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
				return
			}
			result[name] = items
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	if version != "v3" && version != "v4" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "version must be 3, 4 or all"})
		return
	}
	worker := g.workers[version]
	if worker == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": version + " is not enabled by MODE"})
		return
	}
	items, err := worker.list(r.Context(), all)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (g *Gateway) handleLoadModel(w http.ResponseWriter, r *http.Request) {
	g.proxyManagement(w, r, true)
}

func (g *Gateway) handleUnloadModel(w http.ResponseWriter, r *http.Request) {
	g.proxyManagement(w, r, false)
}

func (g *Gateway) handleSetLogLevel(w http.ResponseWriter, r *http.Request) {
	g.proxyManagement(w, r, true)
}

func (g *Gateway) proxyManagement(w http.ResponseWriter, r *http.Request, start bool) {
	version := normalizeVersion(r.URL.Query().Get("version"))
	if version == "" {
		version = g.defaultVersion()
	}
	worker := g.workers[version]
	if worker == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": version + " is not enabled by MODE"})
		return
	}
	if start {
		worker.proxy(w, r)
		return
	}
	if !worker.running() {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	worker.proxy(w, r)
}

func (g *Gateway) defaultVersion() string {
	if g.mode == config.ModeV4 {
		return "v4"
	}
	return "v3"
}

func normalizeVersion(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "3", "v3":
		return "v3"
	case "4", "v4":
		return "v4"
	case "all":
		return "all"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func (g *Gateway) handleReady(w http.ResponseWriter, _ *http.Request) {
	status := map[string]bool{}
	ready := true
	for name, worker := range g.workers {
		running := worker.running()
		status[name] = running
		if !running {
			ready = false
		}
	}
	code := http.StatusOK
	if !ready {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"ready": ready, "workers": status})
}

type worker struct {
	mode      string
	binary    string
	socket    string
	preload   []string
	idle      time.Duration
	start     time.Duration
	timeout   time.Duration
	logger    *slog.Logger
	transport *http.Transport
	client    *http.Client

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	starting bool
	ready    chan struct{}
	startErr error
	closing  bool
}

func newWorker(mode, binary, socket string, preload []string, options Options) *worker {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:       16,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
	}
	return &worker{
		mode: mode, binary: binary, socket: socket, preload: append([]string(nil), preload...),
		idle: options.IdleTimeout, start: options.StartTimeout, timeout: options.RequestTimeout,
		logger: options.Logger, transport: transport,
		client: &http.Client{Transport: transport, Timeout: options.RequestTimeout},
	}
}

func (w *worker) ensure(ctx context.Context) error {
	for {
		w.mu.Lock()
		if w.closing {
			w.mu.Unlock()
			return fmt.Errorf("gateway is shutting down")
		}
		if w.cmd != nil {
			w.mu.Unlock()
			return nil
		}
		if w.starting {
			ready := w.ready
			w.mu.Unlock()
			select {
			case <-ready:
				w.mu.Lock()
				err := w.startErr
				w.mu.Unlock()
				if err != nil {
					return err
				}
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		w.starting = true
		w.ready = make(chan struct{})
		w.startErr = nil
		w.mu.Unlock()

		err := w.spawn(ctx)

		w.mu.Lock()
		w.starting = false
		w.startErr = err
		close(w.ready)
		w.mu.Unlock()
		return err
	}
}

func (w *worker) spawn(parent context.Context) error {
	_ = os.Remove(w.socket)
	cmd := exec.Command(w.binary, "-worker")
	cmd.Env = envWith(os.Environ(), map[string]string{
		"MODE":                w.mode,
		"WORKER_SOCKET":       w.socket,
		"WORKER_IDLE_TIMEOUT": w.idle.String(),
		"MODEL_PRELOAD":       strings.Join(w.preload, ","),
	})
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s worker: %w", w.mode, err)
	}
	done := make(chan struct{})
	w.mu.Lock()
	w.cmd = cmd
	w.done = done
	w.mu.Unlock()

	go func() {
		err := cmd.Wait()
		w.mu.Lock()
		if w.cmd == cmd {
			w.cmd = nil
		}
		close(done)
		w.mu.Unlock()
		if err != nil {
			w.logger.Warn("worker exited", "mode", w.mode, "error", err)
		} else {
			w.logger.Info("worker exited", "mode", w.mode)
		}
	}()

	startCtx := parent
	cancel := func() {}
	if _, ok := parent.Deadline(); !ok {
		startCtx, cancel = context.WithTimeout(parent, w.start)
	}
	defer cancel()
	if err := waitSocket(startCtx, w.socket, done); err != nil {
		_ = cmd.Process.Kill()
		<-done
		return fmt.Errorf("wait %s worker: %w", w.mode, err)
	}
	w.logger.Info("worker ready", "mode", w.mode, "socket", w.socket)
	return nil
}

func waitSocket(ctx context.Context, socket string, done <-chan struct{}) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return fmt.Errorf("worker exited before ready")
		case <-ticker.C:
			conn, err := net.DialTimeout("unix", socket, 200*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}

func (w *worker) running() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cmd != nil
}

func (w *worker) stop(ctx context.Context) error {
	w.mu.Lock()
	w.closing = true
	if w.starting {
		ready := w.ready
		w.mu.Unlock()
		select {
		case <-ready:
		case <-ctx.Done():
			return ctx.Err()
		}
		w.mu.Lock()
	}
	cmd := w.cmd
	done := w.done
	w.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return ctx.Err()
	}
}

func (w *worker) proxy(wr http.ResponseWriter, r *http.Request) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := w.ensure(r.Context()); err != nil {
			writeJSON(wr, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		req := r.Clone(r.Context())
		req.RequestURI = ""
		req.URL.Scheme = "http"
		req.URL.Host = "unix"
		resp, err := w.client.Do(req)
		if err == nil {
			copyResponse(wr, resp)
			return
		}
		if r.Context().Err() != nil {
			writeJSON(wr, http.StatusGatewayTimeout, map[string]string{"detail": r.Context().Err().Error()})
			return
		}
		if attempt == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		writeJSON(wr, http.StatusBadGateway, map[string]string{"detail": err.Error()})
	}
}

func (w *worker) list(ctx context.Context, all bool) (any, error) {
	if !w.running() {
		return []string{}, nil
	}
	path := "/list_model"
	if all {
		path = "/list_all_model"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("worker returned %s", resp.Status)
	}
	var value any
	if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func envWith(base []string, overrides map[string]string) []string {
	keys := map[string]bool{}
	for key := range overrides {
		keys[strings.ToUpper(key)] = true
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key := item
		if index := strings.IndexByte(item, '='); index >= 0 {
			key = item[:index]
		}
		if keys[strings.ToUpper(key)] {
			continue
		}
		result = append(result, item)
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func modeEnabled(mode config.Mode, version string) bool {
	switch version {
	case "v3":
		return mode.IncludesV3()
	case "v4":
		return mode.IncludesV4()
	default:
		return false
	}
}
