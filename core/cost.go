package core

import "sync"

// ModelPricing defines per-token costs for a model.
type ModelPricing struct {
	InputTokenCost  float64 // cost per input token (e.g., 0.000003 for $3/1M)
	OutputTokenCost float64 // cost per output token
	CachedInputCost float64 // cost per cached input token read (0 = same as input)
	CacheWriteCost  float64 // cost per cache write token (0 = same as input)
}

// CostTracker tracks estimated costs across model requests.
type CostTracker struct {
	mu       sync.Mutex
	pricing  map[string]ModelPricing
	costs    map[string]float64
	total    float64
	currency string
}

// NewCostTracker creates a cost tracker with per-model pricing.
func NewCostTracker(pricing map[string]ModelPricing) *CostTracker {
	p := make(map[string]ModelPricing, len(pricing))
	for k, v := range pricing {
		p[k] = v
	}
	return &CostTracker{
		pricing:  p,
		costs:    make(map[string]float64),
		currency: "USD",
	}
}

// Record adds a cost entry based on model name and token usage.
func (ct *CostTracker) Record(modelName string, usage RunUsage) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	pricing, ok := ct.pricing[modelName]
	if !ok {
		return // unknown model, zero cost
	}

	inputCost := float64(usage.InputTokens) * pricing.InputTokenCost
	outputCost := float64(usage.OutputTokens) * pricing.OutputTokenCost

	// Apply cached read token discount if configured.
	// InputTokens includes cache read tokens (normalized across providers),
	// so we subtract the difference to apply the discounted rate.
	if pricing.CachedInputCost > 0 && usage.CacheReadTokens > 0 {
		discount := float64(usage.CacheReadTokens) * (pricing.InputTokenCost - pricing.CachedInputCost)
		inputCost -= discount
	}

	// Apply cache write token surcharge if configured.
	// InputTokens includes cache write tokens (normalized across providers),
	// so we add the difference to apply the higher write rate.
	if pricing.CacheWriteCost > 0 && usage.CacheWriteTokens > 0 {
		surcharge := float64(usage.CacheWriteTokens) * (pricing.CacheWriteCost - pricing.InputTokenCost)
		inputCost += surcharge
	}

	cost := inputCost + outputCost
	ct.costs[modelName] += cost
	ct.total += cost
}

// TotalCost returns the total estimated cost.
func (ct *CostTracker) TotalCost() float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.total
}

// CostBreakdown returns per-model cost breakdown.
func (ct *CostTracker) CostBreakdown() map[string]float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	result := make(map[string]float64, len(ct.costs))
	for k, v := range ct.costs {
		result[k] = v
	}
	return result
}

// RunCost returns the cost details for the run.
type RunCost struct {
	TotalCost float64            `json:"total_cost"`
	Breakdown map[string]float64 `json:"breakdown"`
	Currency  string             `json:"currency"`
}

// buildRunCost creates a RunCost from the tracker's current state.
func (ct *CostTracker) buildRunCost() *RunCost {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	breakdown := make(map[string]float64, len(ct.costs))
	for k, v := range ct.costs {
		breakdown[k] = v
	}
	return &RunCost{
		TotalCost: ct.total,
		Breakdown: breakdown,
		Currency:  ct.currency,
	}
}

// Pricing returns a copy of the configured per-model pricing table.
func (ct *CostTracker) Pricing() map[string]ModelPricing {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	pricing := make(map[string]ModelPricing, len(ct.pricing))
	for modelName, modelPricing := range ct.pricing {
		pricing[modelName] = modelPricing
	}
	return pricing
}

// EstimateRunCost calculates the run cost for a single model usage snapshot
// without mutating any external tracker state.
func EstimateRunCost(modelName string, usage RunUsage, pricing map[string]ModelPricing) *RunCost {
	tracker := NewCostTracker(pricing)
	tracker.Record(modelName, usage)
	return tracker.buildRunCost()
}

// WithCostTracker attaches a cost tracker to the agent.
// Run costs are available on RunResult.Cost after the run.
func WithCostTracker[T any](tracker *CostTracker) AgentOption[T] {
	return func(a *Agent[T]) {
		a.costTracker = tracker
	}
}
