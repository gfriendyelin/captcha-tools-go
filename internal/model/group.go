// Package model provides concurrency-safe lifecycle management for model resources.
package model

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"
)

var (
	// ErrClosed is returned by Acquire after its Group has been closed.
	ErrClosed = errors.New("model: group is closed")
	// ErrActiveLeases is returned by Close when leases are still outstanding.
	ErrActiveLeases = errors.New("model: group has active leases")
	// ErrNilLoader is returned when a Group was constructed without a loader.
	ErrNilLoader = errors.New("model: loader is nil")
	// ErrNilResource is returned when a loader reports success without a resource.
	ErrNilResource = errors.New("model: loader returned a nil resource")
)

// Resource is an expendable model resource.
//
// Close must be safe to call after all leases for the resource have been
// released. A Group calls Close exactly once for every resource it accepts.
type Resource interface {
	Close() error
}

// Loader constructs a model resource.
//
// A loader must clean up partially constructed resources when it returns an
// error or its context is canceled. If it nevertheless returns both a non-nil
// resource and an error, Group closes that resource as a final safeguard.
// Loader calls for a Group are serialized: at most one is in flight at a time.
type Loader func(context.Context) (Resource, error)

// Group lazily loads one resource and shares it through reference-counted
// leases. Its methods are safe for concurrent use.
type Group struct {
	name        string
	loader      Loader
	idleTimeout time.Duration
	logger      *slog.Logger

	mu         sync.Mutex
	changed    chan struct{}
	resource   Resource
	leases     int
	load       *loadCall
	unload     *unloadCall
	idle       *idleTimer
	loadedOnce bool

	closed    bool
	closeDone chan struct{}
	closeErr  error
}

type loadCall struct {
	done    chan struct{}
	cancel  context.CancelFunc
	started time.Time
	reload  bool
	err     error // written before done is closed; read only after that
}

type unloadCall struct {
	done      chan struct{}
	started   time.Time
	automatic bool
	err       error // written before done is closed; read only after that
}

type idleTimer struct {
	timer *time.Timer
}

// NewGroup constructs an unloaded Group.
//
// A non-positive idleTimeout disables automatic unloading. A nil logger uses
// slog.Default. A nil loader is reported as ErrNilLoader by Acquire.
func NewGroup(name string, loader Loader, idleTimeout time.Duration, logger *slog.Logger) *Group {
	if logger == nil {
		logger = slog.Default()
	}
	return &Group{
		name:        name,
		loader:      loader,
		idleTimeout: idleTimeout,
		logger:      logger,
		changed:     make(chan struct{}),
		closeDone:   make(chan struct{}),
	}
}

// Name returns the immutable name supplied to NewGroup.
func (g *Group) Name() string {
	return g.name
}

// Loaded reports whether the Group currently owns a loaded resource. It
// returns false while a resource is being loaded or has been detached for
// unloading, even if Resource.Close has not returned yet.
func (g *Group) Loaded() bool {
	g.mu.Lock()
	loaded := g.resource != nil
	g.mu.Unlock()
	return loaded
}

// Acquire returns a lease for the shared resource, loading it if necessary.
// Concurrent callers share a single loader call. Cancellation of one caller
// cancels only its wait, not the loader call shared with other callers.
//
// If an unload is in progress, Acquire waits for it and then reloads. The
// returned Lease must be released when no longer needed.
func (g *Group) Acquire(ctx context.Context) (*Lease, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			return nil, ErrClosed
		}
		if g.unload != nil {
			changed := g.changed
			g.mu.Unlock()
			if err := waitContext(ctx, changed); err != nil {
				return nil, err
			}
			continue
		}
		if g.resource != nil {
			g.stopIdleLocked()
			g.leases++
			resource := g.resource
			g.mu.Unlock()
			return &Lease{group: g, resource: resource}, nil
		}

		call := g.load
		if call == nil {
			call = g.startLoadLocked(ctx)
		}
		done := call.done
		g.mu.Unlock()

		if err := waitContext(ctx, done); err != nil {
			return nil, err
		}
		if call.err != nil {
			// Every caller waiting on this call observes the same failure. A
			// later Acquire may start a fresh attempt.
			return nil, call.err
		}
		// The resource may have entered an unload before this waiter was
		// scheduled. Re-check the state instead of assuming it is available.
	}
}

