package core

import "fmt"

// Usage tracks token counts for a single model request.
type Usage struct {
	InputTokens      int            `json:"input_tokens,omitempty"`
	OutputTokens     int            `json:"output_tokens,omitempty"`
	CacheWriteTokens int            `json:"cache_write_tokens,omitempty"`
	CacheReadTokens  int            `json:"cache_read_tokens,omitempty"`
	Details          map[string]int `json:"details,omitempty"`
}

// TotalTokens returns the sum of input and output tokens.
func (u Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}

// Incr adds another Usage to this one in place.
func (u *Usage) Incr(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheWriteTokens += other.CacheWriteTokens
	u.CacheReadTokens += other.CacheReadTokens
	if len(other.Details) > 0 {
		if u.Details == nil {
			u.Details = make(map[string]int)
		}
		for k, v := range other.Details {
			u.Details[k] += v
		}
	}
}

// RunUsage tracks aggregate token counts across an entire agent run.
type RunUsage struct {
	Usage
	Requests  int `json:"requests"`
	ToolCalls int `json:"tool_calls"`
}

// IncrRequest adds a single request's usage and increments the request count.
func (u *RunUsage) IncrRequest(other Usage) {
	u.Incr(other)
	u.Requests++
}

// IncrToolCall increments the tool call count.
func (u *RunUsage) IncrToolCall() {
	u.ToolCalls++
}

// IncrRun adds another RunUsage to this one.
func (u *RunUsage) IncrRun(other RunUsage) {
	u.Incr(other.Usage)
	u.Requests += other.Requests
	u.ToolCalls += other.ToolCalls
}

// UsageLimits defines constraints on model usage. Nil fields mean no limit.
type UsageLimits struct {
	RequestLimit      *int
	InputTokensLimit  *int
	OutputTokensLimit *int
	TotalTokensLimit  *int
	ToolCallsLimit    *int // Maximum total tool calls per run.
}

// DefaultUsageLimits returns limits with RequestLimit=50.
func DefaultUsageLimits() UsageLimits {
	n := 50
	return UsageLimits{RequestLimit: &n}
}

// CheckBeforeRequest checks if the next request would exceed the request limit.
func (l UsageLimits) CheckBeforeRequest(usage RunUsage) error {
	if l.RequestLimit != nil && usage.Requests >= *l.RequestLimit {
		return &UsageLimitExceeded{
			Message: fmt.Sprintf("request limit of %d exceeded (used %d)", *l.RequestLimit, usage.Requests),
		}
	}
	return nil
}

// CheckTokens checks if token usage exceeds any configured token limits.
func (l UsageLimits) CheckTokens(usage RunUsage) error {
	if l.InputTokensLimit != nil && usage.InputTokens > *l.InputTokensLimit {
		return &UsageLimitExceeded{
			Message: fmt.Sprintf("input token limit of %d exceeded (used %d)", *l.InputTokensLimit, usage.InputTokens),
		}
	}
	if l.OutputTokensLimit != nil && usage.OutputTokens > *l.OutputTokensLimit {
		return &UsageLimitExceeded{
			Message: fmt.Sprintf("output token limit of %d exceeded (used %d)", *l.OutputTokensLimit, usage.OutputTokens),
		}
	}
	if l.TotalTokensLimit != nil && usage.TotalTokens() > *l.TotalTokensLimit {
		return &UsageLimitExceeded{
			Message: fmt.Sprintf("total token limit of %d exceeded (used %d)", *l.TotalTokensLimit, usage.TotalTokens()),
		}
	}
	return nil
}

// CheckToolCalls checks if tool call usage exceeds the configured limit.
func (l UsageLimits) CheckToolCalls(usage RunUsage) error {
	if l.ToolCallsLimit != nil && usage.ToolCalls > *l.ToolCallsLimit {
		return &UsageLimitExceeded{
			Message: fmt.Sprintf("tool call limit of %d exceeded (used %d)", *l.ToolCallsLimit, usage.ToolCalls),
		}
	}
	return nil
}

// HasTokenLimits returns true if any token limit is configured.
func (l UsageLimits) HasTokenLimits() bool {
	return l.InputTokensLimit != nil || l.OutputTokensLimit != nil || l.TotalTokensLimit != nil
}

// IntPtr returns a pointer to the given int. Convenience for setting limit fields.
func IntPtr(n int) *int {
	return &n
}
