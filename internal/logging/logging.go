package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "DEBUG":
		return slog.LevelDebug, nil
	case "INFO":
		return slog.LevelInfo, nil
	case "WARN", "WARNING":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q", value)
	}
}

func New(value, directory string) (*slog.Logger, *slog.LevelVar, io.Closer, error) {
	level, err := ParseLevel(value)
	if err != nil {
		return nil, nil, nil, err
	}
	files, err := newDailyWriter(directory)
	if err != nil {
		return nil, nil, nil, err
	}
	var variable slog.LevelVar
	variable.Set(level)
	handler := slog.NewTextHandler(io.MultiWriter(os.Stderr, files), &slog.HandlerOptions{Level: &variable})
	return slog.New(handler), &variable, files, nil
}

type dailyWriter struct {
	mu     sync.Mutex
	dir    string
	file   *os.File
	date   string
	closed bool
}

func newDailyWriter(directory string) (*dailyWriter, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	writer := &dailyWriter{dir: directory}
	if err := writer.openLocked(time.Now()); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *dailyWriter) openLocked(now time.Time) error {
	file, err := os.OpenFile(filepath.Join(w.dir, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.file, w.date = file, now.Format("2006-01-02")
	return nil
}

func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	now := time.Now()
	if now.Format("2006-01-02") != w.date {
		oldDate := w.date
		if err := w.file.Close(); err != nil {
			return 0, err
		}
		if err := os.Rename(filepath.Join(w.dir, "app.log"), filepath.Join(w.dir, "app.log."+oldDate)); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		if err := w.openLocked(now); err != nil {
			return 0, err
		}
		w.cleanupLocked()
	}
	return w.file.Write(p)
}

func (w *dailyWriter) cleanupLocked() {
	matches, _ := filepath.Glob(filepath.Join(w.dir, "app.log.????-??-??"))
	sort.Strings(matches)
	for len(matches) > 2 {
		_ = os.Remove(matches[0])
		matches = matches[1:]
	}
}

func (w *dailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
