package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	workspacefs "github.com/fugue-labs/gollem/appserver/tools/fs"
)

func TestServiceStartShellWaitCapturesOutput(t *testing.T) {
	ctx := context.Background()
	var outputs []OutputEvent
	var exits []ExitEvent
	var outputsMu sync.Mutex
	svc := newTestService(t, WithOutputSink(func(ev OutputEvent) {
		outputsMu.Lock()
		defer outputsMu.Unlock()
		outputs = append(outputs, ev)
	}), WithExitSink(func(ev ExitEvent) {
		outputsMu.Lock()
		defer outputsMu.Unlock()
		exits = append(exits, ev)
	}))

	started, err := svc.Start(ctx, StartRequest{
		Command: "printf 'hello stdout'; printf 'hello stderr' >&2",
		Shell:   true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := svc.Wait(ctx, started.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != StatusCompleted || done.ExitCode != 0 {
		t.Fatalf("snapshot = %+v", done)
	}
	if string(done.Stdout) != "hello stdout" {
		t.Fatalf("stdout = %q", done.Stdout)
	}
	if string(done.Stderr) != "hello stderr" {
		t.Fatalf("stderr = %q", done.Stderr)
	}
	outputsMu.Lock()
	outputCount := len(outputs)
	exitsCopy := append([]ExitEvent(nil), exits...)
	outputsMu.Unlock()
	if outputCount == 0 {
		t.Fatal("expected output events")
	}
	if len(exitsCopy) != 1 || exitsCopy[0].Snapshot.ID != started.ID || exitsCopy[0].Snapshot.Status != StatusCompleted {
		t.Fatalf("exit events = %+v", exitsCopy)
	}
}

func TestServiceRunPublishesPerRequestObservers(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	var outputs []OutputEvent
	var exits []ExitEvent
	var mu sync.Mutex

	done, err := svc.Run(ctx, StartRequest{
		Command: "printf 'request stdout'; printf 'request stderr' >&2",
		Shell:   true,
		OutputSink: func(event OutputEvent) {
			mu.Lock()
			defer mu.Unlock()
			outputs = append(outputs, event)
		},
		ExitSink: func(event ExitEvent) {
			mu.Lock()
			defer mu.Unlock()
			exits = append(exits, event)
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if done.Status != StatusCompleted || string(done.Stdout) != "request stdout" || string(done.Stderr) != "request stderr" {
		t.Fatalf("snapshot = %+v", done)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(outputs) < 2 {
		t.Fatalf("per-request output events = %+v", outputs)
	}
	if len(exits) != 1 || exits[0].Snapshot.ID != done.ID || exits[0].Snapshot.Status != StatusCompleted {
		t.Fatalf("per-request exit events = %+v", exits)
	}
}

func TestServiceRunCanSuppressGlobalObservers(t *testing.T) {
	ctx := context.Background()
	var globalOutputs, globalExits, requestOutputs, requestExits int
	svc := newTestService(t,
		WithOutputSink(func(OutputEvent) { globalOutputs++ }),
		WithExitSink(func(ExitEvent) { globalExits++ }),
	)
	done, err := svc.Run(ctx, StartRequest{
		Command:             "printf private-output",
		Shell:               true,
		SuppressGlobalSinks: true,
		OutputSink:          func(OutputEvent) { requestOutputs++ },
		ExitSink:            func(ExitEvent) { requestExits++ },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if done.Status != StatusCompleted || string(done.Stdout) != "private-output" {
		t.Fatalf("snapshot = %+v", done)
	}
	if globalOutputs != 0 || globalExits != 0 || requestOutputs == 0 || requestExits != 1 {
		t.Fatalf("observer counts global=%d/%d request=%d/%d", globalOutputs, globalExits, requestOutputs, requestExits)
	}
}

func TestServiceRunCancellationKillsOwnedProcessWithoutSecondApproval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var approvals []Operation
	var audits []AuditEvent
	var mu sync.Mutex
	ready := make(chan struct{})
	var readyOnce sync.Once
	svc := newTestService(t,
		WithApproval(func(_ context.Context, operation Operation) error {
			mu.Lock()
			defer mu.Unlock()
			approvals = append(approvals, operation)
			return nil
		}),
		WithAuditSink(func(event AuditEvent) {
			mu.Lock()
			defer mu.Unlock()
			audits = append(audits, event)
		}),
	)

	type runResult struct {
		snapshot *Snapshot
		err      error
	}
	resultCh := make(chan runResult, 1)
	go func() {
		snapshot, err := svc.Run(ctx, StartRequest{
			Command: "printf ready; sleep 30",
			Shell:   true,
			OutputSink: func(event OutputEvent) {
				if strings.Contains(string(event.Data), "ready") {
					readyOnce.Do(func() { close(ready) })
				}
			},
		})
		resultCh <- runResult{snapshot: snapshot, err: err}
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for owned process output")
	}
	cancel()
	var result runResult
	select {
	case result = <-resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("Run cancellation error = %v, want context.Canceled", result.err)
	}
	if result.snapshot == nil || result.snapshot.Status != StatusKilled {
		t.Fatalf("canceled snapshot = %+v", result.snapshot)
	}
	processes, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(processes) != 1 || processes[0].Status == StatusRunning {
		t.Fatalf("processes after cancellation = %+v", processes)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(approvals) != 1 || approvals[0].Kind != OperationStart {
		t.Fatalf("approval operations = %+v, want only start", approvals)
	}
	if len(audits) < 2 || audits[len(audits)-1].Operation.Kind != OperationCancel || !audits[len(audits)-1].Allowed {
		t.Fatalf("audit events = %+v, want allowed ownership cancellation", audits)
	}
}

func TestServiceStartUsesProvidedIDAndRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	first, err := svc.Start(ctx, StartRequest{ID: "client-proc", Command: "cat"})
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if first.ID != "client-proc" {
		t.Fatalf("process id = %q, want client-proc", first.ID)
	}
	if _, err := svc.Start(ctx, StartRequest{ID: "client-proc", Command: "cat"}); !errors.Is(err, ErrProcessAlreadyExists) {
		t.Fatalf("duplicate start error = %v, want ErrProcessAlreadyExists", err)
	}
	if err := svc.Kill(ctx, first.ID); err != nil {
		t.Fatalf("Kill first: %v", err)
	}
	if _, err := waitWithTimeout(t, svc, first.ID); err != nil {
		t.Fatalf("Wait first: %v", err)
	}
}

func TestServiceWorkDirIsWorkspaceScoped(t *testing.T) {
	ctx := context.Background()
	outside := t.TempDir()
	svc := newTestService(t)
	if err := os.MkdirAll(filepath.Join(svc.Root(), "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(svc.Root(), "sub", "file.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	started, err := svc.Start(ctx, StartRequest{
		Command: "pwd; cat file.txt",
		Shell:   true,
		WorkDir: "sub",
	})
	if err != nil {
		t.Fatalf("Start scoped: %v", err)
	}
	done, err := svc.Wait(ctx, started.ID)
	if err != nil {
		t.Fatalf("Wait scoped: %v", err)
	}
	if !strings.Contains(string(done.Stdout), "sub") || !strings.Contains(string(done.Stdout), "inside") {
		t.Fatalf("stdout = %q", done.Stdout)
	}
	if _, err := svc.Start(ctx, StartRequest{Command: "pwd", Shell: true, WorkDir: "../outside"}); !errors.Is(err, workspacefs.ErrPathOutsideRoot) {
		t.Fatalf("traversal error = %v, want ErrPathOutsideRoot", err)
	}
	if err := os.Symlink(outside, filepath.Join(svc.Root(), "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := svc.Start(ctx, StartRequest{Command: "pwd", Shell: true, WorkDir: "escape"}); !errors.Is(err, workspacefs.ErrPathOutsideRoot) {
		t.Fatalf("symlink escape error = %v, want ErrPathOutsideRoot", err)
	}
}

func TestServiceWriteStdin(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	started, err := svc.Start(ctx, StartRequest{
		Command: "read line; printf 'got:%s\\n' \"$line\"",
		Shell:   true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.WriteStdin(ctx, started.ID, []byte("hello\n")); err != nil {
		t.Fatalf("WriteStdin: %v", err)
	}
	done, err := waitWithTimeout(t, svc, started.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if string(done.Stdout) != "got:hello\n" {
		t.Fatalf("stdout = %q", done.Stdout)
	}
}

func TestServiceKillAndTimeout(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	started, err := svc.Start(ctx, StartRequest{Command: "sleep 30", Shell: true})
	if err != nil {
		t.Fatalf("Start kill target: %v", err)
	}
	if err := svc.Kill(ctx, started.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	killed, err := waitWithTimeout(t, svc, started.ID)
	if err != nil {
		t.Fatalf("Wait killed: %v", err)
	}
	if killed.Status != StatusKilled {
		t.Fatalf("killed status = %+v", killed)
	}

	timed, err := svc.Start(ctx, StartRequest{
		Command: "sleep 2",
		Shell:   true,
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start timeout target: %v", err)
	}
	timedOut, err := waitWithTimeout(t, svc, timed.ID)
	if err != nil {
		t.Fatalf("Wait timeout: %v", err)
	}
	if timedOut.Status != StatusTimedOut {
		t.Fatalf("timeout status = %+v", timedOut)
	}
}

func TestServiceApprovalAuditAndResize(t *testing.T) {
	ctx := context.Background()
	var events []AuditEvent
	denyStart := func(_ context.Context, op Operation) error {
		if op.Kind == OperationStart {
			return errors.New("start disabled")
		}
		return nil
	}
	svc := newTestService(t, WithApproval(denyStart), WithAuditSink(func(ev AuditEvent) {
		events = append(events, ev)
	}))
	if _, err := svc.Start(ctx, StartRequest{Command: "echo no", Shell: true}); !errors.Is(err, ErrApprovalDenied) {
		t.Fatalf("Start denied error = %v, want ErrApprovalDenied", err)
	}
	if len(events) != 1 || events[0].Allowed || events[0].Err == "" {
		t.Fatalf("audit events = %+v", events)
	}

	svc = newTestService(t)
	started, err := svc.Start(ctx, StartRequest{Command: "sleep 1", Shell: true})
	if err != nil {
		t.Fatalf("Start resize target: %v", err)
	}
	defer func() {
		_ = svc.Kill(context.Background(), started.ID)
		_, _ = waitWithTimeout(t, svc, started.ID)
	}()
	if err := svc.ResizePTY(ctx, started.ID, 80, 24); !errors.Is(err, ErrPTYUnsupported) {
		t.Fatalf("ResizePTY error = %v, want ErrPTYUnsupported", err)
	}
}

func TestServiceListAndOutputLimit(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	started, err := svc.Start(ctx, StartRequest{
		Command:        "printf 'abcdef'",
		Shell:          true,
		MaxOutputBytes: 3,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := svc.Wait(ctx, started.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if string(done.Stdout) != "def" {
		t.Fatalf("limited stdout = %q", done.Stdout)
	}
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != started.ID {
		t.Fatalf("list = %+v", list)
	}
}

func TestServiceCleanCompleted(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	running, err := svc.Start(ctx, StartRequest{Command: "cat"})
	if err != nil {
		t.Fatalf("Start running: %v", err)
	}
	completed, err := svc.Start(ctx, StartRequest{Command: "printf", Args: []string{"done"}})
	if err != nil {
		t.Fatalf("Start completed: %v", err)
	}
	if _, err := waitWithTimeout(t, svc, completed.ID); err != nil {
		t.Fatalf("Wait completed: %v", err)
	}
	removed, err := svc.CleanCompleted(ctx)
	if err != nil {
		t.Fatalf("CleanCompleted: %v", err)
	}
	if len(removed) != 1 || removed[0].ID != completed.ID || removed[0].Status != StatusCompleted {
		t.Fatalf("removed = %+v", removed)
	}
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != running.ID {
		t.Fatalf("list after clean = %+v", list)
	}
	if err := svc.Kill(ctx, running.ID); err != nil {
		t.Fatalf("Kill running: %v", err)
	}
	if _, err := waitWithTimeout(t, svc, running.ID); err != nil {
		t.Fatalf("Wait killed: %v", err)
	}
}

func newTestService(t *testing.T, opts ...Option) *Service {
	t.Helper()
	svc, err := NewService(t.TempDir(), opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func waitWithTimeout(t *testing.T, svc *Service, id string) (*Snapshot, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return svc.Wait(ctx, id)
}
