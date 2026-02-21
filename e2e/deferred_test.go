//go:build e2e

package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TestDeferredToolBasic verifies the deferred tool execution pattern.
// A tool returns CallDeferred, the run yields DeferredToolRequests,
// and the run resumes with WithDeferredResults.
func TestDeferredToolBasic(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	type ApprovalParams struct {
		Action string `json:"action" jsonschema:"description=Action to approve"`
	}

	deferredTool := core.FuncTool[ApprovalParams]("request_approval", "Request human approval for an action",
		func(ctx context.Context, rc *core.RunContext, p ApprovalParams) (string, error) {
			return "", &core.CallDeferred{Message: "awaiting human approval for: " + p.Action}
		},
	)

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](deferredTool),
	)

	// First run - should yield deferred requests.
	_, err := agent.Run(ctx, "Use the request_approval tool to approve 'deploy to production'.")
	if err == nil {
		t.Fatal("expected ErrDeferred, got nil error")
	}

	var deferredErr *core.ErrDeferred[string]
	if !errors.As(err, &deferredErr) {
		skipOnAccountError(t, err)
		t.Fatalf("expected ErrDeferred, got: %T: %v", err, err)
	}

	reqs := deferredErr.Result.DeferredRequests
	if len(reqs) == 0 {
		t.Fatal("expected at least one deferred request")
	}

	t.Logf("Deferred requests: %+v", reqs)
	t.Logf("Messages count: %d", len(deferredErr.Result.Messages))

	// Resume with deferred results.
	result, err := agent.Run(ctx, "The approval was granted, summarize the result.",
		core.WithMessages(deferredErr.Result.Messages...),
		core.WithDeferredResults(core.DeferredToolResult{
			ToolCallID: reqs[0].ToolCallID,
			Content:    "Approved: deployment to production authorized",
		}),
	)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("resumed run failed: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output after resuming")
	}

	t.Logf("Resumed output: %q", result.Output)
}
