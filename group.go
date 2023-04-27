package rungroup

import (
	"context"
	"os"
	"os/signal"
	"sync"
)

type GoFunc func(context.Context)

type Group interface {
	// Go tries to add f to the Group and then run f in a separated goroutine immediately.
	// It returns a bool to indicate whether the try succeeded.
	// See Finalize for more information about why Go returns false.
	Go(f GoFunc) bool
	// Finalize sets the Group to finalized state, which means the Group no longer accepts new members.
	// Calling Go on a finalized RunGroup does nothing and returns false.
	// Finalize may be called by multiple goroutines simultaneously.
	// After the first call, subsequent calls of Finalize do nothing.
	Finalize()
	// Wait blocks until all functions in the Group exit.
	Wait()
	// Close finalizes the RunGroup and cancels the context.
	// Thus sending exit signals to all functions in the Group, but Close does not wait for them to exit.
	Close()
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
	bg := &basicGroup{
		ctx:    newCtx,
		cancel: cancel,
		final:  make(chan struct{}),
	}
	var runGroup Group = bg
	if cfg.panicHandler != nil {
		runGroup = &groupWithPanicHandler{
			basicGroup:   bg,
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
	waitGroup sync.WaitGroup
	final     chan struct{}
	finalLock sync.RWMutex
}

func (g *basicGroup) Go(f GoFunc) bool {
	g.finalLock.RLock()
	defer g.finalLock.RUnlock()
	select {
	case <-g.final:
		return false
	default:
		if f != nil {
			g.waitGroup.Add(1)
			go func() {
				defer g.waitGroup.Done()
				f(g.ctx)
			}()
		}
		return true
	}
}

func (g *basicGroup) Finalize() {
	g.finalLock.Lock()
	defer g.finalLock.Unlock()
	select {
	case <-g.final:
	default:
		close(g.final)
	}
}

func (g *basicGroup) Wait() {
	<-g.final
	g.waitGroup.Wait()
}

func (g *basicGroup) Close() {
	g.Finalize()
	g.cancel()
}

var _ Group = (*groupWithPanicHandler)(nil)

type groupWithPanicHandler struct {
	*basicGroup
	panicHandler func(recovered interface{})
}

func (g *groupWithPanicHandler) Go(f GoFunc) bool {
	if f == nil {
		return g.basicGroup.Go(nil)
	}
	return g.basicGroup.Go(func(ctx context.Context) {
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
		g.Group.Close()
		<-waitChan
	}
}
