package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunBatch_AllSucceed(t *testing.T) {
	model := NewTestModel(TextResponse("result"))
	agent := NewAgent[string](model)

	prompts := []string{"a", "b", "c", "d", "e"}
	results := agent.RunBatch(context.Background(), prompts)

	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("result %d: unexpected error: %v", i, r.Err)
		}
		if r.Result == nil {
			t.Errorf("result %d: expected non-nil result", i)
		}
		if r.Index != i {
			t.Errorf("result %d: expected index %d, got %d", i, i, r.Index)
		}
	}
}

func TestRunBatch_SomeFail(t *testing.T) {
	model := NewTestModel(TextResponse("ok"))

	// Fail prompts that contain "FAIL".
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("fail-check", func(ctx context.Context, prompt string) (string, error) {
			if prompt == "FAIL" {
				return "", context.DeadlineExceeded
			}
			return prompt, nil
		}),
	)

	results := agent.RunBatch(context.Background(), []string{"ok1", "FAIL", "ok2"},
		WithBatchConcurrency(1), // sequential to make deterministic
	)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First and third should succeed.
	if results[0].Err != nil {
		t.Errorf("result 0: unexpected error: %v", results[0].Err)
	}
	// Second should fail.
	if results[1].Err == nil {
		t.Error("result 1: expected error")
	}
	if results[2].Err != nil {
		t.Errorf("result 2: unexpected error: %v", results[2].Err)
	}
}

func TestRunBatch_Concurrency(t *testing.T) {
	var maxConcurrent int64
	var current int64

	model := NewTestModel(TextResponse("ok"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("track", func(ctx context.Context, prompt string) (string, error) {
			c := atomic.AddInt64(&current, 1)
			// Track max concurrency.
			for {
				m := atomic.LoadInt64(&maxConcurrent)
				if c <= m || atomic.CompareAndSwapInt64(&maxConcurrent, m, c) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt64(&current, -1)
			return prompt, nil
		}),
	)

	prompts := make([]string, 10)
	for i := range prompts {
		prompts[i] = "test"
	}

	results := agent.RunBatch(context.Background(), prompts, WithBatchConcurrency(2))

	if len(results) != 10 {
		t.Fatalf("expected 10 results, got %d", len(results))
	}

	mc := atomic.LoadInt64(&maxConcurrent)
	if mc > 2 {
		t.Errorf("max concurrent should be <= 2, got %d", mc)
	}
}

func TestRunBatch_ContextCancel(t *testing.T) {
	model := NewTestModel(TextResponse("ok"))
	agent := NewAgent[string](model,
		WithInputGuardrail[string]("slow", func(ctx context.Context, prompt string) (string, error) {
			select {
			case <-time.After(5 * time.Second):
				return prompt, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	results := agent.RunBatch(ctx, []string{"a", "b", "c"})
	for _, r := range results {
		if r.Err == nil {
			t.Error("expected error from cancelled context")
		}
	}
}

func TestRunBatch_EmptyPrompts(t *testing.T) {
	model := NewTestModel(TextResponse("ok"))
	agent := NewAgent[string](model)

	results := agent.RunBatch(context.Background(), nil)
	if results != nil {
		t.Errorf("expected nil results for empty prompts, got %d", len(results))
	}

	results = agent.RunBatch(context.Background(), []string{})
	if results != nil {
		t.Errorf("expected nil results for empty slice, got %d", len(results))
	}
}

func TestRunBatch_OrderPreserved(t *testing.T) {
	model := NewTestModel(TextResponse("ok"))
	agent := NewAgent[string](model)

	prompts := []string{"first", "second", "third"}
	results := agent.RunBatch(context.Background(), prompts)

	for i, r := range results {
		if r.Index != i {
			t.Errorf("result %d: expected index %d, got %d", i, i, r.Index)
		}
	}
}
