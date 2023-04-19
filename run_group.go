package rungroup

import (
	"context"
	"os"
	"os/signal"
	"sync"
)

type (
	GoFunc       func(context.Context)
	DeferFunc    func()
	PanicHandler func(recovered interface{})
)

type RunGroup interface {
	// Go tries to add f to the 'go' group and then run f in a separated goroutine immediately.
	// It returns a bool to indicate whether the try succeeded.
	// Refer to the Finalize comments for more information about why Go returns false.
	Go(f GoFunc) bool
	// Defer tries to add f to the 'defer' group and then arrange f to be run
	// after the RunGroup finalized and all functions in the 'go' group finished.
	// It returns a bool to indicate whether the try succeeded.
	// Refer to the Finalize comments for more information about why Defer returns false.
	// Functions in 'defer' group will not be run sequentially.
	Defer(f DeferFunc) bool
	// Finalize sets the RunGroup to finalized state, which means the RunGroup no longer accepts new members.
	// Calling Go or Defer on a finalized RunGroup does nothing and returns false.
	// Finalize may be called by multiple goroutines simultaneously.
	// After the first call, subsequent calls of Finalize do nothing.
	Finalize()
	// Wait blocks util all functions in both 'go' group and 'defer' group exit.
	Wait()
	// Close finalizes the RunGroup and closes the context's Done channel.
	// Thus sending exit signals to all functions in the 'go' group, but Close does not wait for them to exit.
	Close()
}

type config struct {
	panicHandler PanicHandler
	trapSignals  []os.Signal
}

type Option func(*config)

func WithTrapSignals(signals []os.Signal) Option {
	return func(c *config) {
		c.trapSignals = signals
	}
}

func WithPanicHandler(f PanicHandler) Option {
	return func(c *config) {
		c.panicHandler = f
	}
}

func New(ctx context.Context, options ...Option) RunGroup {
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
	var runGroup RunGroup = bg
	if cfg.panicHandler != nil {
		runGroup = &groupWithPanicHandler{
			basicGroup:   bg,
			panicHandler: cfg.panicHandler,
		}
	}
	if len(cfg.trapSignals) > 0 {
		runGroup = &groupWithTrapSignals{
			RunGroup: runGroup,
			signals:  cfg.trapSignals,
		}
	}
	return runGroup
}

var _ RunGroup = (*basicGroup)(nil)

type basicGroup struct {
	ctx       context.Context
	cancel    context.CancelFunc
	waitGroup sync.WaitGroup
	final     chan struct{}
	finalLock sync.RWMutex
	deferList []DeferFunc
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

func (g *basicGroup) Defer(f DeferFunc) bool {
	g.finalLock.RLock()
	defer g.finalLock.RUnlock()
	select {
	case <-g.final:
		return false
	default:
		if f != nil {
			g.deferList = append(g.deferList, f)
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
	if len(g.deferList) == 0 {
		return
	}
	g.waitGroup.Add(len(g.deferList))
	for _, f := range g.deferList {
		f := f
		go func() {
			defer g.waitGroup.Done()
			f()
		}()
	}
	g.waitGroup.Wait()
}

func (g *basicGroup) Close() {
	g.Finalize()
	g.cancel()
}

var _ RunGroup = (*groupWithPanicHandler)(nil)

type groupWithPanicHandler struct {
	*basicGroup
	panicHandler PanicHandler
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

func (g *groupWithPanicHandler) Defer(f DeferFunc) bool {
	if f == nil {
		return g.basicGroup.Defer(nil)
	}
	return g.basicGroup.Defer(func() {
		defer func() {
			if r := recover(); r != nil {
				g.panicHandler(r)
			}
		}()
		f()
	})
}

var _ RunGroup = (*groupWithTrapSignals)(nil)

type groupWithTrapSignals struct {
	RunGroup
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
		g.RunGroup.Wait()
	}()
	select {
	case <-waitChan:
	case <-sigChan:
		g.RunGroup.Close()
		<-waitChan
	}
}
