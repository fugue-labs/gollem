//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestGetDeps verifies typed dependency injection.
func TestGetDeps(t *testing.T) {
	anthropicOnly(t)

	type AppConfig struct {
		Version string
		Debug   bool
	}

	var capturedVersion string
	var capturedDebug bool

	type EmptyToolParams struct{}
	configTool := core.FuncTool[EmptyToolParams]("get_config", "Get app configuration",
		func(ctx context.Context, rc *core.RunContext, p EmptyToolParams) (string, error) {
			deps := core.GetDeps[*AppConfig](rc)
			capturedVersion = deps.Version
			capturedDebug = deps.Debug
			return "Version: " + deps.Version, nil
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](configTool),
		core.WithDeps[string](&AppConfig{Version: "1.2.3", Debug: true}),
	)

	_, err := agent.Run(ctx, "Use the get_config tool to check the app configuration.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if capturedVersion != "1.2.3" {
		t.Errorf("expected version '1.2.3', got %q", capturedVersion)
	}
	if !capturedDebug {
		t.Error("expected debug=true")
	}

	t.Logf("Captured: Version=%q Debug=%v", capturedVersion, capturedDebug)
}

// TestTryGetDeps verifies TryGetDeps returns (value, true) when set and (zero, false) when not.
func TestTryGetDeps(t *testing.T) {
	anthropicOnly(t)

	type MyDeps struct {
		Value string
	}

	var found bool
	var foundValue string

	type EmptyToolParams struct{}
	tool := core.FuncTool[EmptyToolParams]("check_deps", "Check deps",
		func(ctx context.Context, rc *core.RunContext, p EmptyToolParams) (string, error) {
			deps, ok := core.TryGetDeps[*MyDeps](rc)
			found = ok
			if ok {
				foundValue = deps.Value
			}
			return "checked", nil
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// With deps set.
	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](tool),
		core.WithDeps[string](&MyDeps{Value: "hello"}),
	)

	_, err := agent.Run(ctx, "Use the check_deps tool.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if !found {
		t.Error("expected TryGetDeps to return true when deps are set")
	}
	if foundValue != "hello" {
		t.Errorf("expected value 'hello', got %q", foundValue)
	}

	t.Logf("Found=%v Value=%q", found, foundValue)
}

// TestRunDepsOverride verifies that WithRunDeps overrides agent-level deps.
func TestRunDepsOverride(t *testing.T) {
	anthropicOnly(t)

	type Config struct {
		Mode string
	}

	var capturedMode string

	type EmptyToolParams struct{}
	tool := core.FuncTool[EmptyToolParams]("get_mode", "Get mode",
		func(ctx context.Context, rc *core.RunContext, p EmptyToolParams) (string, error) {
			deps := core.GetDeps[*Config](rc)
			capturedMode = deps.Mode
			return "mode: " + deps.Mode, nil
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](tool),
		core.WithDeps[string](&Config{Mode: "default"}),
	)

	// Override deps at run level.
	_, err := agent.Run(ctx, "Use the get_mode tool.",
		core.WithRunDeps(&Config{Mode: "override"}),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("agent.Run failed: %v", err)
	}

	if capturedMode != "override" {
		t.Errorf("expected mode 'override', got %q", capturedMode)
	}

	t.Logf("Captured mode: %q", capturedMode)
}

// TestConcurrentAgentRuns verifies running the same agent concurrently is safe.
func TestConcurrentAgentRuns(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	agent := core.NewAgent[string](newAnthropicProvider())

	// Run 3 concurrent requests.
	results := agent.RunBatch(ctx, []string{
		"Say 'one'",
		"Say 'two'",
		"Say 'three'",
	})

	for i, r := range results {
		if r.Err != nil {
			skipOnAccountError(t, r.Err)
			t.Errorf("concurrent run[%d] failed: %v", i, r.Err)
			continue
		}
		if r.Result == nil || r.Result.Output == "" {
			t.Errorf("concurrent run[%d] has empty result", i)
		}
		t.Logf("concurrent run[%d]: %q", i, r.Result.Output)
	}
}
