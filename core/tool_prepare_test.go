package core

import (
	"context"
	"testing"
)

// helper: create a simple tool with the given name and optional PrepareFunc.
func makeTool(name string, prepare ToolPrepareFunc) Tool {
	type NoParams struct{}
	t := FuncTool[NoParams](name, "Description for "+name,
		func(_ context.Context, _ NoParams) (string, error) {
			return "ok", nil
		},
	)
	t.PrepareFunc = prepare
	return t
}

// TestToolPrepare_ExcludeTool verifies that when a per-tool PrepareFunc returns
// nil the tool is not sent to the model.
func TestToolPrepare_ExcludeTool(t *testing.T) {
	excluded := makeTool("secret_tool", func(_ context.Context, _ *RunContext, _ ToolDefinition) *ToolDefinition {
		return nil // exclude
	})
	included := makeTool("public_tool", nil)

	model := NewTestModel(TextResponse("done"))
	agent := NewAgent[string](model,
		WithTools[string](excluded, included),
	)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}

	params := calls[0].Parameters
	for _, td := range params.FunctionTools {
		if td.Name == "secret_tool" {
			t.Error("secret_tool should have been excluded by PrepareFunc")
		}
	}

	found := false
	for _, td := range params.FunctionTools {
		if td.Name == "public_tool" {
			found = true
		}
	}
	if !found {
		t.Error("public_tool should be present in FunctionTools")
	}
}

// TestToolPrepare_IncludeTool verifies that when a per-tool PrepareFunc returns
// the definition the tool is available to the model.
func TestToolPrepare_IncludeTool(t *testing.T) {
	tool := makeTool("my_tool", func(_ context.Context, _ *RunContext, def ToolDefinition) *ToolDefinition {
		return &def // include unchanged
	})

	model := NewTestModel(TextResponse("done"))
	agent := NewAgent[string](model,
		WithTools[string](tool),
	)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}

	found := false
	for _, td := range calls[0].Parameters.FunctionTools {
		if td.Name == "my_tool" {
			found = true
		}
	}
	if !found {
		t.Error("my_tool should be included when PrepareFunc returns definition")
	}
}

// TestToolPrepare_ModifyDescription verifies that a PrepareFunc can modify
// the tool definition (e.g. description) and the model receives the modified version.
func TestToolPrepare_ModifyDescription(t *testing.T) {
	tool := makeTool("my_tool", func(_ context.Context, _ *RunContext, def ToolDefinition) *ToolDefinition {
		def.Description = "modified description"
		return &def
	})

	model := NewTestModel(TextResponse("done"))
	agent := NewAgent[string](model,
		WithTools[string](tool),
	)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}

	for _, td := range calls[0].Parameters.FunctionTools {
		if td.Name == "my_tool" {
			if td.Description != "modified description" {
				t.Errorf("description = %q, want %q", td.Description, "modified description")
			}
			return
		}
	}
	t.Error("my_tool not found in FunctionTools")
}

// TestToolsPrepare_AgentWide verifies that the agent-wide AgentToolsPrepareFunc
// can filter out multiple tools at once.
func TestToolsPrepare_AgentWide(t *testing.T) {
	tool1 := makeTool("tool_a", nil)
	tool2 := makeTool("tool_b", nil)
	tool3 := makeTool("tool_c", nil)

	// Agent-wide filter: only keep tool_b.
	agentPrepare := func(_ context.Context, _ *RunContext, defs []ToolDefinition) []ToolDefinition {
		var kept []ToolDefinition
		for _, d := range defs {
			if d.Name == "tool_b" {
				kept = append(kept, d)
			}
		}
		return kept
	}

	model := NewTestModel(TextResponse("done"))
	agent := NewAgent[string](model,
		WithTools[string](tool1, tool2, tool3),
		WithToolsPrepare[string](agentPrepare),
	)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}

	params := calls[0].Parameters
	if len(params.FunctionTools) != 1 {
		t.Fatalf("expected 1 function tool, got %d", len(params.FunctionTools))
	}
	if params.FunctionTools[0].Name != "tool_b" {
		t.Errorf("expected tool_b, got %q", params.FunctionTools[0].Name)
	}
}

// TestToolPrepare_ContextBased verifies that a PrepareFunc can inspect
// RunContext to conditionally include or exclude a tool.
func TestToolPrepare_ContextBased(t *testing.T) {
	// Tool is only available on the second run step.
	tool := makeTool("step2_tool", func(_ context.Context, rc *RunContext, def ToolDefinition) *ToolDefinition {
		if rc.RunStep >= 2 {
			return &def
		}
		return nil
	})
	alwaysTool := makeTool("always_tool", nil)

	// First response triggers a tool call to always_tool so we get a second iteration.
	// Second response is the final text result.
	model := NewTestModel(
		ToolCallResponse("always_tool", `{}`),
		TextResponse("done"),
	)
	agent := NewAgent[string](model,
		WithTools[string](tool, alwaysTool),
	)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := model.Calls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 model calls, got %d", len(calls))
	}

	// First call (step 1): step2_tool should be excluded.
	firstTools := calls[0].Parameters.FunctionTools
	for _, td := range firstTools {
		if td.Name == "step2_tool" {
			t.Error("step2_tool should be excluded on step 1")
		}
	}

	// Second call (step 2): step2_tool should be included.
	secondTools := calls[1].Parameters.FunctionTools
	found := false
	for _, td := range secondTools {
		if td.Name == "step2_tool" {
			found = true
		}
	}
	if !found {
		t.Error("step2_tool should be included on step 2")
	}
}

// TestToolPrepare_NoPrepare verifies that tools without a PrepareFunc
// are always included in the model request.
func TestToolPrepare_NoPrepare(t *testing.T) {
	tool1 := makeTool("plain_a", nil)
	tool2 := makeTool("plain_b", nil)

	model := NewTestModel(TextResponse("done"))
	agent := NewAgent[string](model,
		WithTools[string](tool1, tool2),
	)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one model call")
	}

	params := calls[0].Parameters
	names := make(map[string]bool)
	for _, td := range params.FunctionTools {
		names[td.Name] = true
	}
	if !names["plain_a"] {
		t.Error("plain_a should be present")
	}
	if !names["plain_b"] {
		t.Error("plain_b should be present")
	}
}