func (g *Group) startLoadLocked(parent context.Context) *loadCall {
	// The load belongs to the Group, not to the first caller. Preserve values
	// but detach caller cancellation and deadline; Close explicitly cancels it.
	loadCtx, cancel := context.WithCancel(context.WithoutCancel(parent))
	call := &loadCall{
		done:    make(chan struct{}),
		cancel:  cancel,
		started: time.Now(),
		reload:  g.loadedOnce,
	}
	g.load = call
	go g.runLoad(loadCtx, call)
	return call
}

func (g *Group) runLoad(ctx context.Context, call *loadCall) {
	defer call.cancel()

	var resource Resource
	var err error
	if g.loader == nil {
		err = ErrNilLoader
	} else {
		resource, err = g.loader(ctx)
	}
	if err == nil && isNilResource(resource) {
		err = ErrNilResource
		resource = nil
	}
	if err != nil && !isNilResource(resource) {
		// Loaders own partial cleanup, but do not leak a resource returned
		// alongside an error if a loader violates the usual convention.
		if closeErr := resource.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("model: close resource after load failure: %w", closeErr))
		}
		resource = nil
	}

	g.mu.Lock()
	if err == nil {
		g.resource = resource
		g.loadedOnce = true
	}
	g.load = nil
	call.err = err
	if err == nil && g.leases == 0 && g.unload == nil && !g.closed {
		// All original waiters may have canceled. Do not leave an unused
		// successful load resident forever when an idle timeout is configured.
		g.scheduleIdleLocked()
	}
	close(call.done)
	g.signalLocked()
	g.mu.Unlock()

	operation := "load"
	if call.reload {
		operation = "reload"
	}
	attrs := []any{
		"name", g.name,
		"duration", time.Since(call.started),
		"reload", call.reload,
	}
	if err != nil {
		g.logger.Error("model "+operation+" failed", append(attrs, "error", err)...)
	} else {
		g.logger.Info("model "+operation+" completed", attrs...)
	}
}

// Lease is one reference to a Group's loaded resource. A Lease must not be
// copied after first use.
type Lease struct {
	group    *Group
	resource Resource
	once     sync.Once
}

// Resource returns the leased resource. It remains the same value after
// Release, but callers must stop using it before releasing the lease.
func (l *Lease) Resource() Resource {
	if l == nil {
		return nil
	}
	return l.resource
}

// Release relinquishes the lease. It is idempotent and safe for concurrent
// calls. The final release schedules idle unloading when configured and wakes
// any pending manual unload.
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.group.release()
	})
}

func (g *Group) release() {
	g.mu.Lock()
	if g.leases <= 0 {
		g.mu.Unlock()
		panic("model: lease released without an active lease")
	}
	g.leases--
	if g.leases == 0 {
		if g.resource != nil && g.unload == nil && !g.closed {
			g.scheduleIdleLocked()
		}
		g.signalLocked()
	}
	g.mu.Unlock()
}

// Unload waits for the current load and all outstanding leases, prevents new
// acquisitions from racing with removal, and closes the resource. Acquisitions
// waiting behind the unload automatically start a fresh load afterwards.
// Concurrent calls join the same unload operation; repeated calls on an
// unloaded Group are no-ops.
//
// Canceling ctx stops only this caller's wait. The unload itself continues so
// that concurrent callers and the state machine are never stranded. A loader
// in progress is awaited rather than canceled; Close is the operation that
// cancels loading.
func (g *Group) Unload(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	call := g.unload
	if call == nil {
		if g.resource == nil && g.load == nil {
			g.mu.Unlock()
			return nil
		}
		call = g.startUnloadLocked(false)
	}
	done := call.done
	g.mu.Unlock()

	if err := waitContext(ctx, done); err != nil {
		return err
	}
	return call.err
}

func (g *Group) startUnloadLocked(automatic bool) *unloadCall {
	g.stopIdleLocked()
	call := &unloadCall{
		done:      make(chan struct{}),
		started:   time.Now(),
		automatic: automatic,
	}
	g.unload = call
	go g.runUnload(call)
	return call
}

