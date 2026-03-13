package temporal

import (
	"context"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestValidateCompatibility_SupportedAgent(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	tool := core.FuncTool[struct{}]("echo", "Echo", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})

	agent := core.NewAgent[string](model,
		core.WithSystemPrompt[string]("You are concise."),
		core.WithTools[string](tool),
	)

	report := CompatibilityReportFor(agent)
	if !report.Supported() {
		t.Fatalf("expected supported agent, got report: %v", report)
	}
	if err := ValidateCompatibility(agent); err != nil {
		t.Fatalf("expected nil compatibility error, got %v", err)
	}
}

func TestValidateCompatibility_UnsupportedAgent(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	tool := core.FuncTool[struct{}]("echo", "Echo", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	toolset := core.NewToolset("extra", tool)

	agent := core.NewAgent[string](model,
		core.WithToolsets[string](toolset),
		core.WithDynamicSystemPrompt[string](func(_ context.Context, _ *core.RunContext) (string, error) {
			return "dynamic", nil
		}),
	)

	report := CompatibilityReportFor(agent)
	if report.Supported() {
		t.Fatal("expected unsupported agent")
	}
	if len(report.Issues) < 2 {
		t.Fatalf("expected at least 2 compatibility issues, got %d", len(report.Issues))
	}

	err := ValidateCompatibility(agent)
	if err == nil {
		t.Fatal("expected compatibility error, got nil")
	}
	if !strings.Contains(err.Error(), "toolsets") {
		t.Fatalf("expected error to mention toolsets, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "dynamic_system_prompts") {
		t.Fatalf("expected error to mention dynamic system prompts, got %q", err.Error())
	}
}

func TestNewTemporalAgent_PanicsOnUnsupportedAgent(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	agent := core.NewAgent[string](model,
		core.WithMessageInterceptor[string](func(_ context.Context, messages []core.ModelMessage) core.InterceptResult {
			return core.InterceptResult{Action: core.MessageAllow}
		}),
	)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for unsupported agent features")
		}
		msg := r.(error).Error()
		if !strings.Contains(msg, "message_interceptors") {
			t.Fatalf("expected panic to mention message interceptors, got %q", msg)
		}
	}()

	_ = NewTemporalAgent(agent, WithName("incompatible"))
}
