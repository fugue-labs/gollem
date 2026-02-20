package orchestration_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/orchestration"
)

func TestPipeline_Sequential(t *testing.T) {
	p := orchestration.NewPipeline(
		orchestration.TransformStep(strings.ToUpper),
		orchestration.TransformStep(func(s string) string { return s + "!" }),
	)

	result, err := p.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result != "HELLO!" {
		t.Errorf("expected 'HELLO!', got %q", result)
	}
}

func TestPipeline_Then(t *testing.T) {
	p := orchestration.NewPipeline(orchestration.TransformStep(strings.ToUpper))
	p2 := p.Then(orchestration.TransformStep(func(s string) string { return "[" + s + "]" }))

	result, err := p2.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result != "[HELLO]" {
		t.Errorf("expected '[HELLO]', got %q", result)
	}

	// Original pipeline unchanged.
	orig, err := p.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if orig != "HELLO" {
		t.Errorf("expected 'HELLO' from original, got %q", orig)
	}
}

func TestPipeline_AgentStep(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("agent output"))
	agent := core.NewAgent[string](model)

	step := orchestration.AgentStep(agent)
	result, err := step(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if result != "agent output" {
		t.Errorf("expected 'agent output', got %q", result)
	}
}

func TestPipeline_TransformStep(t *testing.T) {
	step := orchestration.TransformStep(func(s string) string {
		return strings.ReplaceAll(s, "world", "Go")
	})

	result, err := step(context.Background(), "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello Go" {
		t.Errorf("expected 'hello Go', got %q", result)
	}
}

func TestPipeline_ParallelSteps(t *testing.T) {
	step := orchestration.ParallelSteps(
		orchestration.TransformStep(func(s string) string { return "A:" + s }),
		orchestration.TransformStep(func(s string) string { return "B:" + s }),
	)

	result, err := step(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "A:input") || !strings.Contains(result, "B:input") {
		t.Errorf("expected both parallel results, got %q", result)
	}
}

func TestPipeline_ConditionalStep(t *testing.T) {
	step := orchestration.ConditionalStep(
		func(s string) bool { return len(s) > 5 },
		orchestration.TransformStep(func(s string) string { return "long: " + s }),
		orchestration.TransformStep(func(s string) string { return "short: " + s }),
	)

	long, _ := step(context.Background(), "abcdefgh")
	if long != "long: abcdefgh" {
		t.Errorf("expected 'long: abcdefgh', got %q", long)
	}

	short, _ := step(context.Background(), "hi")
	if short != "short: hi" {
		t.Errorf("expected 'short: hi', got %q", short)
	}
}

func TestPipeline_ErrorPropagation(t *testing.T) {
	p := orchestration.NewPipeline(
		func(_ context.Context, _ string) (string, error) {
			return "", errors.New("step failed")
		},
		orchestration.TransformStep(func(s string) string { return "should not reach" }),
	)

	_, err := p.Run(context.Background(), "input")
	if err == nil {
		t.Fatal("expected error from failed step")
	}
	if err.Error() != "step failed" {
		t.Errorf("expected 'step failed', got %q", err.Error())
	}
}
