// Package rungroup provides a clean and composable API for implementing
// common concurrency patterns to run, cancel and wait for multiple
// goroutines as a group.
package rungroup

import (
	"context"
	"errors"
)

// Group manages the lifecycle of a goroutine group.
//
// A Group transitions from an active state to a finalized state,
// and finally to a closed state.
//
// Upon instantiation, a Group resides in an active state.
// A group can accept and spawn new goroutines (through calls to Go)
// only when it is in an active state.
//
// After the Finalize or Cancel method is called, the Group enters
// a finalized state. A finalized Group ceases to accept new goroutines.
// See the Finalize and Cancel comments for the differences between
// these two methods.
//
// Once a Group is in a finalized state and all the goroutines have exited,
// the Group transitions to a closed state and the wait channel is closed.
// A closed Group ceases to accept new goroutines.
type Group struct {
	ctx       context.Context
	cancel    context.CancelFunc
	waitGroup *waitGroup
}

// New creates a new Group. The underlying context of the returned Group
// is based on the provided baseCtx context.
// The returned Group is in an active state.
//
// See the Group's comment for more details about the active state.
func New(baseCtx context.Context) *Group {
	newCtx, cancel := context.WithCancel(baseCtx)
	return &Group{
		ctx:       newCtx,
		cancel:    cancel,
		waitGroup: newWaitGroup(cancel),
	}
}

// Go attempts to run the function f in a new goroutine and add the
// goroutine to the Group. The attempt will succeed only when the Group
// is in an active state. Go returns a boolean to indicate whether
// the attempt was successful.
//
// The context provided to f as a function argument will be canceled
// (i.e. the Done channel of the context will be closed) once the Cancel
// method is called, so f can react accordingly.
//
// NOTE: Go will panic if f is nil.
//
// See the Group's comment for more details about the active state.
func (g *Group) Go(f func(context.Context)) bool {
	if f == nil {
		panic(errors.New("func is nil"))
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

// Finalize puts the Group into a finalized state.
//
// Finalize may be called by multiple goroutines simultaneously.
// After the first call, subsequent calls to Finalize do nothing.
//
// See the Group's comment for more details about the finalized state.
func (g *Group) Finalize() {
	g.waitGroup.finalize()
}

// Cancel puts the Group into a finalized state and cancels the underlying
// context, effectively sending exit signals to all goroutines in the Group,
// but does not wait for them to exit.
//
// Cancel may be called by multiple goroutines simultaneously.
// After the first call, subsequent calls to Cancel do nothing.
//
// See the Group's comment for more details about the finalized state.
func (g *Group) Cancel() {
	g.waitGroup.finalize()
	g.cancel()
}

// WaitChannel return the wait channel.
// The channel blocks until the Group enters a closed state.
//
// See the Group's comment for more details about the closed state.
func (g *Group) WaitChannel() <-chan struct{} {
	return g.waitGroup.waitChannel()
}
