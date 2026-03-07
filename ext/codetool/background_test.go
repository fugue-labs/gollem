package codetool

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- RingBuffer Tests ---

func TestRingBuffer_Basic(t *testing.T) {
	buf := NewRingBuffer(5)
	buf.WriteLine("line1")
	buf.WriteLine("line2")
	buf.WriteLine("line3")

	if buf.Len() != 3 {
		t.Errorf("expected len 3, got %d", buf.Len())
	}

	got := buf.LastN(2)
	if got != "line2\nline3" {
		t.Errorf("LastN(2) = %q, want %q", got, "line2\nline3")
	}

	got = buf.LastN(10) // more than available
	if got != "line1\nline2\nline3" {
		t.Errorf("LastN(10) = %q, want %q", got, "line1\nline2\nline3")
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	buf := NewRingBuffer(3)
	buf.WriteLine("a")
	buf.WriteLine("b")
	buf.WriteLine("c")
	buf.WriteLine("d") // evicts "a"
	buf.WriteLine("e") // evicts "b"

	if buf.Len() != 3 {
		t.Errorf("expected len 3 (capped), got %d", buf.Len())
	}

	got := buf.LastN(3)
	if got != "c\nd\ne" {
		t.Errorf("LastN(3) = %q, want %q", got, "c\nd\ne")
	}
}

func TestRingBuffer_Empty(t *testing.T) {
	buf := NewRingBuffer(5)
	if got := buf.LastN(5); got != "" {
		t.Errorf("LastN on empty = %q, want empty", got)
	}
	if buf.Len() != 0 {
		t.Errorf("Len on empty = %d, want 0", buf.Len())
	}
}

func TestRingBuffer_SingleLine(t *testing.T) {
	buf := NewRingBuffer(5)
	buf.WriteLine("only")
	got := buf.LastN(1)
	if got != "only" {
		t.Errorf("LastN(1) = %q, want %q", got, "only")
	}
}

func TestRingBuffer_ExactCapacity(t *testing.T) {
	buf := NewRingBuffer(3)
	buf.WriteLine("a")
	buf.WriteLine("b")
	buf.WriteLine("c")

	got := buf.LastN(3)
	if got != "a\nb\nc" {
		t.Errorf("LastN(3) = %q, want %q", got, "a\nb\nc")
	}
}

// --- BackgroundProcessManager Tests ---

func TestBackgroundProcessManager_StartAndComplete(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()

	result, err := mgr.Start("", "echo hello && echo world", false, 0)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !strings.Contains(result, "bg-1") {
		t.Errorf("expected bg-1 in result, got: %s", result)
	}
	if !strings.Contains(result, "pid") {
		t.Errorf("expected pid in result, got: %s", result)
	}

	// Wait for process to complete.
	time.Sleep(500 * time.Millisecond)

	status, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess failed: %v", err)
	}
	if !strings.Contains(status, "completed") {
		t.Errorf("expected completed status, got: %s", status)
	}
	if !strings.Contains(status, "hello") {
		t.Errorf("expected stdout captured, got: %s", status)
	}
}

func TestBackgroundProcessManager_FailedProcess(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()

	_, err := mgr.Start("", "echo oops >&2 && exit 42", false, 0)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	status, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess failed: %v", err)
	}
	if !strings.Contains(status, "failed") {
		t.Errorf("expected failed status, got: %s", status)
	}
	if !strings.Contains(status, "exit code 42") {
		t.Errorf("expected exit code 42, got: %s", status)
	}
	if !strings.Contains(status, "oops") {
		t.Errorf("expected stderr captured, got: %s", status)
	}
}

func TestBackgroundProcessManager_CompletionPrompt(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()

	_, err := mgr.Start("", "echo done", false, 0)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Before completion: should return empty.
	_, err = mgr.CompletionPrompt(context.Background(), &core.RunContext{})
	if err != nil {
		t.Fatalf("CompletionPrompt error: %v", err)
	}
	// Process might not be done yet, but let's wait and check.
	time.Sleep(500 * time.Millisecond)

	prompt, err := mgr.CompletionPrompt(context.Background(), &core.RunContext{})
	if err != nil {
		t.Fatalf("CompletionPrompt error: %v", err)
	}
	if !strings.Contains(prompt, "bg-1") {
		t.Errorf("expected bg-1 in completion prompt, got: %q", prompt)
	}
	if !strings.Contains(prompt, "completed") {
		t.Errorf("expected 'completed' in prompt, got: %q", prompt)
	}

	// Second call: should return empty (already notified).
	prompt2, err := mgr.CompletionPrompt(context.Background(), &core.RunContext{})
	if err != nil {
		t.Fatalf("CompletionPrompt error: %v", err)
	}
	if prompt2 != "" {
		t.Errorf("expected empty on second call, got: %q", prompt2)
	}
}

