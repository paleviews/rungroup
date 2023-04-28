package rungroup

import (
	"context"
	"os"
	"os/signal"
)

type GoFunc func(context.Context)

type Group interface {
	// Go tries to add f to the Group and then run f in a separated goroutine immediately.
	// It returns a bool to indicate whether the try succeeded.
	Go(f GoFunc) bool
	// Cancel cancels the context.
	// Thus sending exit signals to all goroutines in the Group,
	// but does not wait for them to exit.
	Cancel()
	// Wait waits for all goroutines in the Group to exit.
	Wait()
}

type config struct {
	panicHandler func(recovered interface{})
	trapSignals  []os.Signal
}

type Option func(*config)

func WithTrapSignals(signals []os.Signal) Option {
	return func(c *config) {
		c.trapSignals = signals
	}
}

func WithPanicHandler(f func(recovered interface{})) Option {
	return func(c *config) {
		c.panicHandler = f
	}
}

func New(ctx context.Context, options ...Option) Group {
	var cfg config
	for _, opt := range options {
		opt(&cfg)
	}
	newCtx, cancel := context.WithCancel(ctx)
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
	if !g.waitGroup.add(1) {
		return false
	}
	if f != nil {
		go func() {
			defer g.waitGroup.done()
			f(g.ctx)
		}()
	}
	return true
}

func (g *basicGroup) Wait() {
	g.waitGroup.wait()
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