func (g *Group) runUnload(call *unloadCall) {
	for {
		g.mu.Lock()
		if g.leases == 0 && g.load == nil {
			resource := g.resource
			g.resource = nil
			g.signalLocked()
			g.mu.Unlock()

			err := closeResource(resource)

			g.mu.Lock()
			call.err = err
			g.unload = nil
			close(call.done)
			g.signalLocked()
			g.mu.Unlock()

			attrs := []any{
				"name", g.name,
				"duration", time.Since(call.started),
				"automatic", call.automatic,
			}
			if err != nil {
				g.logger.Error("model unload failed", append(attrs, "error", err)...)
			} else {
				g.logger.Info("model unload completed", attrs...)
			}
			return
		}
		changed := g.changed
		g.mu.Unlock()
		<-changed
	}
}

// Close permanently closes the Group. If any leases are outstanding, Close
// returns ErrActiveLeases and leaves the Group open and otherwise unchanged.
// Otherwise it prevents new acquisitions, cancels an in-flight load, waits for
// any unload, and closes the loaded resource.
//
// Close is idempotent. Concurrent and subsequent calls wait for the first
// close attempt to finish and observe its result. A resource Close error does
// not reopen the Group.
func (g *Group) Close() error {
	g.mu.Lock()
	if g.closed {
		done := g.closeDone
		g.mu.Unlock()
		<-done
		g.mu.Lock()
		err := g.closeErr
		g.mu.Unlock()
		return err
	}
	if g.leases != 0 {
		leases := g.leases
		g.mu.Unlock()
		return fmt.Errorf("%w: %d", ErrActiveLeases, leases)
	}

	started := time.Now()
	g.closed = true
	g.stopIdleLocked()
	if g.load != nil {
		g.load.cancel()
	}
	g.signalLocked()

	var unloadErr error
	for {
		if g.unload != nil {
			call := g.unload
			done := call.done
			g.mu.Unlock()
			<-done
			if call.err != nil {
				unloadErr = call.err
			}
			g.mu.Lock()
			continue
		}
		if g.load != nil {
			done := g.load.done
			g.mu.Unlock()
			<-done
			g.mu.Lock()
			continue
		}

		resource := g.resource
		g.resource = nil
		g.mu.Unlock()

		closeErr := closeResource(resource)
		err := errors.Join(unloadErr, closeErr)

		g.mu.Lock()
		g.closeErr = err
		close(g.closeDone)
		g.signalLocked()
		g.mu.Unlock()

		attrs := []any{"name", g.name, "duration", time.Since(started)}
		if err != nil {
			g.logger.Error("model close failed", append(attrs, "error", err)...)
		} else {
			g.logger.Info("model close completed", attrs...)
		}
		return err
	}
}

func (g *Group) scheduleIdleLocked() {
	if g.idleTimeout <= 0 || g.idle != nil || g.resource == nil ||
		g.leases != 0 || g.load != nil || g.unload != nil || g.closed {
		return
	}
	token := &idleTimer{}
	g.idle = token
	token.timer = time.AfterFunc(g.idleTimeout, func() {
		g.unloadIdle(token)
	})
}

func (g *Group) stopIdleLocked() {
	if g.idle == nil {
		return
	}
	g.idle.timer.Stop()
	g.idle = nil
}

func (g *Group) unloadIdle(token *idleTimer) {
	g.mu.Lock()
	// Stop cannot prevent an already-started callback. The token check keeps
	// such a stale callback from unloading a newly acquired resource.
	if g.idle != token || g.resource == nil || g.leases != 0 ||
		g.load != nil || g.unload != nil || g.closed {
		g.mu.Unlock()
		return
	}
	g.startUnloadLocked(true)
	g.mu.Unlock()
}

func (g *Group) signalLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func waitContext(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func closeResource(resource Resource) error {
	if isNilResource(resource) {
		return nil
	}
	return resource.Close()
}

func isNilResource(resource Resource) bool {
	if resource == nil {
		return true
	}
	value := reflect.ValueOf(resource)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
