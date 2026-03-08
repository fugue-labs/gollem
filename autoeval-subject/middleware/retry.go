// Package middleware contains middleware implementations for the terminal agent.
// These are optimized by the autoeval harness.
package middleware

// RetryConfig configures automatic retry behavior.
type RetryConfig struct {
	// MaxRetries is the maximum number of retries per tool call.
	MaxRetries int `yaml:"max_retries" json:"max_retries"`
	// BackoffMS is the initial backoff in milliseconds.
	BackoffMS int `yaml:"backoff_ms" json:"backoff_ms"`
}
