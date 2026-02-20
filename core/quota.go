package core

import "fmt"

// UsageQuota defines hard limits that terminate a run when exceeded.
type UsageQuota struct {
	MaxRequests     int // max model requests (0 = unlimited)
	MaxTotalTokens  int // max total tokens across all requests (0 = unlimited)
	MaxInputTokens  int // max input/prompt tokens (0 = unlimited)
	MaxOutputTokens int // max output/completion tokens (0 = unlimited)
}

// QuotaExceededError is returned when a usage quota is breached.
type QuotaExceededError struct {
	Quota   UsageQuota
	Usage   RunUsage
	Message string
}

func (e *QuotaExceededError) Error() string {
	return "usage quota exceeded: " + e.Message
}

// WithUsageQuota sets hard usage limits for the agent run.
func WithUsageQuota[T any](quota UsageQuota) AgentOption[T] {
	return func(a *Agent[T]) {
		a.usageQuota = &quota
	}
}

// checkQuota returns an error if usage exceeds the quota.
func checkQuota(quota *UsageQuota, usage RunUsage) error {
	if quota == nil {
		return nil
	}
	if quota.MaxRequests > 0 && usage.Requests >= quota.MaxRequests {
		return &QuotaExceededError{
			Quota:   *quota,
			Usage:   usage,
			Message: fmt.Sprintf("request limit %d reached (used %d)", quota.MaxRequests, usage.Requests),
		}
	}
	if quota.MaxTotalTokens > 0 && usage.TotalTokens() >= quota.MaxTotalTokens {
		return &QuotaExceededError{
			Quota:   *quota,
			Usage:   usage,
			Message: fmt.Sprintf("total token limit %d reached (used %d)", quota.MaxTotalTokens, usage.TotalTokens()),
		}
	}
	if quota.MaxInputTokens > 0 && usage.InputTokens >= quota.MaxInputTokens {
		return &QuotaExceededError{
			Quota:   *quota,
			Usage:   usage,
			Message: fmt.Sprintf("input token limit %d reached (used %d)", quota.MaxInputTokens, usage.InputTokens),
		}
	}
	if quota.MaxOutputTokens > 0 && usage.OutputTokens >= quota.MaxOutputTokens {
		return &QuotaExceededError{
			Quota:   *quota,
			Usage:   usage,
			Message: fmt.Sprintf("output token limit %d reached (used %d)", quota.MaxOutputTokens, usage.OutputTokens),
		}
	}
	return nil
}
