package middleware

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// Metrics holds thread-safe counters for model request metrics.
type Metrics struct {
	RequestCount  atomic.Int64
	ErrorCount    atomic.Int64
	InputTokens   atomic.Int64
	OutputTokens  atomic.Int64
	TotalDuration atomic.Int64 // nanoseconds
	ToolCalls     atomic.Int64
}

// Snapshot returns a copy of the current metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		RequestCount:  m.RequestCount.Load(),
		ErrorCount:    m.ErrorCount.Load(),
		InputTokens:   m.InputTokens.Load(),
		OutputTokens:  m.OutputTokens.Load(),
		TotalDuration: time.Duration(m.TotalDuration.Load()),
		ToolCalls:     m.ToolCalls.Load(),
	}
}

// MetricsSnapshot is a point-in-time copy of metrics.
type MetricsSnapshot struct {
	RequestCount  int64
	ErrorCount    int64
	InputTokens   int64
	OutputTokens  int64
	TotalDuration time.Duration
	ToolCalls     int64
}

// AverageLatency returns the average request latency.
func (s MetricsSnapshot) AverageLatency() time.Duration {
	if s.RequestCount == 0 {
		return 0
	}
	return s.TotalDuration / time.Duration(s.RequestCount)
}

// MetricsMiddleware tracks request metrics.
type MetricsMiddleware struct {
	metrics *Metrics
}

// NewMetrics creates a new metrics middleware with a shared Metrics instance.
func NewMetrics(m *Metrics) *MetricsMiddleware {
	return &MetricsMiddleware{metrics: m}
}

// WrapRequest implements Middleware.
func (m *MetricsMiddleware) WrapRequest(next RequestFunc) RequestFunc {
	return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		start := time.Now()
		m.metrics.RequestCount.Add(1)

		resp, err := next(ctx, messages, settings, params)
		duration := time.Since(start)
		m.metrics.TotalDuration.Add(int64(duration))

		if err != nil {
			m.metrics.ErrorCount.Add(1)
			return nil, err
		}

		m.metrics.InputTokens.Add(int64(resp.Usage.InputTokens))
		m.metrics.OutputTokens.Add(int64(resp.Usage.OutputTokens))
		m.metrics.ToolCalls.Add(int64(len(resp.ToolCalls())))

		return resp, nil
	}
}
