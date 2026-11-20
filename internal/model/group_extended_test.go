package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupConcurrentUnloadJoinsOneClose(t *testing.T) {
	resource := newFakeResource(1)
	resource.closeStarted = make(chan struct{})
	resource.closeBlock = make(chan struct{})
	group := NewGroup("duplicate-unload", func(context.Context) (Resource, error) {
		return resource, nil
	}, 0, discardLogger())
	lease, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()

	firstUnload := make(chan error, 1)
	go func() { firstUnload <- group.Unload(context.Background()) }()
	waitSignal(t, resource.closeStarted, "blocked resource Close")

	const followers = 12
	results := make([]<-chan error, followers)
	observed := make([]<-chan struct{}, followers)
	for i := 0; i < followers; i++ {
		ctx, seen := observeContext(context.Background())
		observed[i] = seen
		result := make(chan error, 1)
		results[i] = result
		go func() { result <- group.Unload(ctx) }()
	}
	for i := range observed {
		waitSignal(t, observed[i], "concurrent Unload")
	}
	if group.Loaded() {
		t.Fatal("detached resource still reported loaded")
	}
	close(resource.closeBlock)
	if err := waitError(t, firstUnload); err != nil {
		t.Fatal(err)
	}
	for i, result := range results {
		if err := waitError(t, result); err != nil {
			t.Fatalf("concurrent Unload %d: %v", i, err)
		}
	}
	if resource.closeCalls.Load() != 1 {
		t.Fatalf("resource close calls = %d", resource.closeCalls.Load())
	}
	if err := group.Unload(context.Background()); err != nil {
		t.Fatalf("repeated Unload: %v", err)
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupLoadFailureClosesReturnedResource(t *testing.T) {
	loadErr := errors.New("resource plus error")
	resource := newFakeResource(1)
	group := NewGroup("bad-loader", func(context.Context) (Resource, error) {
		return resource, loadErr
	}, 0, discardLogger())
	lease, err := group.Acquire(context.Background())
	if lease != nil || !errors.Is(err, loadErr) {
		t.Fatalf("Acquire lease=%v err=%v", lease, err)
	}
	if resource.closeCalls.Load() != 1 {
		t.Fatalf("returned failed resource close calls = %d", resource.closeCalls.Load())
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupUnloadErrorIsReturnedAndCanReload(t *testing.T) {
	closeErr := errors.New("synthetic close failure")
	var calls atomic.Int32
	first := newFakeResource(1)
	first.closeErr = closeErr
	second := newFakeResource(2)
	group := NewGroup("unload-error", func(context.Context) (Resource, error) {
		if calls.Add(1) == 1 {
			return first, nil
		}
		return second, nil
	}, 0, discardLogger())
	lease, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if err := group.Unload(context.Background()); !errors.Is(err, closeErr) {
		t.Fatalf("Unload error = %v", err)
	}
	if group.Loaded() {
		t.Fatal("failed Close left detached resource loaded")
	}
	reloaded, err := group.Acquire(context.Background())
	if err != nil || reloaded.Resource() != second {
		t.Fatalf("reload lease=%v err=%v", reloaded, err)
	}
	reloaded.Release()
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupNilLoaderAndNilResource(t *testing.T) {
	nilLoader := NewGroup("nil-loader", nil, 0, discardLogger())
	if _, err := nilLoader.Acquire(context.Background()); !errors.Is(err, ErrNilLoader) {
		t.Fatalf("nil loader error = %v", err)
	}
	if err := nilLoader.Close(); err != nil {
		t.Fatal(err)
	}

	nilResource := NewGroup("nil-resource", func(context.Context) (Resource, error) {
		return nil, nil
	}, 0, discardLogger())
	if _, err := nilResource.Acquire(context.Background()); !errors.Is(err, ErrNilResource) {
		t.Fatalf("nil resource error = %v", err)
	}
	if err := nilResource.Close(); err != nil {
		t.Fatal(err)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestGroupLifecycleLogs(t *testing.T) {
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	loadErr := errors.New("expected logged load error")
	var calls atomic.Int32
	group := NewGroup("logging", func(context.Context) (Resource, error) {
		call := calls.Add(1)
		if call <= 2 {
			return newFakeResource(int(call)), nil
		}
		return nil, loadErr
	}, 0, logger)

	lease, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if err := group.Unload(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, err = group.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if err := group.Unload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := group.Acquire(context.Background()); !errors.Is(err, loadErr) {
		t.Fatalf("final load error = %v", err)
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(testTimeout)
	for {
		text := logs.String()
		if strings.Contains(text, "model load completed") &&
			strings.Contains(text, "model reload completed") &&
			strings.Contains(text, "model reload failed") &&
			strings.Contains(text, "model unload completed") &&
			strings.Contains(text, "duration") &&
			strings.Contains(text, "reload") && strings.Contains(text, "true") &&
			strings.Contains(text, loadErr.Error()) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing lifecycle log records: %s", text)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGroupConcurrentLifecycleRaceStress(t *testing.T) {
	var loads atomic.Int32
	var closes atomic.Int32
	group := NewGroup("stress", func(context.Context) (Resource, error) {
		id := int(loads.Add(1))
		resource := newFakeResource(id)
		resource.onClose = func() { closes.Add(1) }
		return resource, nil
	}, time.Millisecond, discardLogger())

	const workers = 8
	const iterations = 40
	errorsCh := make(chan error, workers)
	var workersWG sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		workersWG.Add(1)
		go func(worker int) {
			defer workersWG.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				lease, err := group.Acquire(context.Background())
				if err != nil {
					errorsCh <- fmt.Errorf("worker %d Acquire: %w", worker, err)
					return
				}
				if lease.Resource() == nil {
					errorsCh <- fmt.Errorf("worker %d got nil resource", worker)
					lease.Release()
					return
				}
				if iteration%5 == 0 {
					runtime.Gosched()
				}
				lease.Release()
				if worker%3 == 0 && iteration%11 == 0 {
					ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
					err := group.Unload(ctx)
					cancel()
					if err != nil {
						errorsCh <- fmt.Errorf("worker %d Unload: %w", worker, err)
						return
					}
				}
			}
		}(worker)
	}
	workersWG.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	if err := group.Close(); err != nil {
		t.Fatalf("final Close: %v", err)
	}
	if loads.Load() != closes.Load() {
		t.Fatalf("resource leak or double close: loads=%d closes=%d", loads.Load(), closes.Load())
	}
	if group.Loaded() {
		t.Fatal("stress Group remained loaded after Close")
	}
}
