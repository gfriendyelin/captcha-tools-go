// Package worker contains the process-level lifecycle helpers used by the
// captcha worker. Model memory is reclaimed by exiting the worker process, not
// by unloading individual ONNX sessions.
package worker

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// IdleTracker counts in-flight HTTP requests and reports when the worker has
// been idle long enough to exit.
type IdleTracker struct {
	handler http.Handler
	timeout time.Duration

	mu       sync.Mutex
	inflight int
	last     time.Time
	fired    bool
}

func NewIdleTracker(handler http.Handler, timeout time.Duration) *IdleTracker {
	return &IdleTracker{handler: handler, timeout: timeout, last: time.Now()}
}

func (t *IdleTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	t.inflight++
	t.last = time.Now()
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.inflight--
		t.last = time.Now()
		t.mu.Unlock()
	}()
	t.handler.ServeHTTP(w, r)
}

// Run blocks until ctx is canceled or the worker has been idle for its timeout,
// then closes idle. A timeout <= 0 disables idle exit.
func (t *IdleTracker) Run(ctx context.Context, idle chan<- struct{}) {
	if t.timeout <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(minDuration(250*time.Millisecond, t.timeout))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if t.shouldExit(time.Now()) {
				close(idle)
				return
			}
		}
	}
}

func (t *IdleTracker) shouldExit(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fired || t.inflight != 0 || now.Sub(t.last) < t.timeout {
		return false
	}
	t.fired = true
	return true
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
