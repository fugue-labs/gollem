package mcp

import (
	"fmt"
	"time"
)

const (
	defaultHTTPMaxSessions                   = 1024
	defaultHTTPMaxSessionsPerPrincipal       = 64
	defaultHTTPMaxRequestBodyBytes     int64 = 8 << 20
	defaultHTTPMaxConcurrentMessages         = 256
	defaultHTTPMaxConcurrentPerSession       = 4
	defaultHTTPOutboxMaxMessages             = 256
	defaultHTTPOutboxMaxBytes          int64 = 8 << 20
)

// HTTPServerTransportConfig contains the hard resource limits for a
// streamable HTTP MCP transport. Every limit is mandatory and positive: the
// configured constructor deliberately has no zero-means-unlimited mode.
type HTTPServerTransportConfig struct {
	MaxSessions                     int
	MaxSessionsPerPrincipal         int
	IdleTimeout                     time.Duration
	AbsoluteLifetime                time.Duration
	MaxRequestBodyBytes             int64
	MaxConcurrentMessages           int
	MaxConcurrentMessagesPerSession int
	OutboxMaxMessages               int
	OutboxMaxBytes                  int64
	RetryAfter                      time.Duration
}

// DefaultHTTPServerTransportConfig returns production-safe bounded defaults.
func DefaultHTTPServerTransportConfig() HTTPServerTransportConfig {
	return HTTPServerTransportConfig{
		MaxSessions:                     defaultHTTPMaxSessions,
		MaxSessionsPerPrincipal:         defaultHTTPMaxSessionsPerPrincipal,
		IdleTimeout:                     30 * time.Minute,
		AbsoluteLifetime:                24 * time.Hour,
		MaxRequestBodyBytes:             defaultHTTPMaxRequestBodyBytes,
		MaxConcurrentMessages:           defaultHTTPMaxConcurrentMessages,
		MaxConcurrentMessagesPerSession: defaultHTTPMaxConcurrentPerSession,
		OutboxMaxMessages:               defaultHTTPOutboxMaxMessages,
		OutboxMaxBytes:                  defaultHTTPOutboxMaxBytes,
		RetryAfter:                      time.Second,
	}
}

func (c HTTPServerTransportConfig) validate() error {
	switch {
	case c.MaxSessions <= 0:
		return fmt.Errorf("mcp: HTTP transport MaxSessions must be positive")
	case c.MaxSessionsPerPrincipal <= 0:
		return fmt.Errorf("mcp: HTTP transport MaxSessionsPerPrincipal must be positive")
	case c.MaxSessionsPerPrincipal > c.MaxSessions:
		return fmt.Errorf("mcp: HTTP transport MaxSessionsPerPrincipal cannot exceed MaxSessions")
	case c.IdleTimeout <= 0:
		return fmt.Errorf("mcp: HTTP transport IdleTimeout must be positive")
	case c.AbsoluteLifetime <= 0:
		return fmt.Errorf("mcp: HTTP transport AbsoluteLifetime must be positive")
	case c.IdleTimeout > c.AbsoluteLifetime:
		return fmt.Errorf("mcp: HTTP transport IdleTimeout cannot exceed AbsoluteLifetime")
	case c.MaxRequestBodyBytes <= 0:
		return fmt.Errorf("mcp: HTTP transport MaxRequestBodyBytes must be positive")
	case c.MaxConcurrentMessages <= 0:
		return fmt.Errorf("mcp: HTTP transport MaxConcurrentMessages must be positive")
	case c.MaxConcurrentMessagesPerSession <= 0:
		return fmt.Errorf("mcp: HTTP transport MaxConcurrentMessagesPerSession must be positive")
	case c.MaxConcurrentMessagesPerSession > c.MaxConcurrentMessages:
		return fmt.Errorf("mcp: HTTP transport MaxConcurrentMessagesPerSession cannot exceed MaxConcurrentMessages")
	case c.OutboxMaxMessages <= 0:
		return fmt.Errorf("mcp: HTTP transport OutboxMaxMessages must be positive")
	case c.OutboxMaxBytes <= 0:
		return fmt.Errorf("mcp: HTTP transport OutboxMaxBytes must be positive")
	case c.RetryAfter <= 0:
		return fmt.Errorf("mcp: HTTP transport RetryAfter must be positive")
	default:
		return nil
	}
}

// sweepInterval is intentionally derived rather than independently tunable.
// It stays responsive for short test/development lifetimes without allowing a
// production configuration to create a hot ticker or defer cleanup for long.
func (c HTTPServerTransportConfig) sweepInterval() time.Duration {
	interval := c.IdleTimeout / 2
	if interval < 10*time.Millisecond {
		return 10 * time.Millisecond
	}
	if interval > time.Minute {
		return time.Minute
	}
	return interval
}

// HTTPServerTransportStats is a source-free operational snapshot. It contains
// counts only—never session IDs, principals, request data, or queued payloads.
// InFlightMessages counts ordinary asynchronous request/notification handlers.
// POST decoding and matched nested-response delivery use separate internal
// lanes, each bounded by MaxConcurrentMessages globally and
// MaxConcurrentMessagesPerSession where session-scoped. RejectedMessages
// aggregates admission failures from all three lanes.
type HTTPServerTransportStats struct {
	ActiveSessions           int
	ProvisionalSessions      int
	RejectedSessionCreations uint64
	ExpiredSessions          uint64
	InFlightMessages         int
	RejectedMessages         uint64
	OutboxOverflowClosures   uint64
}
