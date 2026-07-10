package appserver

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
)

func TestRequestSchedulerBackpressure(t *testing.T) {
	scheduler := NewRequestScheduler(1)
	first, rpcErr := scheduler.TryAcquire("fs/writeFile", nil)
	if rpcErr != nil {
		t.Fatalf("first acquire error: %v", rpcErr)
	}
	defer first.Release()

	second, rpcErr := scheduler.TryAcquire("fs/writeFile", nil)
	if rpcErr == nil || rpcErr.Code != protocol.CodeOverloaded {
		if second != nil {
			second.Release()
		}
		t.Fatalf("second acquire error = %#v, want overloaded", rpcErr)
	}

	first.Release()
	third, rpcErr := scheduler.TryAcquire("fs/writeFile", nil)
	if rpcErr != nil {
		t.Fatalf("third acquire after release: %v", rpcErr)
	}
	third.Release()
}

func TestRequestSchedulerSerializesMatchingScopes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	scheduler := NewRequestScheduler(3)
	first, rpcErr := scheduler.TryAcquire("fs/writeFile", nil)
	if rpcErr != nil {
		t.Fatalf("first acquire error: %v", rpcErr)
	}
	second, rpcErr := scheduler.TryAcquire("fs/readFile", nil)
	if rpcErr != nil {
		t.Fatalf("second acquire error: %v", rpcErr)
	}
	parallel, rpcErr := scheduler.TryAcquire("process/spawn", nil)
	if rpcErr != nil {
		t.Fatalf("parallel acquire error: %v", rpcErr)
	}

	releaseFirst := make(chan struct{})
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	parallelEntered := make(chan struct{})
	errs := make(chan error, 3)

	go func() {
		errs <- first.Run(ctx, func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered
	go func() {
		errs <- second.Run(ctx, func() error {
			close(secondEntered)
			return nil
		})
	}()
	go func() {
		errs <- parallel.Run(ctx, func() error {
			close(parallelEntered)
			return nil
		})
	}()

	select {
	case <-parallelEntered:
	case <-time.After(time.Second):
		t.Fatal("different request scope did not run in parallel")
	}
	select {
	case <-secondEntered:
		t.Fatal("matching request scope ran before first scope holder completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("matching request scope did not run after first scope holder completed")
	}
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatalf("scheduled run returned error: %v", err)
		}
	}
}

func TestRequestScheduleForThreadControls(t *testing.T) {
	methods := []string{
		"thread/search",
		"thread/loaded/list",
		"thread/unsubscribe",
		"thread/compact/start",
		"thread/shellCommand",
		"thread/approveGuardianDeniedAction",
		"thread/rollback",
		"thread/inject_items",
		"thread/goal/get",
		"thread/goal/set",
		"thread/goal/clear",
		"thread/metadata/update",
		"thread/memoryMode/set",
		"thread/name/set",
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			schedule := RequestScheduleFor(method, nil)
			if schedule.Scope != "thread" || !schedule.Serial {
				t.Fatalf("schedule = %#v, want serial thread scope", schedule)
			}
		})
	}
}

func TestRequestScheduleForMemoryReset(t *testing.T) {
	schedule := RequestScheduleFor("memory/reset", nil)
	if schedule.Scope != "memory" || !schedule.Serial {
		t.Fatalf("schedule = %#v, want serial memory scope", schedule)
	}
}
