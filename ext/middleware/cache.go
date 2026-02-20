package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// cacheEntry holds a cached response along with its creation time.
type cacheEntry struct {
	response  *core.ModelResponse
	createdAt time.Time
}

// cacheMiddleware implements both Middleware and StreamMiddleware.
type cacheMiddleware struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
	stats   *CacheStats
}

// CacheStats tracks cache hit/miss statistics.
type CacheStats struct {
	mu     sync.Mutex
	Hits   int64
	Misses int64
}

// HitRate returns the cache hit rate (0.0 to 1.0).
// Returns 0.0 if no requests have been made.
func (s *CacheStats) HitRate() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := s.Hits + s.Misses
	if total == 0 {
		return 0.0
	}
	return float64(s.Hits) / float64(total)
}

// CacheMiddleware creates middleware that caches model responses.
// Identical requests (same messages, settings, and parameters) return
// cached responses without calling the model. Cache entries expire
// after the specified TTL. Streaming requests pass through without caching.
func CacheMiddleware(ttl time.Duration) Middleware {
	return &cacheMiddleware{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
		stats:   &CacheStats{},
	}
}

// CacheMiddlewareWithStats returns a CacheMiddleware along with a CacheStats
// object for monitoring cache performance.
func CacheMiddlewareWithStats(ttl time.Duration) (Middleware, *CacheStats) {
	stats := &CacheStats{}
	mw := &cacheMiddleware{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
		stats:   stats,
	}
	return mw, stats
}

// hashRequest computes a SHA-256 hash of the request parameters for use as
// a cache key. The hash covers messages, settings, and parameters.
func hashRequest(messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (string, error) {
	h := sha256.New()
	enc := json.NewEncoder(h)
	if err := enc.Encode(messages); err != nil {
		return "", fmt.Errorf("cache: failed to hash messages: %w", err)
	}
	if err := enc.Encode(settings); err != nil {
		return "", fmt.Errorf("cache: failed to hash settings: %w", err)
	}
	if err := enc.Encode(params); err != nil {
		return "", fmt.Errorf("cache: failed to hash params: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WrapRequest implements Middleware. It caches responses keyed by a hash of
// the request. On cache hit, the stored response is returned without calling
// the model. Expired entries are evicted lazily on access.
func (c *cacheMiddleware) WrapRequest(next RequestFunc) RequestFunc {
	return func(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
		key, err := hashRequest(messages, settings, params)
		if err != nil {
			// If hashing fails, fall through to the model.
			return next(ctx, messages, settings, params)
		}

		now := time.Now()

		c.mu.Lock()
		entry, found := c.entries[key]
		if found {
			if now.Sub(entry.createdAt) < c.ttl {
				// Cache hit — return stored response.
				c.mu.Unlock()
				c.stats.mu.Lock()
				c.stats.Hits++
				c.stats.mu.Unlock()
				return entry.response, nil
			}
			// Expired — evict the stale entry.
			delete(c.entries, key)
		}
		c.mu.Unlock()

		c.stats.mu.Lock()
		c.stats.Misses++
		c.stats.mu.Unlock()

		// Cache miss — call the model.
		resp, err := next(ctx, messages, settings, params)
		if err != nil {
			return nil, err
		}

		c.mu.Lock()
		c.entries[key] = cacheEntry{
			response:  resp,
			createdAt: now,
		}
		c.mu.Unlock()

		return resp, nil
	}
}

// WrapStreamRequest implements StreamMiddleware. Streaming requests bypass
// the cache entirely and pass through to the next handler.
func (c *cacheMiddleware) WrapStreamRequest(next StreamRequestFunc) StreamRequestFunc {
	return next
}

// Verify cacheMiddleware implements StreamMiddleware.
var _ StreamMiddleware = (*cacheMiddleware)(nil)