func TestBackgroundProcessManager_MaxConcurrent(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()

	// Start max processes.
	for i := range maxBackgroundProcesses {
		_, err := mgr.Start("", "sleep 60", false, 0)
		if err != nil {
			t.Fatalf("Start %d failed: %v", i, err)
		}
	}

	// Next one should fail.
	_, err := mgr.Start("", "echo overflow", false, 0)
	if err == nil {
		t.Fatal("expected error for exceeding max concurrent")
	}
	var retryErr *core.ModelRetryError
	if !strings.Contains(err.Error(), "maximum concurrent") {
		t.Errorf("expected max concurrent error, got: %v (type: %T, retryErr: %v)", err, err, retryErr)
	}
}

func TestBackgroundProcessManager_FormatAll(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()

	_, _ = mgr.Start("", "echo first", false, 0)
	_, _ = mgr.Start("", "echo second", false, 0)
	time.Sleep(500 * time.Millisecond)

	all := mgr.FormatAll()
	if !strings.Contains(all, "bg-1") || !strings.Contains(all, "bg-2") {
		t.Errorf("expected both processes in output, got: %s", all)
	}
}

func TestBackgroundProcessManager_FormatAllEmpty(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	all := mgr.FormatAll()
	if all != "No background processes." {
		t.Errorf("expected empty message, got: %q", all)
	}
}

func TestBackgroundProcessManager_UnknownProcess(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	_, err := mgr.FormatProcess("bg-999")
	if err == nil {
		t.Fatal("expected error for unknown process")
	}
}

func TestBackgroundProcessManager_KeepAliveOutputCapture(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	_, err := mgr.Start("", "echo keepalive-output && echo line2", true, 0)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// keep_alive uses file-based capture, not ring buffers.
	status, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess failed: %v", err)
	}
	if !strings.Contains(status, "keepalive-output") {
		t.Errorf("expected output captured from log file, got: %s", status)
	}
	if !strings.Contains(status, "line2") {
		t.Errorf("expected multi-line output from log file, got: %s", status)
	}
	// Verify it says "Last output" (combined) not "Last stdout" (pipe-based).
	if !strings.Contains(status, "Last output:") {
		t.Errorf("expected 'Last output:' for file-based capture, got: %s", status)
	}

	// Clean up.
	mgr.mu.Lock()
	mgr.processes["bg-1"].KeepAlive = false
	mgr.mu.Unlock()
	mgr.Cleanup()
}

func TestBackgroundProcessManager_KeepAlive(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	_, err := mgr.Start("", "sleep 60", true, 0)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify keep_alive is shown in status.
	status, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess failed: %v", err)
	}
	if !strings.Contains(status, "keep_alive") {
		t.Errorf("expected keep_alive indicator in status, got: %s", status)
	}

	// Cleanup should NOT kill keep_alive processes.
	mgr.Cleanup()

	// Process should still be running.
	status2, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess after cleanup failed: %v", err)
	}
	if !strings.Contains(status2, "running") {
		t.Errorf("expected still running after cleanup, got: %s", status2)
	}

	// Manual cleanup: kill it so the test doesn't leak.
	mgr.mu.Lock()
	proc := mgr.processes["bg-1"]
	proc.KeepAlive = false
	mgr.mu.Unlock()
	mgr.Cleanup()
}

func TestBackgroundProcessManager_Timeout(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()

	_, err := mgr.Start("", "sleep 60", false, 1*time.Second)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for timeout to fire.
	time.Sleep(2 * time.Second)

	status, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess failed: %v", err)
	}
	if !strings.Contains(status, "failed") {
		t.Errorf("expected failed after timeout, got: %s", status)
	}
}

func TestBackgroundProcessManager_CapturesMultilineOutput(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()

	// Generate many lines of output.
	_, err := mgr.Start("", "for i in $(seq 1 50); do echo line-$i; done", false, 0)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	status, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess failed: %v", err)
	}
	// Should show the last 20 lines (line-31 through line-50).
	if !strings.Contains(status, "line-50") {
		t.Errorf("expected last line in output, got: %s", status)
	}
}

