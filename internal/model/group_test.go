package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testTimeout = 5 * time.Second

type fakeResource struct {
	id           int
	closeCalls   atomic.Int32
	closed       chan struct{}
	closeStarted chan struct{}
	closeBlock   chan struct{}
	closeErr     error
	onClose      func()
	closeOnce    sync.Once
}

func newFakeResource(id int) *fakeResource {
	return &fakeResource{id: id, closed: make(chan struct{})}
}

func (r *fakeResource) Close() error {
	r.closeOnce.Do(func() {
		r.closeCalls.Add(1)
		if r.closeStarted != nil {
			close(r.closeStarted)
		}
		if r.closeBlock != nil {
			<-r.closeBlock
		}
		if r.onClose != nil {
			r.onClose()
		}
		close(r.closed)
	})
	return r.closeErr
}

type observedContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

// observeContext signals the first evaluation of Done, allowing tests to wait
// for an actual blocking point instead of relying on arbitrary sleeps.
func observeContext(ctx context.Context) (context.Context, <-chan struct{}) {
	observed := make(chan struct{})
	return &observedContext{Context: ctx, observed: observed}, observed
}

func (c *observedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

type acquireResult struct {
	lease *Lease
	err   error
}

func startAcquire(group *Group, ctx context.Context) (<-chan acquireResult, <-chan struct{}) {
	ctx, observed := observeContext(ctx)
	result := make(chan acquireResult, 1)
	go func() {
		lease, err := group.Acquire(ctx)
		result <- acquireResult{lease: lease, err: err}
	}()
	return result, observed
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitAcquire(t *testing.T, result <-chan acquireResult) acquireResult {
	t.Helper()
	select {
	case acquired := <-result:
		return acquired
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for Acquire")
		return acquireResult{}
	}
}

func waitError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for operation")
		return nil
	}
}

func TestGroupLazyConcurrentAcquireLoadsOnce(t *testing.T) {
	var calls atomic.Int32
	resource := newFakeResource(1)
	loaderStarted := make(chan struct{})
	allowLoad := make(chan struct{})
	group := NewGroup("concurrent", func(context.Context) (Resource, error) {
		if calls.Add(1) == 1 {
			close(loaderStarted)
		}
		<-allowLoad
		return resource, nil
	}, 0, discardLogger())

	if group.Name() != "concurrent" {
		t.Fatalf("Name() = %q", group.Name())
	}
	if group.Loaded() {
		t.Fatal("new Group must be lazy and unloaded")
	}
	if err := group.Unload(context.Background()); err != nil {
		t.Fatalf("no-op Unload: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("Unload on an unloaded Group invoked the loader")
	}

	const goroutines = 24
	results := make([]<-chan acquireResult, goroutines)
	observed := make([]<-chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		results[i], observed[i] = startAcquire(group, context.Background())
	}
	waitSignal(t, loaderStarted, "shared loader")
	for i := range observed {
		waitSignal(t, observed[i], fmt.Sprintf("Acquire waiter %d", i))
	}
	close(allowLoad)

	leases := make([]*Lease, goroutines)
	for i, result := range results {
		acquired := waitAcquire(t, result)
		if acquired.err != nil {
			t.Fatalf("Acquire %d: %v", i, acquired.err)
		}
		if acquired.lease.Resource() != resource {
			t.Fatalf("Acquire %d returned a different resource", i)
		}
		leases[i] = acquired.lease
	}
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1", calls.Load())
	}
	if !group.Loaded() {
		t.Fatal("Group is not loaded after successful Acquire")
	}
	for _, lease := range leases {
		lease.Release()
	}
	if err := group.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if resource.closeCalls.Load() != 1 {
		t.Fatalf("resource close calls = %d", resource.closeCalls.Load())
	}
}

