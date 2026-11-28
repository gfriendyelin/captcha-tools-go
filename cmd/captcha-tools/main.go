package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/kafuneri/captcha-tools-go/internal/config"
	"github.com/kafuneri/captcha-tools-go/internal/geetest"
	"github.com/kafuneri/captcha-tools-go/internal/inference"
	"github.com/kafuneri/captcha-tools-go/internal/logging"
	"github.com/kafuneri/captcha-tools-go/internal/procmon"
	"github.com/kafuneri/captcha-tools-go/internal/server"
	"github.com/kafuneri/captcha-tools-go/internal/solver"
	"github.com/kafuneri/captcha-tools-go/internal/storage"
	"github.com/kafuneri/captcha-tools-go/internal/v4"
	"github.com/kafuneri/captcha-tools-go/internal/worker"
)

type flowAdapter struct{ client *geetest.Client }

func (f *flowAdapter) Prepare(ctx context.Context) (server.Picture, error) {
	picture, err := f.client.Prepare(ctx)
	if err != nil {
		return server.Picture{}, err
	}
	return server.Picture{Data: picture.Content, Name: picture.Name, Type: picture.PicType}, nil
}
func (f *flowAdapter) Verify(ctx context.Context, points []string) (json.RawMessage, error) {
	response, err := f.client.Verify(ctx, points)
	if err != nil {
		return nil, err
	}
	return response.Raw, nil
}
func (f *flowAdapter) Close() error { f.client.Close(); return nil }

// v4FlowAdapter binds one V4 protocol session to the recognition registry. A
// retryable failure re-opens the session and tries again with a fresh puzzle.
type v4FlowAdapter struct {
	client    *v4.Client
	registry  *inference.Registry
	challenge v4.Challenge
	session   *v4.Session
}

const (
	// maxSolveAttempts bounds one V4 solve: network/5xx errors, a rotated
	// protocol key and a non-passing verification all get a fresh challenge,
	// while an unsupported build or a 4xx upstream answer is returned at once.
	maxSolveAttempts = 3
	retryInterval    = 150 * time.Millisecond
)

// retryableV4 reports whether opening a fresh session could fix the error.
func retryableV4(err error) bool {
	var protocolErr *v4.Error
	return errors.As(err, &protocolErr) && protocolErr.Retryable
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *v4FlowAdapter) Load(ctx context.Context) (server.V4Puzzle, error) {
	var lastErr error
	for attempt := 0; attempt < maxSolveAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepContext(ctx, retryInterval*time.Duration(attempt)); err != nil {
				return server.V4Puzzle{}, err
			}
		}
		session, err := f.client.Open(ctx, f.challenge)
		if err == nil {
			f.session = session
			return v4Puzzle(session), nil
		}
		lastErr = err
		if !retryableV4(err) || ctx.Err() != nil {
			return server.V4Puzzle{}, err
		}
	}
	return server.V4Puzzle{}, lastErr
}

func v4Puzzle(session *v4.Session) server.V4Puzzle {
	puzzle := session.Puzzle()
	saved := puzzle.SaveAssets()
	assets := make([]server.Artifact, len(saved))
	for i, asset := range saved {
		assets[i] = server.Artifact{Name: asset.Name, Data: asset.Data}
	}
	return server.V4Puzzle{Kind: puzzle.Kind, Assets: assets, PayloadProtocol: session.PT()}
}

