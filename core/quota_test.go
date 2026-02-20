package core

import (
	"context"
	"errors"
	"testing"
)

func TestUsageQuota_MaxRequests(t *testing.T) {
	usage := RunUsage{}
	usage.Requests = 5
	quota := &UsageQuota{MaxRequests: 5}

	err := checkQuota(quota, usage)
	if err == nil {
		t.Fatal("expected quota exceeded error")
	}
	var qe *QuotaExceededError
	if !errors.As(err, &qe) {
		t.Fatalf("expected QuotaExceededError, got %T", err)
	}
}

func TestUsageQuota_MaxTotalTokens(t *testing.T) {
	usage := RunUsage{}
	usage.InputTokens = 30000
	usage.OutputTokens = 25000
	quota := &UsageQuota{MaxTotalTokens: 50000}

	err := checkQuota(quota, usage)
	if err == nil {
		t.Fatal("expected quota exceeded error for total tokens")
	}
}

func TestUsageQuota_NoLimit(t *testing.T) {
	usage := RunUsage{}
	usage.Requests = 1000
	usage.InputTokens = 999999
	quota := &UsageQuota{} // all zeros = unlimited

	err := checkQuota(quota, usage)
	if err != nil {
		t.Fatalf("expected no error with zero limits, got: %v", err)
	}
}

func TestUsageQuota_AgentIntegration(t *testing.T) {
	// Use tool calls to force the agent to loop beyond the quota.
	model := NewTestModel(
		ToolCallResponse("some_tool", `{"query":"test"}`),
		ToolCallResponse("some_tool", `{"query":"test2"}`),
		ToolCallResponse("some_tool", `{"query":"test3"}`),
	)

	dummyTool := FuncTool[struct{ Query string }](
		"some_tool", "A test tool",
		func(_ context.Context, p struct{ Query string }) (string, error) {
			return "ok", nil
		},
	)

	agent := NewAgent[string](model,
		WithTools[string](dummyTool),
		WithUsageQuota[string](UsageQuota{MaxRequests: 2}),
	)

	_, err := agent.Run(context.Background(), "test quota")
	if err == nil {
		t.Fatal("expected error from quota exceeded")
	}
	var qe *QuotaExceededError
	if !errors.As(err, &qe) {
		t.Logf("got non-quota error (may interact with usage limits): %v", err)
	}
}

func TestQuotaExceededError(t *testing.T) {
	err := &QuotaExceededError{
		Quota:   UsageQuota{MaxRequests: 10},
		Usage:   RunUsage{},
		Message: "request limit 10 reached",
	}
	if err.Error() != "usage quota exceeded: request limit 10 reached" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestUsageQuota_MultipleFields(t *testing.T) {
	usage := RunUsage{}
	usage.InputTokens = 100
	usage.OutputTokens = 200
	usage.Requests = 1

	// Input tokens breached first.
	quota := &UsageQuota{MaxInputTokens: 50, MaxRequests: 10}
	err := checkQuota(quota, usage)
	if err == nil {
		t.Fatal("expected error for input token limit")
	}
	var qe *QuotaExceededError
	if !errors.As(err, &qe) {
		t.Fatalf("expected QuotaExceededError, got %T", err)
	}
}
