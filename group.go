// Package rungroup provides a clean and composable API for implementing
// a common concurrency pattern to run, cancel and wait for multiple goroutines
// as a group.
package rungroup

import (
	"context"
	"os"
	"os/signal"
)

type GoFunc func(context.Context)

// Group managers the lifecycle of multiple goroutines as a group.
//
// A Group transitions from an active state to a canceled state,
// and finally to a closed state.
//
// Upon instantiation, a Group resides in an active state.
// An active Group can spawn a goroutine and track its progress.
//
// After the Cancel method is called, the Group enters a canceled state.
// A canceled Group ceases to accept new goroutines.
//
// Once all the goroutines have exited, the Group transitions to a closed state
// and the Wait exits to unblock the caller of Wait.
//
// Note that if all the goroutines within a Group exit and Cancel remains
// uncalled, the Group will advance from an active state to a closed state,
// bypassing the canceled phase.
type Group interface {
	// Go attempts call function f in a new goroutine and add f to the Group.
	// It returns a boolean to indicate whether the attempt was successful.
	// The context provided to f as a function argument will be canceled
	// (i.e. the Done channel of the context will be closed) once the Cancel
	// is invoked, so f can react accordingly.
	Go(f GoFunc) bool
	// Cancel cancels the underlying context, effectively sending exit signals
	// to all goroutines in the Group, but does not wait for them to exit.
	// After Cancel is called, the Group no longer accepts new goroutines
	// (i.e. Go always returns false from that point forward).
	// Cancel may be called by multiple goroutines simultaneously.
	// After the first call, subsequent calls to a CancelFunc do nothing.
	Cancel()
	// Wait blocks until all goroutines in the Group have exited.
	Wait()
}

type config struct {
	panicHandler func(recovered interface{})
	trapSignals  []os.Signal
}

type Option func(*config)

// WithTrapSignals configures the Group to intercept the OS signals
// and Cancel the Group if it does receive the OS signal.
func WithTrapSignals(signals []os.Signal) Option {
	return func(c *config) {
		c.trapSignals = signals
	}
}

// WithPanicHandler wraps each function in the Group with a panic recovery
// mechanism and passes the recovered object to function f if it does catch
// a panic.
func WithPanicHandler(f func(recovered interface{})) Option {
	return func(c *config) {
		c.panicHandler = f
	}
}

// New creates a new Group. The underlying context of the returned Group
// is based on the provided parent context.
func New(parent context.Context, options ...Option) Group {
	var cfg config
	for _, opt := range options {
		opt(&cfg)
	}
	newCtx, cancel := context.WithCancel(parent)
	var runGroup Group = &basicGroup{
		ctx:       newCtx,
		cancel:    cancel,
		waitGroup: newWaitGroup(),
	}
	if cfg.panicHandler != nil {
		runGroup = &groupWithPanicHandler{
			Group:        runGroup,
			panicHandler: cfg.panicHandler,
		}
	}
	if len(cfg.trapSignals) > 0 {
		runGroup = &groupWithTrapSignals{
			Group:   runGroup,
			signals: cfg.trapSignals,
		}
	}
	return runGroup
}

var _ Group = (*basicGroup)(nil)

type basicGroup struct {
	ctx       context.Context
	cancel    context.CancelFunc
	waitGroup *waitGroup
}

func (g *basicGroup) Go(f GoFunc) bool {
	if f == nil {
		return g.waitGroup.add(0)
	}
	if !g.waitGroup.add(1) {
		return false
	}
	go func() {
		defer g.waitGroup.done()
		f(g.ctx)
	}()
	return true
}

func (g *basicGroup) Wait() {
	g.waitGroup.wait()
	g.cancel()
}

func (g *basicGroup) Cancel() {
	g.waitGroup.cancel()
	g.cancel()
}

var _ Group = (*groupWithPanicHandler)(nil)

type groupWithPanicHandler struct {
	Group
	panicHandler func(recovered interface{})
}

func (g *groupWithPanicHandler) Go(f GoFunc) bool {
	if f == nil {
		return g.Group.Go(nil)
	}
	return g.Group.Go(func(ctx context.Context) {
		defer func() {
			if r := recover(); r != nil {
				g.panicHandler(r)
			}
		}()
		f(ctx)
	})
}

var _ Group = (*groupWithTrapSignals)(nil)

type groupWithTrapSignals struct {
	Group
	signals []os.Signal
}

var (
	signalNotify = signal.Notify
	signalStop   = signal.Stop
)

func (g *groupWithTrapSignals) Wait() {
	sigChan := make(chan os.Signal, 1)
	defer func() {
		signalStop(sigChan)
		close(sigChan)
	}()
	signalNotify(sigChan, g.signals...)
	waitChan := make(chan struct{})
	go func() {
		defer func() {
			close(waitChan)
		}()
		g.Group.Wait()
	}()
	select {
	case <-waitChan:
	case <-sigChan:
		g.Group.Cancel()
		<-waitChan
	}
}
