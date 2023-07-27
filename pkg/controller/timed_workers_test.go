package controller


import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/klog/v2/ktesting"
	testingclock "k8s.io/utils/clock/testing"
)

func TestExecute(t *testing.T) {
	testVal := int32(0)
	wg := sync.WaitGroup{}
	wg.Add(5)
	queue := CreateWorkerQueue(func(ctx context.Context, args *WorkArgs) error {
		atomic.AddInt32(&testVal, 1)
		wg.Done()
		return nil
	})
	now := time.Now()
	queue.AddWork(context.TODO(), NewWorkArgs("1", "1", ""), now, now)
	queue.AddWork(context.TODO(), NewWorkArgs("2", "2", ""), now, now)
	queue.AddWork(context.TODO(), NewWorkArgs("3", "3", ""), now, now)
	queue.AddWork(context.TODO(), NewWorkArgs("4", "4", ""), now, now)
	queue.AddWork(context.TODO(), NewWorkArgs("5", "5", ""), now, now)
	// Adding the same thing second time should be no-op
	queue.AddWork(context.TODO(), NewWorkArgs("1", "1", ""), now, now)
	queue.AddWork(context.TODO(), NewWorkArgs("2", "2", ""), now, now)
	queue.AddWork(context.TODO(), NewWorkArgs("3", "3", ""), now, now)
	queue.AddWork(context.TODO(), NewWorkArgs("4", "4", ""), now, now)
	queue.AddWork(context.TODO(), NewWorkArgs("5", "5", ""), now, now)
	wg.Wait()
	lastVal := atomic.LoadInt32(&testVal)
	if lastVal != 5 {
		t.Errorf("Expected testVal = 5, got %v", lastVal)
	}
}

func TestExecuteDelayed(t *testing.T) {
	testVal := int32(0)
	wg := sync.WaitGroup{}
	wg.Add(5)
	queue := CreateWorkerQueue(func(ctx context.Context, args *WorkArgs) error {
		atomic.AddInt32(&testVal, 1)
		wg.Done()
		return nil
	})
	now := time.Now()
	then := now.Add(10 * time.Second)
	fakeClock := testingclock.NewFakeClock(now)
	queue.clock = fakeClock
	queue.AddWork(context.TODO(), NewWorkArgs("1", "1", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("2", "2", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("3", "3", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("4", "4", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("5", "5", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("1", "1", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("2", "2", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("3", "3", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("4", "4", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("5", "5", ""), now, then)
	fakeClock.Step(11 * time.Second)
	wg.Wait()
	lastVal := atomic.LoadInt32(&testVal)
	if lastVal != 5 {
		t.Errorf("Expected testVal = 5, got %v", lastVal)
	}
}

func TestCancel(t *testing.T) {
	testVal := int32(0)
	wg := sync.WaitGroup{}
	wg.Add(3)
	queue := CreateWorkerQueue(func(ctx context.Context, args *WorkArgs) error {
		atomic.AddInt32(&testVal, 1)
		wg.Done()
		return nil
	})
	now := time.Now()
	then := now.Add(10 * time.Second)
	fakeClock := testingclock.NewFakeClock(now)
	queue.clock = fakeClock
	queue.AddWork(context.TODO(), NewWorkArgs("1", "1", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("2", "2", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("3", "3", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("4", "4", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("5", "5", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("1", "1", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("2", "2", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("3", "3", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("4", "4", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("5", "5", ""), now, then)
	logger, _ := ktesting.NewTestContext(t)
	queue.CancelWork(logger, NewWorkArgs("2", "2", "").KeyFromWorkArgs())
	queue.CancelWork(logger, NewWorkArgs("4", "4", "").KeyFromWorkArgs())
	fakeClock.Step(11 * time.Second)
	wg.Wait()
	lastVal := atomic.LoadInt32(&testVal)
	if lastVal != 3 {
		t.Errorf("Expected testVal = 3, got %v", lastVal)
	}
}

func TestCancelAndReadd(t *testing.T) {
	testVal := int32(0)
	wg := sync.WaitGroup{}
	wg.Add(4)
	queue := CreateWorkerQueue(func(ctx context.Context, args *WorkArgs) error {
		atomic.AddInt32(&testVal, 1)
		wg.Done()
		return nil
	})
	now := time.Now()
	then := now.Add(10 * time.Second)
	fakeClock := testingclock.NewFakeClock(now)
	queue.clock = fakeClock
	queue.AddWork(context.TODO(), NewWorkArgs("1", "1", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("2", "2", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("3", "3", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("4", "4", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("5", "5", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("1", "1", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("2", "2", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("3", "3", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("4", "4", ""), now, then)
	queue.AddWork(context.TODO(), NewWorkArgs("5", "5", ""), now, then)
	logger, _ := ktesting.NewTestContext(t)
	queue.CancelWork(logger, NewWorkArgs("2", "2", "").KeyFromWorkArgs())
	queue.CancelWork(logger, NewWorkArgs("4", "4", "").KeyFromWorkArgs())
	queue.AddWork(context.TODO(), NewWorkArgs("2", "2", ""), now, then)
	fakeClock.Step(11 * time.Second)
	wg.Wait()
	lastVal := atomic.LoadInt32(&testVal)
	if lastVal != 4 {
		t.Errorf("Expected testVal = 4, got %v", lastVal)
	}
}
