package core

import (
	"context"
	"math"
	"sync"
	"testing"
)

func TestCostTracker_Record(t *testing.T) {
	pricing := map[string]ModelPricing{
		"test-model": {InputTokenCost: 0.000003, OutputTokenCost: 0.000015},
	}
	tracker := NewCostTracker(pricing)

	usage := RunUsage{}
	usage.InputTokens = 1000
	usage.OutputTokens = 500
	tracker.Record("test-model", usage)

	expected := 1000*0.000003 + 500*0.000015
	if math.Abs(tracker.TotalCost()-expected) > 0.0001 {
		t.Errorf("expected cost %f, got %f", expected, tracker.TotalCost())
	}
}

func TestCostTracker_Breakdown(t *testing.T) {
	pricing := map[string]ModelPricing{
		"model-a": {InputTokenCost: 0.000001, OutputTokenCost: 0.000002},
		"model-b": {InputTokenCost: 0.000010, OutputTokenCost: 0.000020},
	}
	tracker := NewCostTracker(pricing)

	usageA := RunUsage{}
	usageA.InputTokens = 100
	usageA.OutputTokens = 50
	tracker.Record("model-a", usageA)

	usageB := RunUsage{}
	usageB.InputTokens = 200
	usageB.OutputTokens = 100
	tracker.Record("model-b", usageB)

	breakdown := tracker.CostBreakdown()
	if len(breakdown) != 2 {
		t.Errorf("expected 2 models in breakdown, got %d", len(breakdown))
	}
	if breakdown["model-a"] <= 0 {
		t.Error("expected positive cost for model-a")
	}
	if breakdown["model-b"] <= 0 {
		t.Error("expected positive cost for model-b")
	}
}

func TestCostTracker_UnknownModel(t *testing.T) {
	pricing := map[string]ModelPricing{
		"known": {InputTokenCost: 0.000001, OutputTokenCost: 0.000002},
	}
	tracker := NewCostTracker(pricing)

	usage := RunUsage{}
	usage.InputTokens = 1000
	tracker.Record("unknown-model", usage)

	if tracker.TotalCost() != 0 {
		t.Errorf("expected 0 cost for unknown model, got %f", tracker.TotalCost())
	}
}

func TestCostTracker_ThreadSafe(t *testing.T) {
	pricing := map[string]ModelPricing{
		"test-model": {InputTokenCost: 0.000001, OutputTokenCost: 0.000002},
	}
	tracker := NewCostTracker(pricing)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			usage := RunUsage{}
			usage.InputTokens = 100
			usage.OutputTokens = 50
			tracker.Record("test-model", usage)
		}()
	}
	wg.Wait()

	if tracker.TotalCost() <= 0 {
		t.Error("expected positive total cost after concurrent recording")
	}
}

func TestCostTracker_AgentIntegration(t *testing.T) {
	pricing := map[string]ModelPricing{
		"test-model": {InputTokenCost: 0.000003, OutputTokenCost: 0.000015},
	}
	tracker := NewCostTracker(pricing)

	resp := TextResponse("done")
	resp.Usage = Usage{InputTokens: 100, OutputTokens: 50}

	model := NewTestModel(resp)
	agent := NewAgent[string](model,
		WithCostTracker[string](tracker),
	)

	result, err := agent.Run(context.Background(), "test cost tracking")
	if err != nil {
		t.Fatal(err)
	}
	if result.Cost == nil {
		t.Fatal("expected Cost on RunResult")
	}
	if result.Cost.TotalCost <= 0 {
		t.Error("expected positive total cost")
	}
	if result.Cost.Currency != "USD" {
		t.Errorf("expected USD, got %q", result.Cost.Currency)
	}
}

func TestCostTracker_ZeroPricing(t *testing.T) {
	pricing := map[string]ModelPricing{
		"free-model": {InputTokenCost: 0, OutputTokenCost: 0},
	}
	tracker := NewCostTracker(pricing)

	usage := RunUsage{}
	usage.InputTokens = 10000
	usage.OutputTokens = 5000
	tracker.Record("free-model", usage)

	if tracker.TotalCost() != 0 {
		t.Errorf("expected 0 cost for zero pricing, got %f", tracker.TotalCost())
	}
}
