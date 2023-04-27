package rungroup

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

func repeatGo(group Group, f GoFunc, n int) {
	for i := 0; i < n; i++ {
		group.Go(f)
	}
}

type testcase struct {
	group   Group
	cancel  func()
	assert  func() bool
	cleanup func()
}

func (c *testcase) do(t *testing.T) {
	defer c.cleanup()
	if c.cancel != nil {
		c.cancel()
	}
	c.group.Wait()
	if !c.assert() {
		t.Fail()
	}
}

type exitOn string

const (
	exitOnFunctionReturn exitOn = "spawned functions exit by themselves"
	exitOnContextSignal  exitOn = "spawned functions exit on context signals"
	exitOnGroupCancel    exitOn = "spawned functions exit on cancel"
	exitOnPanic          exitOn = "spawned functions panic"
	exitOnOSSignal       exitOn = "spawned functions exit on os signals"
)

type testcaseArgs struct {
	exitOn           exitOn
	withPanicHandler bool
	withTrapSignals  bool
}

func newTestCase(args testcaseArgs, osSignalSendSimulate func()) *testcase {
	const num = 16
	var (
		group          Group
		ctx, ctxCancel = context.WithCancel(context.Background())
		cancel         func()
		counter        int64
		times          int64 = 1
	)
	{
		var options []Option
		if args.withPanicHandler {
			options = append(options, WithPanicHandler(func(interface{}) {
				atomic.AddInt64(&counter, 1)
			}))
		}
		if args.withTrapSignals {
			options = append(options, WithTrapSignals([]os.Signal{syscall.SIGINT}))
		}
		group = New(ctx, options...)
	}

	switch args.exitOn {
	case exitOnFunctionReturn:
		repeatGo(group, func(context.Context) {
			atomic.AddInt64(&counter, 1)
		}, num)
	case exitOnContextSignal:
		repeatGo(group, func(ctx context.Context) {
			atomic.AddInt64(&counter, 1)
			<-ctx.Done()
		}, num)
		cancel = ctxCancel
	case exitOnGroupCancel:
		repeatGo(group, func(ctx context.Context) {
			atomic.AddInt64(&counter, 1)
			<-ctx.Done()
		}, num)
		cancel = group.Close
	case exitOnPanic:
		repeatGo(group, func(context.Context) {
			atomic.AddInt64(&counter, 1)
			panic("oops")
		}, num)
		times++
	case exitOnOSSignal:
		repeatGo(group, func(ctx context.Context) {
			atomic.AddInt64(&counter, 1)
			<-ctx.Done()
		}, num)
		cancel = func() {
			osSignalSendSimulate()
		}
	}

	assert := func() bool {
		return atomic.LoadInt64(&counter) == times*num
	}
	group.Finalize()

	return &testcase{
		group:   group,
		cancel:  cancel,
		assert:  assert,
		cleanup: ctxCancel,
	}
}

func osSignalFake() (
	fakeNotify func(c chan<- os.Signal, sig ...os.Signal),
	fakeStop func(chan<- os.Signal),
	fakeSend func(),
	free func(),
) {

	var (
		sourceChannel  = make(chan os.Signal, 1)
		targetChannels sync.Map
		signal         os.Signal = syscall.SIGINT
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
	fakeSend = func() {
		sourceChannel <- signal
	}
	free = func() {
		close(sourceChannel)
	}
	return
}

func TestGroup(t *testing.T) {
	var sendOSSignal func()
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
	for _, withPanicHandler := range []bool{false, true} {
		name := "without panic handler"
		if withPanicHandler {
			name = "with panic handler"
		}
		t.Run(name, func(t *testing.T) {
			for _, withTrapSignals := range []bool{false, true} {
				name := "without tap on os signals"
				if withTrapSignals {
					name = "with tap on os signals"
				}
				t.Run(name, func(t *testing.T) {
					types := []exitOn{exitOnFunctionReturn, exitOnContextSignal, exitOnGroupCancel}
					if withPanicHandler {
						types = append(types, exitOnPanic)
					}
					if withTrapSignals {
						types = append(types, exitOnOSSignal)
					}
					for _, exitType := range types {
						t.Run(string(exitType), func(t *testing.T) {
							newTestCase(testcaseArgs{
								exitOn:           exitType,
								withPanicHandler: withPanicHandler,
								withTrapSignals:  withTrapSignals,
							}, sendOSSignal).do(t)
						})
					}
				})
			}
		})
	}
}