func TestGroupLoadFailureBroadcastAndRetry(t *testing.T) {
	loadErr := errors.New("synthetic load failure")
	var calls atomic.Int32
	loaderStarted := make(chan struct{})
	failLoad := make(chan struct{})
	broken := newFakeResource(1)
	working := newFakeResource(2)
	group := NewGroup("failure", func(ctx context.Context) (Resource, error) {
		switch calls.Add(1) {
		case 1:
			close(loaderStarted)
			select {
			case <-failLoad:
				_ = broken.Close() // loader-owned partial cleanup
				return nil, loadErr
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		default:
			return working, nil
		}
	}, 0, discardLogger())

	const goroutines = 16
	results := make([]<-chan acquireResult, goroutines)
	observed := make([]<-chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		results[i], observed[i] = startAcquire(group, context.Background())
	}
	waitSignal(t, loaderStarted, "failing loader")
	for i := range observed {
		waitSignal(t, observed[i], fmt.Sprintf("failure waiter %d", i))
	}
	close(failLoad)
	for i, result := range results {
		acquired := waitAcquire(t, result)
		if acquired.lease != nil || !errors.Is(acquired.err, loadErr) {
			t.Fatalf("waiter %d got lease=%v err=%v", i, acquired.lease, acquired.err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want one shared failure", calls.Load())
	}
	if broken.closeCalls.Load() != 1 {
		t.Fatalf("loader cleanup calls = %d", broken.closeCalls.Load())
	}
	if group.Loaded() {
		t.Fatal("failed load left Group loaded")
	}

	lease, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatalf("retry Acquire: %v", err)
	}
	if lease.Resource() != working || calls.Load() != 2 {
		t.Fatal("retry did not perform a fresh load")
	}
	lease.Release()
	if err := group.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if working.closeCalls.Load() != 1 {
		t.Fatalf("working resource close calls = %d", working.closeCalls.Load())
	}
}

func TestGroupIdleUnloadAndReload(t *testing.T) {
	var calls atomic.Int32
	group := NewGroup("idle", func(context.Context) (Resource, error) {
		return newFakeResource(int(calls.Add(1))), nil
	}, 40*time.Millisecond, discardLogger())

	firstLease, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := firstLease.Resource().(*fakeResource)
	firstLease.Release()
	waitSignal(t, first.closed, "first idle unload")
	if group.Loaded() {
		t.Fatal("resource remained loaded after idle close")
	}

	secondLease, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatalf("reload Acquire: %v", err)
	}
	second := secondLease.Resource().(*fakeResource)
	if second == first || calls.Load() != 2 {
		t.Fatal("Acquire after idle unload did not reload")
	}
	secondLease.Release()
	waitSignal(t, second.closed, "second idle unload")
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupZeroIdleTimeoutDisablesAutomaticUnload(t *testing.T) {
	resource := newFakeResource(1)
	group := NewGroup("no-idle", func(context.Context) (Resource, error) {
		return resource, nil
	}, 0, discardLogger())
	lease, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	time.Sleep(60 * time.Millisecond)
	if !group.Loaded() || resource.closeCalls.Load() != 0 {
		t.Fatal("idleTimeout=0 unexpectedly unloaded the resource")
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupUnloadWaitsAndBlocksCompetingAcquire(t *testing.T) {
	var calls atomic.Int32
	group := NewGroup("unload-race", func(context.Context) (Resource, error) {
		return newFakeResource(int(calls.Add(1))), nil
	}, 0, discardLogger())
	oldLease, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oldResource := oldLease.Resource().(*fakeResource)

	unloadCtx, unloadObserved := observeContext(context.Background())
	unloadResult := make(chan error, 1)
	go func() { unloadResult <- group.Unload(unloadCtx) }()
	waitSignal(t, unloadObserved, "Unload wait")

	acquireResultCh, acquireObserved := startAcquire(group, context.Background())
	waitSignal(t, acquireObserved, "Acquire behind Unload")
	select {
	case <-oldResource.closed:
		t.Fatal("resource closed while its lease was active")
	default:
	}
	if !group.Loaded() {
		t.Fatal("resource detached before the final Release")
	}

	oldLease.Release()
	if err := waitError(t, unloadResult); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	acquired := waitAcquire(t, acquireResultCh)
	if acquired.err != nil {
		t.Fatalf("competing Acquire: %v", acquired.err)
	}
	newResource := acquired.lease.Resource().(*fakeResource)
	if newResource == oldResource || calls.Load() != 2 {
		t.Fatal("competing Acquire did not reload after Unload")
	}
	if oldResource.closeCalls.Load() != 1 {
		t.Fatal("old resource was not closed exactly once")
	}
	acquired.lease.Release()
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupUnloadContextCancellationDoesNotStrandGroup(t *testing.T) {
	var calls atomic.Int32
	group := NewGroup("unload-cancel", func(context.Context) (Resource, error) {
		return newFakeResource(int(calls.Add(1))), nil
	}, 0, discardLogger())
	lease, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oldResource := lease.Resource().(*fakeResource)

	base, cancel := context.WithCancel(context.Background())
	unloadCtx, unloadObserved := observeContext(base)
	unloadResult := make(chan error, 1)
	go func() { unloadResult <- group.Unload(unloadCtx) }()
	waitSignal(t, unloadObserved, "cancelable Unload")
	cancel()
	if err := waitError(t, unloadResult); !errors.Is(err, context.Canceled) {
		t.Fatalf("Unload error = %v, want context.Canceled", err)
	}

	acquireResultCh, acquireObserved := startAcquire(group, context.Background())
	waitSignal(t, acquireObserved, "Acquire behind canceled Unload wait")
	lease.Release()
	acquired := waitAcquire(t, acquireResultCh)
	if acquired.err != nil {
		t.Fatalf("Acquire after canceled wait: %v", acquired.err)
	}
	if oldResource.closeCalls.Load() != 1 || calls.Load() != 2 {
		t.Fatal("canceled Unload wait stranded or duplicated lifecycle work")
	}
	acquired.lease.Release()
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupAcquireCancellationDoesNotCancelSharedLoad(t *testing.T) {
	var calls atomic.Int32
	resource := newFakeResource(1)
	loaderStarted := make(chan struct{})
	allowLoad := make(chan struct{})
	group := NewGroup("acquire-cancel", func(ctx context.Context) (Resource, error) {
		calls.Add(1)
		close(loaderStarted)
		select {
		case <-allowLoad:
			return resource, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, 0, discardLogger())

	base, cancel := context.WithCancel(context.Background())
	firstResult, firstObserved := startAcquire(group, base)
	waitSignal(t, loaderStarted, "shared loader")
	waitSignal(t, firstObserved, "first Acquire wait")
	secondResult, secondObserved := startAcquire(group, context.Background())
	waitSignal(t, secondObserved, "second Acquire wait")
	cancel()
	if first := waitAcquire(t, firstResult); !errors.Is(first.err, context.Canceled) {
		t.Fatalf("first Acquire error = %v", first.err)
	}
	close(allowLoad)
	second := waitAcquire(t, secondResult)
	if second.err != nil {
		t.Fatalf("shared load was canceled by first waiter: %v", second.err)
	}
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d", calls.Load())
	}
	second.lease.Release()
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupCloseRejectsActiveLeasesAndReleaseIsIdempotent(t *testing.T) {
	var calls atomic.Int32
	resource := newFakeResource(1)
	group := NewGroup("close-protection", func(context.Context) (Resource, error) {
		calls.Add(1)
		return resource, nil
	}, 0, discardLogger())
	first, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Close(); !errors.Is(err, ErrActiveLeases) {
		t.Fatalf("Close with lease error = %v", err)
	}
	if !group.Loaded() {
		t.Fatal("failed Close changed Group state")
	}
	second, err := group.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire after rejected Close: %v", err)
	}

	var releases sync.WaitGroup
	for i := 0; i < 20; i++ {
		releases.Add(2)
		go func() { defer releases.Done(); first.Release() }()
		go func() { defer releases.Done(); second.Release() }()
	}
	releases.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d", calls.Load())
	}
	if err := group.Close(); err != nil {
		t.Fatalf("Close after releases: %v", err)
	}
	if err := group.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if resource.closeCalls.Load() != 1 {
		t.Fatalf("resource close calls = %d", resource.closeCalls.Load())
	}
	if _, err := group.Acquire(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Acquire after Close error = %v", err)
	}
	if err := group.Unload(context.Background()); err != nil {
		t.Fatalf("Unload after Close: %v", err)
	}
}

func TestGroupCloseCancelsInFlightLoad(t *testing.T) {
	loaderStarted := make(chan struct{})
	group := NewGroup("close-load", func(ctx context.Context) (Resource, error) {
		close(loaderStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}, 0, discardLogger())
	acquired, observed := startAcquire(group, context.Background())
	waitSignal(t, loaderStarted, "in-flight loader")
	waitSignal(t, observed, "Acquire waiting for loader")
	closeResult := make(chan error, 1)
	go func() { closeResult <- group.Close() }()
	if err := waitError(t, closeResult); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if result := waitAcquire(t, acquired); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("Acquire error = %v, want loader cancellation", result.err)
	}
	if group.Loaded() {
		t.Fatal("canceled load left a resource")
	}
}
