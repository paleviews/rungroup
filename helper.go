package rungroup

import (
	"context"
	"os"
	"os/signal"
)

var (
	signalNotify = signal.Notify
	signalStop   = signal.Stop
)

// CancelOnOSSignals setups the Group to be notified and canceled upon
// receiving the specified OS signals. If no signals are provided,
// all incoming signals will cause the Group to be notified and canceled.
//
// CancelOnOSSignals helps to implement the common pattern to intercept
// OS signals, notify the goroutines to return voluntarily and wait for
// all of them to exit cleanly.
func CancelOnOSSignals(group *Group, signals ...os.Signal) {
	sigChan := make(chan os.Signal, 1)
	signalNotify(sigChan, signals...)
	go func() {
		defer func() {
			signalStop(sigChan)
			close(sigChan)
		}()
		select {
		case <-group.WaitChannel():
		case <-sigChan:
			group.Cancel()
		}
	}()
}

// NewGroupDeriveFrom creates a new Group derived from the parent Group;
// which means canceling the parent Group also cancels the new Group,
// and the parent Group will not close before the new Group closes. If the
// parent Group no longer accepts new goroutines, NewGroupDeriveFrom does
// nothing and returns (nil, false).
//
// NewGroupDeriveFrom helps to implement the common pattern of cascade
// goroutine groups.
func NewGroupDeriveFrom(baseCtx context.Context, parent *Group) (*Group, bool) {
	child := New(baseCtx)
	ok := parent.Go(func(ctx context.Context) {
		select {
		case <-child.WaitChannel():
		case <-ctx.Done():
			child.Cancel()
			<-child.WaitChannel()
		}
	})
	if ok {
		return child, true
	}
	child.Cancel()
	return nil, false
}