func (f *v4FlowAdapter) Submit(ctx context.Context) (json.RawMessage, bool, error) {
	var lastRaw json.RawMessage
	var lastPassed bool
	var lastErr error
	for attempt := 0; attempt < maxSolveAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepContext(ctx, retryInterval*time.Duration(attempt)); err != nil {
				return nil, false, err
			}
			session, openErr := f.client.Open(ctx, f.challenge)
			if openErr != nil {
				return nil, false, errors.Join(lastErr, openErr)
			}
			f.session = session
		}
		answer, err := solver.V4(ctx, f.registry, f.session.Puzzle())
		if err != nil {
			return nil, false, err
		}
		raw, passed, err := f.session.Submit(ctx, answer)
		if err == nil {
			lastRaw, lastPassed = raw, passed
			// A wrong answer is worth one more fresh challenge; the final
			// attempt is always returned so the caller sees the real result.
			if passed || attempt == maxSolveAttempts-1 {
				return raw, passed, nil
			}
			continue
		}
		lastErr = err
		if !retryableV4(err) || ctx.Err() != nil || attempt == maxSolveAttempts-1 {
			return nil, false, err
		}
	}
	return lastRaw, lastPassed, lastErr
}

func (f *v4FlowAdapter) Close() error { return nil }

type runtime struct {
	cfg      config.Config
	logger   *slog.Logger
	registry *inference.Registry
	v4Client *v4.Client
	handler  http.Handler
}

