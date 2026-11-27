package main

import (
	"context"
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
	"github.com/kafuneri/captcha-tools-go/internal/gateway"
	"github.com/kafuneri/captcha-tools-go/internal/logging"
)

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
	logger, _, logCloser, err := logging.New(cfg.LogLevel, cfg.LogDir)
	if err != nil {
		fatal(err)
	}
	defer logCloser.Close()
	slog.SetDefault(logger)

	binary := os.Getenv("WORKER_BINARY")
	if binary == "" {
		executable, err := os.Executable()
		if err != nil {
			fatal(err)
		}
		binary = filepath.Join(filepath.Dir(executable), "captcha-tools")
	}
	if _, err := os.Stat(binary); err != nil {
		fatal(fmt.Errorf("worker binary %q: %w", binary, err))
	}
	g, err := gateway.New(gateway.Options{
		Mode:           cfg.Mode,
		WorkerBinary:   binary,
		SocketDir:      os.TempDir(),
		IdleTimeout:    cfg.WorkerIdleTimeout,
		StartTimeout:   cfg.WorkerStartTimeout,
		RequestTimeout: cfg.WorkerRequestTimeout,
		PreloadV3:      cfg.PreloadV3,
		PreloadV4:      cfg.PreloadV4,
		Logger:         logger,
	})
	if err != nil {
		fatal(err)
	}

	httpServer := &http.Server{Addr: ":" + strconv.Itoa(cfg.Port), Handler: g, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		logger.Info("captcha gateway listening",
			"address", "http://0.0.0.0:"+strconv.Itoa(cfg.Port),
			"mode", cfg.Mode,
			"worker_idle_timeout", cfg.WorkerIdleTimeout,
		)
		errCh <- httpServer.ListenAndServe()
	}()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.WorkerStartTimeout)
		defer cancel()
		if err := g.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("start V3 worker failed", "error", err)
		}
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
	workerCtx, workerCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	err = errors.Join(err, g.Close(workerCtx))
	workerCancel()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("gateway stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("captcha gateway stopped")
}

func checkHealth(port int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/healthz", nil)
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
