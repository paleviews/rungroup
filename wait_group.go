package rungroup

import (
	"errors"
	"sync"
)

type waitGroup struct {
	mu        sync.Mutex
	sem       int32
	finalized bool
	signal    chan struct{}
	onClose   func()
}

func newWaitGroup(onClose func()) *waitGroup {
	return &waitGroup{
		signal:  make(chan struct{}),
		onClose: onClose,
	}
}

func (wg *waitGroup) add(delta uint8) bool {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if wg.finalized {
		return false
	}
	wg.sem += int32(delta)
	return true
}

func (wg *waitGroup) done() {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	wg.sem--
	if wg.sem < 0 {
		panic(errors.New("wait group over done"))
	}
	if wg.sem == 0 && wg.finalized {
		close(wg.signal)
		if c := wg.onClose; c != nil {
			c()
		}
	}
}

func (wg *waitGroup) finalize() {
	wg.mu.Lock()
	defer wg.mu.Unlock()
	if !wg.finalized {
		wg.finalized = true
		if wg.sem == 0 {
			close(wg.signal)
			if c := wg.onClose; c != nil {
				c()
			}
		}
	}
}

func (wg *waitGroup) waitChannel() <-chan struct{} {
	return wg.signal
}
