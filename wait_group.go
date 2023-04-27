package rungroup

import (
	"errors"
	"sync"
)

type waitGroup struct {
	sem    int64
	mu     sync.Mutex
	signal chan struct{}
}

func newWaitGroup() *waitGroup {
	return &waitGroup{
		signal: make(chan struct{}),
	}
}

func (wg *waitGroup) add(delta uint32) bool {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	select {
	case <-wg.signal:
		return false
	default:
	}
	wg.sem += int64(delta)
	return true
}

func (wg *waitGroup) done() {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	select {
	case <-wg.signal:
		return
	default:
	}
	wg.sem--
	if wg.sem < 0 {
		panic(errors.New("wait group over done"))
	}
	if wg.sem == 0 {
		close(wg.signal)
	}
}

func (wg *waitGroup) wait() {
	<-wg.signal
}