// --- Bash Tool Background Integration Tests ---

func TestBash_Background(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	tool := Bash(WithBackgroundProcessManager(mgr))

	result := call(t, tool, `{"command":"echo bg-test","background":true}`)
	if !strings.Contains(result, "bg-1") {
		t.Errorf("expected bg-1 in result, got: %s", result)
	}
	if !strings.Contains(result, "Background process started") {
		t.Errorf("expected start message, got: %s", result)
	}
}

func TestBash_BackgroundWithKeepAlive(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	tool := Bash(WithBackgroundProcessManager(mgr))

	result := call(t, tool, `{"command":"sleep 60","background":true,"keep_alive":true}`)
	if !strings.Contains(result, "bg-1") {
		t.Errorf("expected bg-1, got: %s", result)
	}

	status, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess failed: %v", err)
	}
	if !strings.Contains(status, "keep_alive") {
		t.Errorf("expected keep_alive, got: %s", status)
	}

	// Clean up the sleep process.
	mgr.mu.Lock()
	mgr.processes["bg-1"].KeepAlive = false
	mgr.mu.Unlock()
}

func TestBash_BackgroundNoManager(t *testing.T) {
	// Without a manager, background should return a retry error.
	tool := Bash()
	err := callErr(t, tool, `{"command":"echo test","background":true}`)
	if err == nil {
		t.Fatal("expected error without manager")
	}
}

func TestBash_BackgroundFalseRunsForeground(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	tool := Bash(WithBackgroundProcessManager(mgr))

	// background=false should run as normal foreground.
	result := call(t, tool, `{"command":"echo foreground","background":false}`)
	if !strings.Contains(result, "foreground") {
		t.Errorf("expected foreground output, got: %s", result)
	}
	// No background processes should have been created.
	all := mgr.FormatAll()
	if all != "No background processes." {
		t.Errorf("expected no background processes, got: %s", all)
	}
}

func TestBash_NilRunContextRunsNormally(t *testing.T) {
	tool := Bash()

	result, err := tool.Handler(context.Background(), nil, `{"command":"echo nil-rc"}`)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "nil-rc") {
		t.Errorf("expected normal output, got: %s", resultStr)
	}
}

// --- BashStatus Tool Tests ---

func TestBashStatus_All(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	_, _ = mgr.Start("", "echo one", false, 0)
	time.Sleep(500 * time.Millisecond)

	tool := BashStatus(WithBackgroundProcessManager(mgr))
	result := call(t, tool, `{"id":"all"}`)
	if !strings.Contains(result, "bg-1") {
		t.Errorf("expected bg-1 in all output, got: %s", result)
	}
}

func TestBashStatus_Specific(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	_, _ = mgr.Start("", "echo specific-test", false, 0)
	time.Sleep(500 * time.Millisecond)

	tool := BashStatus(WithBackgroundProcessManager(mgr))
	result := call(t, tool, `{"id":"bg-1"}`)
	if !strings.Contains(result, "specific-test") {
		t.Errorf("expected command in output, got: %s", result)
	}
}

func TestBashStatus_UnknownID(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	tool := BashStatus(WithBackgroundProcessManager(mgr))
	err := callErr(t, tool, `{"id":"bg-999"}`)
	if err == nil {
		t.Fatal("expected error for unknown ID")
	}
}

func TestBashStatus_EmptyID(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	tool := BashStatus(WithBackgroundProcessManager(mgr))
	err := callErr(t, tool, `{"id":""}`)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

// --- Adopt Tests ---

func TestBackgroundProcessManager_Adopt(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()

	// Start a process externally (simulating foreground bash).
	cmd := exec.CommandContext(context.Background(), "bash", "-c", "echo adopted-output && echo err-output >&2")
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	id, err := mgr.Adopt(cmd, stdoutPipe, stderrPipe, "echo adopted-output")
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	if id != "bg-1" {
		t.Errorf("expected bg-1, got %s", id)
	}

	// Wait for process to complete.
	time.Sleep(500 * time.Millisecond)

	status, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess failed: %v", err)
	}
	if !strings.Contains(status, "completed") {
		t.Errorf("expected completed, got: %s", status)
	}
	if !strings.Contains(status, "adopted-output") {
		t.Errorf("expected stdout captured, got: %s", status)
	}
	if !strings.Contains(status, "err-output") {
		t.Errorf("expected stderr captured, got: %s", status)
	}
}

// --- Detach Tests ---

