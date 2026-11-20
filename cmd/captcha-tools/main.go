package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kafuneri/captcha-tools-go/internal/config"
	"github.com/kafuneri/captcha-tools-go/internal/geetest"
	"github.com/kafuneri/captcha-tools-go/internal/inference"
	"github.com/kafuneri/captcha-tools-go/internal/logging"
	"github.com/kafuneri/captcha-tools-go/internal/server"
	"github.com/kafuneri/captcha-tools-go/internal/storage"
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

func main() {
	healthcheck := flag.Bool("healthcheck", false, "query the local /healthz endpoint and exit")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	if *healthcheck {
		if err := checkHealth(cfg.Port); err != nil {
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
	store, err := storage.New(cfg.WorkDir, cfg.SaveDir)
	if err != nil {
		logger.Error("initialize storage failed", "error", err)
		os.Exit(1)
	}
	registry := inference.NewRegistry(cfg, logger)
	if len(cfg.Preload) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		err = registry.Preload(ctx, cfg.Preload)
		cancel()
		if err != nil {
			logger.Error("model preload failed", "error", err)
			os.Exit(1)
		}
	}
	flows := func(gt, challenge string) (server.Flow, error) {
		client, err := geetest.NewClient(gt, challenge, geetest.Config{Timeout: cfg.HTTPTimeout, MaxResponseSize: cfg.MaxImageBytes})
		if err != nil {
			return nil, err
		}
		return &flowAdapter{client: client}, nil
	}
	handler := server.New(server.Options{Predictor: registry, Flows: flows, Models: registry, Store: store}, level)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(cfg.Port), Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		logger.Info("captcha Go service listening", "address", "http://0.0.0.0:"+strconv.Itoa(cfg.Port), "preload", cfg.Preload, "idle_timeout", cfg.ModelIdleTimeout)
		errCh <- httpServer.ListenAndServe()
	}()
	select {
	case <-signalCtx.Done():
		logger.Info("shutdown signal received")
	case err = <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	err = errors.Join(err, httpServer.Shutdown(shutdownCtx))
	if shutdownCtx.Err() != nil {
		err = errors.Join(err, httpServer.Close())
	}
	cancel()
	modelCtx, modelCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	if closeErr := registry.Close(modelCtx); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	modelCancel()
	if destroyErr := inference.DestroyEnvironment(); destroyErr != nil {
		err = errors.Join(err, destroyErr)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("service stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("captcha Go service stopped")
}

func checkHealth(port int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
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
