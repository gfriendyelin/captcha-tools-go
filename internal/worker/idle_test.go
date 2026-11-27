package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdleTrackerExitsAfterTimeout(t *testing.T) {
	tracker := NewIdleTracker(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idle := make(chan struct{})
	go tracker.Run(ctx, idle)
	select {
	case <-idle:
	case <-time.After(time.Second):
		t.Fatal("idle tracker did not fire")
	}
}

func TestIdleTrackerWaitsForInflightRequest(t *testing.T) {
	release := make(chan struct{})
	tracker := NewIdleTracker(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}), 80*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idle := make(chan struct{})
	go tracker.Run(ctx, idle)

	done := make(chan struct{})
	go func() {
		tracker.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(done)
	}()
	select {
	case <-idle:
		t.Fatal("idle tracker fired with an in-flight request")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-done
	select {
	case <-idle:
	case <-time.After(time.Second):
		t.Fatal("idle tracker did not fire after the request finished")
	}
}
