package rungroup

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

type testcase struct {
	group       *Group
	concurrency int32
	shouldWait  bool
	cancel      func()
}

func (tc *testcase) do(t *testing.T) {
	var counter int32
	for i := int32(0); i < tc.concurrency; i++ {
		tc.group.Go(func(ctx context.Context) {
			atomic.AddInt32(&counter, 1)
			if tc.shouldWait {
				<-ctx.Done()
			}
		})
	}
	if c := tc.cancel; c != nil {
		c()
	}
	tc.group.Finalize()
	<-tc.group.WaitChannel()
	if counter != tc.concurrency {
		t.Fail()
	}
}

func testGroup(t *testing.T, gen func() (group *Group, parentCtxCancel context.CancelFunc)) {
	for _, concurrency := range []int32{0, 1, 2, 3, 4, 5, 32} {
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			t.Run("exit on function return", func(t *testing.T) {
				group, parentCtxCancel := gen()
				defer parentCtxCancel()
				(&testcase{
					group:       group,
					concurrency: concurrency,
					shouldWait:  false,
					cancel:      nil,
				}).do(t)
			})
			t.Run("exit on context signal", func(t *testing.T) {
				group, parentCtxCancel := gen()
				defer parentCtxCancel()
				(&testcase{
					group:       group,
					concurrency: concurrency,
					shouldWait:  true,
					cancel:      parentCtxCancel,
				}).do(t)
			})
			t.Run("exit on group cancel", func(t *testing.T) {
				group, parentCtxCancel := gen()
				defer parentCtxCancel()
				(&testcase{
					group:       group,
					concurrency: concurrency,
					shouldWait:  true,
					cancel:      group.Cancel,
				}).do(t)
			})
		})
	}
}

func TestGroup(t *testing.T) {
	gen := func() (*Group, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		group := New(ctx)
		return group, cancel
	}
	testGroup(t, gen)
}