func main() {
	workerMode := flag.Bool("worker", false, "run a mode-specific worker on WORKER_SOCKET")
	healthcheck := flag.Bool("healthcheck", false, "query the local /healthz endpoint and exit")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	if *healthcheck {
		if err := checkHealth(cfg, *workerMode); err != nil {
			fatal(err)
		}
		return
	}
	logger, level, logCloser, err := logging.New(cfg.LogLevel, cfg.LogDir)
	if err != nil {
		fatal(err)
	}
	defer logCloser.Close()
	slog.SetDefault(logger)
	if *workerMode && cfg.Mode == config.ModeAll {
		fatal(fmt.Errorf("MODE must be v3 or v4 when -worker is used"))
	}
	if err := run(cfg, *workerMode, logger, level); err != nil {
		logger.Error("service stopped with error", "error", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, workerMode bool, logger *slog.Logger, level *slog.LevelVar) error {
	runtime, err := buildRuntime(cfg, logger, level)
	if err != nil {
		return err
	}
	defer runtime.close()

	isWorker := workerMode || cfg.WorkerSocket != ""
	var listener net.Listener
	var socketPath string
	if isWorker {
		socketPath = cfg.WorkerSocket
		if socketPath == "" {
			socketPath = filepath.Join(os.TempDir(), fmt.Sprintf("captcha-worker-%d.sock", os.Getpid()))
		}
		if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
			return fmt.Errorf("create worker socket directory: %w", err)
		}
		_ = os.Remove(socketPath)
		listener, err = net.Listen("unix", socketPath)
		if err != nil {
			return fmt.Errorf("listen worker socket: %w", err)
		}
		_ = os.Chmod(socketPath, 0o600)
		defer os.Remove(socketPath)
	} else {
		listener, err = net.Listen("tcp", ":"+strconv.Itoa(cfg.Port))
		if err != nil {
			return err
		}
	}

	var idleTracker *worker.IdleTracker
	handler := runtime.handler
	if isWorker && cfg.WorkerIdleTimeout > 0 {
		idleTracker = worker.NewIdleTracker(handler, cfg.WorkerIdleTimeout)
		handler = idleTracker
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}

	idleCh := make(chan struct{})
	trackerCtx, stopTracker := context.WithCancel(context.Background())
	defer stopTracker()
	if idleTracker != nil {
		go idleTracker.Run(trackerCtx, idleCh)
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		address := "http://0.0.0.0:" + strconv.Itoa(cfg.Port)
		if isWorker {
			address = "unix://" + socketPath
		}
		logger.Info("captcha Go service listening",
			"mode", cfg.Mode,
			"address", address,
			"preload", cfg.Preload,
			"idle_timeout", cfg.WorkerIdleTimeout,
		)
		errCh <- httpServer.Serve(listener)
	}()

	idled := false
	select {
	case <-signalCtx.Done():
		logger.Info("shutdown signal received")
	case err = <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
		}
	case <-idleCh:
		idled = true
		logger.Info("worker idle exit", "mode", cfg.Mode, "idle_timeout", cfg.WorkerIdleTimeout, "rss_bytes", rss())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	err = errors.Join(err, httpServer.Shutdown(shutdownCtx))
	if shutdownCtx.Err() != nil {
		err = errors.Join(err, httpServer.Close())
	}
	cancel()
	stopTracker()
	if idled {
		logger.Info("worker stopped after idle timeout", "mode", cfg.Mode)
	}
	return err
}

func buildRuntime(cfg config.Config, logger *slog.Logger, level *slog.LevelVar) (*runtime, error) {
	store, err := storage.New(cfg.WorkDir, cfg.SaveDir)
	if err != nil {
		return nil, fmt.Errorf("initialize storage: %w", err)
	}
	registry := inference.NewRegistry(cfg, logger)
	if len(cfg.Preload) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.WorkerStartTimeout)
		err = registry.Preload(ctx, cfg.Preload)
		cancel()
		if err != nil {
			_ = registry.Close(context.Background())
			return nil, fmt.Errorf("model preload failed: %w", err)
		}
	}

	rt := &runtime{cfg: cfg, logger: logger, registry: registry}
	var flows server.FlowFactory
	if cfg.Mode.IncludesV3() {
		flows = func(gt, challenge string) (server.Flow, error) {
			client, err := geetest.NewClient(gt, challenge, geetest.Config{Timeout: cfg.HTTPTimeout, MaxResponseSize: cfg.MaxImageBytes})
			if err != nil {
				return nil, err
			}
			return &flowAdapter{client: client}, nil
		}
	}
	var v4Flows server.V4FlowFactory
	if cfg.Mode.IncludesV4() {
		client, err := v4.NewClient(v4.Config{Timeout: cfg.HTTPTimeout, MaxResponseSize: cfg.MaxImageBytes})
		if err != nil {
			_ = registry.Close(context.Background())
			return nil, fmt.Errorf("initialize v4 client: %w", err)
		}
		rt.v4Client = client
		v4Flows = func(challenge server.V4Challenge) (server.V4Flow, error) {
			return &v4FlowAdapter{
				client:   client,
				registry: registry,
				challenge: v4.Challenge{
					CaptchaID:  challenge.CaptchaID,
					ClientType: challenge.ClientType,
					RiskType:   challenge.RiskType,
					Lang:       challenge.Lang,
					Voice:      challenge.Voice,
				},
			}, nil
		}
	}
	var predictor server.Predictor
	if cfg.Mode.IncludesV3() {
		predictor = registry
	}
	rt.handler = server.New(server.Options{
		Mode:          string(cfg.Mode),
		Predictor:     predictor,
		Flows:         flows,
		V4Flows:       v4Flows,
		Models:        registry,
		Store:         store,
		SaveResult:    cfg.SaveResult,
		DisableDelays: cfg.DisableDelays,
	}, level)
	return rt, nil
}

func (r *runtime) close() {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.ShutdownTimeout)
	if err := r.registry.Close(ctx); err != nil {
		r.logger.Error("close model registry failed", "error", err)
	}
	cancel()
	if r.v4Client != nil {
		r.v4Client.Close()
	}
	if err := inference.DestroyEnvironment(); err != nil {
		r.logger.Error("destroy ONNX environment failed", "error", err)
	}
}

func rss() uint64 {
	value, err := procmon.RSS()
	if err != nil {
		return 0
	}
	return value
}

func checkHealth(cfg config.Config, workerMode bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := http.DefaultClient
	url := "http://127.0.0.1:" + strconv.Itoa(cfg.Port) + "/healthz"
	if workerMode || cfg.WorkerSocket != "" {
		socket := cfg.WorkerSocket
		if socket == "" {
			socket = filepath.Join(os.TempDir(), fmt.Sprintf("captcha-worker-%d.sock", os.Getpid()))
		}
		client = &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socket)
			},
		}}
		url = "http://unix/healthz"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned %s", resp.Status)
	}
	return nil
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