func TestBash_DetachMovesToBackground(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	tool := Bash(WithBackgroundProcessManager(mgr))

	// Create a detach channel that closes immediately — simulates UI pressing
	// "move to background" right away.
	detach := make(chan struct{})
	close(detach)

	ctx := context.Background()
	rc := &core.RunContext{Detach: detach}
	result, err := tool.Handler(ctx, rc, `{"command":"sleep 60"}`)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "moved to background") {
		t.Errorf("expected detach message, got: %s", resultStr)
	}
	if !strings.Contains(resultStr, "bg-1") {
		t.Errorf("expected bg-1 in result, got: %s", resultStr)
	}

	// Verify the process is tracked.
	status, err := mgr.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("FormatProcess failed: %v", err)
	}
	if !strings.Contains(status, "running") {
		t.Errorf("expected running status, got: %s", status)
	}
}

func TestBash_DetachPreservesOutputForBashStatus(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	tool := Bash(WithBackgroundProcessManager(mgr))

	detach := make(chan struct{})
	close(detach)

	ctx := context.Background()
	rc := &core.RunContext{Detach: detach}
	result, err := tool.Handler(ctx, rc, `{"command":"for i in 1 2 3; do echo line-$i; sleep 0.2; done"}`)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	resultStr := result.(string)
	if !strings.Contains(resultStr, "moved to background") {
		t.Fatalf("expected detach result, got: %s", resultStr)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		status, err := mgr.FormatProcess("bg-1")
		if err != nil {
			t.Fatalf("FormatProcess failed: %v", err)
		}
		if strings.Contains(status, "completed") {
			if !strings.Contains(status, "line-1") || !strings.Contains(status, "line-3") {
				t.Fatalf("expected detached output to remain available, got: %s", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for detached process to complete")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestBash_DetachFallbackStillHonorsTimeout(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	tool := Bash(WithBackgroundProcessManager(mgr))

	for i := range maxBackgroundProcesses {
		_, err := mgr.Start("", "sleep 60", false, 0)
		if err != nil {
			t.Fatalf("prefill background process %d failed: %v", i, err)
		}
	}

	detach := make(chan struct{})
	close(detach)

	ctx := context.Background()
	rc := &core.RunContext{Detach: detach}
	result, err := tool.Handler(ctx, rc, `{"command":"sleep 3","timeout":1}`)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "timed out") {
		t.Fatalf("expected foreground fallback to honor timeout, got: %s", resultStr)
	}
}

func TestBash_NoDetachRunsNormally(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	tool := Bash(WithBackgroundProcessManager(mgr))

	// nil Detach channel — should run to completion as normal.
	ctx := context.Background()
	rc := &core.RunContext{}
	result, err := tool.Handler(ctx, rc, `{"command":"echo normal-output"}`)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	resultStr := result.(string)
	if !strings.Contains(resultStr, "normal-output") {
		t.Errorf("expected normal output, got: %s", resultStr)
	}
	// No background processes should exist.
	all := mgr.FormatAll()
	if all != "No background processes." {
		t.Errorf("expected no background processes, got: %s", all)
	}
}

func TestBash_DetachNotFiredRunsNormally(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	tool := Bash(WithBackgroundProcessManager(mgr))

	// Detach channel exists but is never closed — command completes normally.
	detach := make(chan struct{})
	ctx := context.Background()
	rc := &core.RunContext{Detach: detach}
	result, err := tool.Handler(ctx, rc, `{"command":"echo fast-command"}`)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	resultStr := result.(string)
	if !strings.Contains(resultStr, "fast-command") {
		t.Errorf("expected output, got: %s", resultStr)
	}
	// No background processes — command finished before detach.
	all := mgr.FormatAll()
	if all != "No background processes." {
		t.Errorf("expected no background processes, got: %s", all)
	}
}

func TestToolset_CleansUpBackgroundProcessesOnRunEnd(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	defer mgr.Cleanup()
	model := core.NewTestModel(
		core.ToolCallResponse("bash", `{"command":"sleep 60","background":true}`),
		core.TextResponse("done"),
	)

	// Lifecycle hooks live at the agent level (via AgentOptions), not on the
	// Toolset. Wire them explicitly here to mirror what AgentOptions does.
	agent := core.NewAgent[string](model,
		core.WithToolsets[string](Toolset(WithBackgroundProcessManager(mgr))),
		core.WithHooks[string](core.Hook{
			OnRunEnd: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage, _ error) {
				mgr.Cleanup()
			},
		}),
	)

	result, err := agent.Run(context.Background(), "start a background process")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("unexpected output: %q", result.Output)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		status, err := mgr.FormatProcess("bg-1")
		if err != nil {
			t.Fatalf("FormatProcess failed: %v", err)
		}
		if !strings.Contains(status, "running") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background process was not cleaned up after run end: %s", status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPerWorkerManagerIsolation(t *testing.T) {
	// Simulate two workers, each with their own BackgroundProcessManager
	// (as created by ToolsetFactory). One worker ending should NOT kill
	// the other worker's background processes.
	mgrA := NewBackgroundProcessManager()
	mgrB := NewBackgroundProcessManager()

	// Worker A starts a long-running background process.
	_, err := mgrA.Start("", "sleep 60", false, 0)
	if err != nil {
		t.Fatalf("mgrA.Start failed: %v", err)
	}

	// Worker B starts a background process.
	_, err = mgrB.Start("", "sleep 60", false, 0)
	if err != nil {
		t.Fatalf("mgrB.Start failed: %v", err)
	}

	// Worker B finishes: its cleanup kills only its own processes.
	mgrB.Cleanup()

	// Worker A's process should still be running.
	statusA, err := mgrA.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("mgrA.FormatProcess failed: %v", err)
	}
	if !strings.Contains(statusA, "running") {
		t.Fatalf("expected mgrA process still running after mgrB cleanup, got: %s", statusA)
	}

	// Worker B's process should be killed.
	time.Sleep(200 * time.Millisecond)
	statusB, _ := mgrB.FormatProcess("bg-1")
	if strings.Contains(statusB, "running") {
		t.Fatalf("expected mgrB process killed after cleanup, got: %s", statusB)
	}

	// Worker A's bash_status should NOT see Worker B's processes.
	allA := mgrA.FormatAll()
	if strings.Contains(allA, "sleep 60") && mgrA.FormatAll() == mgrB.FormatAll() {
		t.Fatalf("expected isolated process views between managers")
	}

	// Clean up worker A.
	mgrA.Cleanup()
}

func TestToolsetFactory_PerWorkerHooksIsolation(t *testing.T) {
	// Build two toolsets from a factory (simulating ToolsetFactory),
	// each with their own manager and hooks. One worker's OnRunEnd
	// should only clean up its own background processes.
	makeToolset := func() (*core.Toolset, *BackgroundProcessManager) {
		mgr := NewBackgroundProcessManager()
		ts := Toolset(WithBackgroundProcessManager(mgr))
		ts.Hooks = []core.Hook{{
			OnRunEnd: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage, _ error) {
				mgr.Cleanup()
			},
		}}
		return ts, mgr
	}

	tsA, mgrA := makeToolset()
	tsB, mgrB := makeToolset()

	// Start a background process in each manager.
	_, err := mgrA.Start("", "sleep 60", false, 0)
	if err != nil {
		t.Fatalf("mgrA.Start failed: %v", err)
	}
	_, err = mgrB.Start("", "sleep 60", false, 0)
	if err != nil {
		t.Fatalf("mgrB.Start failed: %v", err)
	}

	// Fire Worker B's OnRunEnd hook — should only clean up mgrB.
	for _, h := range tsB.Hooks {
		if h.OnRunEnd != nil {
			h.OnRunEnd(context.Background(), &core.RunContext{}, nil, nil)
		}
	}
	time.Sleep(200 * time.Millisecond)

	// Worker A's process should still be running.
	statusA, err := mgrA.FormatProcess("bg-1")
	if err != nil {
		t.Fatalf("mgrA.FormatProcess failed: %v", err)
	}
	if !strings.Contains(statusA, "running") {
		t.Fatalf("expected mgrA process still running after mgrB hook fired, got: %s", statusA)
	}

	// Worker B's process should be killed.
	statusB, _ := mgrB.FormatProcess("bg-1")
	if strings.Contains(statusB, "running") {
		t.Fatalf("expected mgrB process cleaned up, got: %s", statusB)
	}

	// Fire Worker A's hook — should clean up mgrA.
	for _, h := range tsA.Hooks {
		if h.OnRunEnd != nil {
			h.OnRunEnd(context.Background(), &core.RunContext{}, nil, nil)
		}
	}
	time.Sleep(200 * time.Millisecond)

	statusA2, _ := mgrA.FormatProcess("bg-1")
	if strings.Contains(statusA2, "running") {
		t.Fatalf("expected mgrA process cleaned up after its hook fired, got: %s", statusA2)
	}
}
