package rungroup

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

func osSignalFake() (
	fakeNotify func(c chan<- os.Signal, sig ...os.Signal),
	fakeStop func(chan<- os.Signal),
	fakeSend func(signal os.Signal),
	free func(),
) {
	var (
		sourceChannel  = make(chan os.Signal, 1)
		targetChannels sync.Map
	)
	go func() {
		for s := range sourceChannel {
			targetChannels.Range(func(c, _ interface{}) bool {
				cc := c.(chan<- os.Signal)
				select {
				case cc <- s:
				default:
				}
				return true
			})
		}
	}()
	fakeNotify = func(c chan<- os.Signal, _ ...os.Signal) {
		targetChannels.Store(c, struct{}{})
	}
	fakeStop = func(c chan<- os.Signal) {
		targetChannels.Delete(c)
	}
	fakeSend = func(signal os.Signal) {
		sourceChannel <- signal
	}
	free = func() {
		close(sourceChannel)
	}
	return
}

func TestCancelOnOSSignals(t *testing.T) {
	osSig := syscall.SIGINT
	gen := func() (*Group, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		group := New(ctx)
		CancelOnOSSignals(group, osSig)
		return group, cancel
	}
	testGroup(t, gen)

	t.Run("exit on os signal", func(t *testing.T) {
		var sendOSSignal func(os.Signal)
		{
			oldNotify := signalNotify
			oldStop := signalStop
			fakeNotify, fakeStop, fakeSend, free := osSignalFake()
			signalNotify = fakeNotify
			signalStop = fakeStop
			sendOSSignal = fakeSend
			defer func() {
				signalNotify = oldNotify
				signalStop = oldStop
				free()
			}()
		}
		for _, concurrency := range []int32{0, 1, 2, 3, 4, 5, 32} {
			t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
				group, parentCtxCancel := gen()
				defer parentCtxCancel()
				(&testcase{
					group:       group,
					concurrency: concurrency,
					shouldWait:  true,
					cancel: func() {
						sendOSSignal(osSig)
					},
				}).do(t)
			})
		}
	})
}

func TestNewGroupDeriveFrom(t *testing.T) {
	t.Run("derive from a finalized group", func(t *testing.T) {
		parent := New(context.Background())
		parent.Finalize()
		derived, ok := NewGroupDeriveFrom(context.Background(), parent)
		if ok || derived != nil {
			t.Fatal("derive a new group from a finalized group")
		}
	})

	t.Run("canceled by parent", func(t *testing.T) {
		parent := New(context.Background())
		derived, ok := NewGroupDeriveFrom(context.Background(), parent)
		if !ok {
			t.Fatal("failed to derive a new group")
		}
		var counter byte
		derived.Go(func(ctx context.Context) {
			<-ctx.Done()
			counter = 97
		})
		parent.Cancel()
		<-derived.WaitChannel()
		if counter != 97 {
			t.Fatal("the goroutine in the derived group is not done")
		}
	})

	t.Run("derived group closes first", func(t *testing.T) {
		parent := New(context.Background())
		child, ok := NewGroupDeriveFrom(context.Background(), parent)
		if !ok {
			t.Fatal("failed to derive a new group")
		}
		var counter byte
		child.Go(func(ctx context.Context) {
			<-ctx.Done()
			counter = 97
		})
		parent.Finalize()
		select {
		case <-parent.WaitChannel():
			t.Fatal("the parent group closed before the derived group closes")
		case <-time.After(time.Millisecond * 10):
		}
		child.Cancel()
		<-parent.WaitChannel()
		if counter != 97 {
			t.Fatal("the goroutine in the derived group is not done")
		}
	})
}
