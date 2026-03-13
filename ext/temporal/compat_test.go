package temporal

import (
	"bytes"
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

func TestValidateCompatibility_NilAgent(t *testing.T) {
	report := CompatibilityReportFor[string](nil)
	if report.Supported() {
		t.Fatal("expected nil agent to be unsupported")
	}
	if err := ValidateCompatibility[string](nil); err == nil {
		t.Fatal("expected nil-agent compatibility error")
	}
}

func TestValidateCompatibility_FormerlyBlockedFeaturesSupported(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	agent := core.NewAgent[string](model,
		core.WithEventBus[string](core.NewEventBus()),
		core.WithDeps[string](map[string]string{"env": "test"}),
		core.WithAutoContext[string](core.AutoContextConfig{
			MaxTokens: 10,
			KeepLastN: 4,
		}),
		core.WithAgentMiddleware[string](core.StreamOnlyMiddleware(
			func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters, next core.AgentStreamFunc) (core.StreamedResponse, error) {
				return next(ctx, messages, settings, params)
			},
		)),
	)

	report := CompatibilityReportFor(agent)
	if !report.Supported() {
		t.Fatalf("expected formerly blocked features to be supported, got report %+v", report)
	}
	if err := ValidateCompatibility(agent); err != nil {
		t.Fatalf("expected nil compatibility error, got %v", err)
	}
}

func TestNewTemporalAgent_PanicsOnNilAgent(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	_ = model

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil agent")
		}
	}()

	_ = NewTemporalAgent[string](nil, WithName("incompatible"))
}

func TestValidateCompatibility_TracingAndCostTrackingSupported(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	agent := core.NewAgent[string](model,
		core.WithTracing[string](),
		core.WithTraceExporter[string](core.NewConsoleExporter(&bytes.Buffer{})),
		core.WithCostTracker[string](core.NewCostTracker(map[string]core.ModelPricing{
			model.ModelName(): {
				InputTokenCost:  0.000001,
				OutputTokenCost: 0.000002,
			},
		})),
	)

	if err := ValidateCompatibility(agent); err != nil {
		t.Fatalf("expected tracing and cost tracking to be supported, got %v", err)
	}
}

func TestValidateCompatibility_UsageQuotaSupported(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	agent := core.NewAgent[string](model,
		core.WithUsageQuota[string](core.UsageQuota{MaxRequests: 1}),
	)

	if err := ValidateCompatibility(agent); err != nil {
		t.Fatalf("expected usage quota to be supported, got %v", err)
	}
}

func TestValidateCompatibility_ToolApprovalSupportedWithoutCallback(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	tool := core.FuncTool[struct{}]("dangerous_action", "Dangerous action", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	}, core.WithRequiresApproval())

	agent := core.NewAgent[string](model, core.WithTools[string](tool))
	if err := ValidateCompatibility(agent); err != nil {
		t.Fatalf("expected approval-required tool to be supported, got %v", err)
	}
}

func TestValidateCompatibility_CallbackFeaturesSupported(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	tool := core.FuncTool[struct{}]("dangerous_action", "Dangerous action", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	}, core.WithRequiresApproval())

	agent := core.NewAgent[string](model,
		core.WithTools[string](tool),
		core.WithDynamicSystemPrompt[string](func(_ context.Context, _ *core.RunContext) (string, error) {
			return "dynamic", nil
		}),
		core.WithHistoryProcessor[string](func(_ context.Context, messages []core.ModelMessage) ([]core.ModelMessage, error) {
			return messages, nil
		}),
		core.WithMessageInterceptor[string](func(_ context.Context, messages []core.ModelMessage) core.InterceptResult {
			return core.InterceptResult{Action: core.MessageAllow, Messages: messages}
		}),
		core.WithResponseInterceptor[string](func(_ context.Context, _ *core.ModelResponse) core.InterceptResult {
			return core.InterceptResult{Action: core.MessageAllow}
		}),
		core.WithOutputValidator[string](func(_ context.Context, _ *core.RunContext, output string) (string, error) {
			return output, nil
		}),
		core.WithOutputRepair[string](func(_ context.Context, raw string, _ error) (string, error) {
			return raw, nil
		}),
		core.WithGlobalToolResultValidator[string](func(_ context.Context, _ *core.RunContext, _ string, _ string) error {
			return nil
		}),
		core.WithToolApproval[string](func(_ context.Context, _ string, _ string) (bool, error) {
			return true, nil
		}),
	)

	if err := ValidateCompatibility(agent); err != nil {
		t.Fatalf("expected callback-heavy compatible agent, got %v", err)
	}
}

func TestValidateCompatibility_ToolsetsAndExecutionHooksSupported(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	tool := core.FuncTool[struct{}]("echo", "Echo", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	toolset := core.NewToolset("extra", tool)
	kb := core.NewStaticKnowledgeBase("context")

	agent := core.NewAgent[string](model,
		core.WithToolsets[string](toolset),
		core.WithInputGuardrail[string]("trim", func(_ context.Context, prompt string) (string, error) {
			return strings.TrimSpace(prompt), nil
		}),
		core.WithTurnGuardrail[string]("pass", func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage) error {
			return nil
		}),
		core.WithRunCondition[string](core.TextContains("ok")),
		core.WithHooks[string](core.Hook{}),
		core.WithKnowledgeBase[string](kb),
		core.WithKnowledgeBaseAutoStore[string](),
	)

	if err := ValidateCompatibility(agent); err != nil {
		t.Fatalf("expected toolsets, guardrails, hooks, run conditions, and knowledge base to be supported, got %v", err)
	}
}

func TestValidateCompatibility_ToolPrepareAndRequestMiddlewareSupported(t *testing.T) {
	model := core.NewTestModel(core.TextResponse("ok"))
	tool := core.FuncTool[struct{}]("echo", "Echo", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	tool.PrepareFunc = func(_ context.Context, _ *core.RunContext, def core.ToolDefinition) *core.ToolDefinition {
		def.Description = "prepared"
		return &def
	}

	agent := core.NewAgent[string](model,
		core.WithTools[string](tool),
		core.WithToolsPrepare[string](func(_ context.Context, _ *core.RunContext, defs []core.ToolDefinition) []core.ToolDefinition {
			return defs
		}),
		core.WithAgentMiddleware[string](core.RequestOnlyMiddleware(
			func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters, next func(context.Context, []core.ModelMessage, *core.ModelSettings, *core.ModelRequestParameters) (*core.ModelResponse, error)) (*core.ModelResponse, error) {
				return next(ctx, messages, settings, params)
			},
		)),
	)

	if err := ValidateCompatibility(agent); err != nil {
		t.Fatalf("expected tool preparation and request middleware to be supported, got %v", err)
	}
}

func TestValidateAgentConfig_PassthroughRejected(t *testing.T) {
	err := validateAgentConfig(&agentConfig{
		passthroughTools: map[string]bool{
			"fast_tool": true,
			"slow_tool": true,
		},
	})
	if err == nil {
		t.Fatal("expected passthrough tool config to be rejected")
	}
	if !strings.Contains(err.Error(), "WithToolPassthrough") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "fast_tool") || !strings.Contains(err.Error(), "slow_tool") {
		t.Fatalf("expected tool names in error, got %v", err)
	}
}
