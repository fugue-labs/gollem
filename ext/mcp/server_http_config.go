package mcp

import (
	"fmt"
	"time"
)

const (
	defaultHTTPMaxSessions                   = 1024
	defaultHTTPMaxSessionsPerPrincipal       = 64
	defaultHTTPMaxRequestBodyBytes     int64 = 8 << 20
	// A byte limit alone does not bound encoding/json's heap use: a compact
	// array of empty objects can expand into hundreds of thousands of Go maps.
	// These limits cap that structural multiplier before nested MCP params are
	// unmarshaled into map[string]any. They are deliberately generous enough
	// for large batched tool arguments while keeping per-decode object counts
	// finite and measurable.
	defaultHTTPMaxJSONDepth                  = 64
	defaultHTTPMaxJSONStructuralTokens       = 65_536
	defaultHTTPMaxConcurrentMessages         = 256
	defaultHTTPMaxConcurrentPerSession       = 4
	defaultHTTPOutboxMaxMessages             = 256
	defaultHTTPOutboxMaxBytes          int64 = 8 << 20
	defaultHTTPRequestBodyTimeout            = 30 * time.Second
)

// HTTPServerTransportConfig contains the hard resource limits for a
// streamable HTTP MCP transport. Capacity/size limits are mandatory and
// positive. MaxJSONDepth and MaxJSONStructuralTokens bound the decoded object
// graph independently of request bytes; structural tokens include containers,
// object keys, and scalar values. RequestBodyTimeout zero normalizes to the
// safe 30-second default; it never means unlimited.
type HTTPServerTransportConfig struct {
	MaxSessions                     int
	MaxSessionsPerPrincipal         int
	IdleTimeout                     time.Duration
	AbsoluteLifetime                time.Duration
	MaxRequestBodyBytes             int64
	MaxJSONDepth                    int
	MaxJSONStructuralTokens         int
	RequestBodyTimeout              time.Duration
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
		MaxJSONDepth:                    defaultHTTPMaxJSONDepth,
		MaxJSONStructuralTokens:         defaultHTTPMaxJSONStructuralTokens,
		RequestBodyTimeout:              defaultHTTPRequestBodyTimeout,
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
	case c.MaxJSONDepth <= 0:
		return fmt.Errorf("mcp: HTTP transport MaxJSONDepth must be positive")
	case c.MaxJSONStructuralTokens <= 0:
		return fmt.Errorf("mcp: HTTP transport MaxJSONStructuralTokens must be positive")
	case c.RequestBodyTimeout < 0:
		return fmt.Errorf("mcp: HTTP transport RequestBodyTimeout cannot be negative")
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

func (c HTTPServerTransportConfig) withDefaults() HTTPServerTransportConfig {
	if c.RequestBodyTimeout == 0 {
		c.RequestBodyTimeout = defaultHTTPRequestBodyTimeout
	}
	return c
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
// Ordinary decoding, protected nested-response decoding, matched response
// delivery, and SSE use separately bounded lanes. Each global lane is capped
// by MaxConcurrentMessages; ordinary session-scoped lanes are capped by
// MaxConcurrentMessagesPerSession, the protected decode reserve is exactly one
// per session, and SSE is one per session. Thus aggregate decode work is
// explicitly bounded at 2*MaxConcurrentMessages globally and
// MaxConcurrentMessagesPerSession+1 for a session with pending nested requests.
// RejectedMessages aggregates failures from every lane.
type HTTPServerTransportStats struct {
	ActiveSessions           int
	ProvisionalSessions      int
	RejectedSessionCreations uint64
	ExpiredSessions          uint64
	InFlightMessages         int
	InFlightDecodes          int
	InFlightProtectedDecodes int
	InFlightControlResponses int
	InFlightSSEStreams       int
	RejectedMessages         uint64
	RejectedJSONComplexity   uint64
	OutboxOverflowClosures   uint64
}
