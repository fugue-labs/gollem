package modelutil

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

// CacheStore is the interface for a response cache backend.
type CacheStore interface {
	Get(key string) (*core.ModelResponse, bool)
	Set(key string, response *core.ModelResponse)
}

// MemoryCache is an in-memory CacheStore with optional TTL.
type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]memoryCacheEntry
	ttl     time.Duration // zero means no expiration
}

type memoryCacheEntry struct {
	response  *core.ModelResponse
	createdAt time.Time
}

// NewMemoryCache creates an in-memory cache with no expiration.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		entries: make(map[string]memoryCacheEntry),
	}
}

// NewMemoryCacheWithTTL creates an in-memory cache with TTL-based expiration.
func NewMemoryCacheWithTTL(ttl time.Duration) *MemoryCache {
	return &MemoryCache{
		entries: make(map[string]memoryCacheEntry),
		ttl:     ttl,
	}
}

func (c *MemoryCache) Get(key string) (*core.ModelResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if c.ttl > 0 && time.Since(entry.createdAt) > c.ttl {
		// Expired — remove lazily.
		delete(c.entries, key)
		return nil, false
	}
	return entry.response, true
}

func (c *MemoryCache) Set(key string, response *core.ModelResponse) {
	c.mu.Lock()
	c.entries[key] = memoryCacheEntry{
		response:  response,
		createdAt: time.Now(),
	}
	c.mu.Unlock()
}

// CachedModel wraps a Model with response caching.
// Request() checks the cache first; on miss, calls the wrapped model and stores the result.
// RequestStream() is NOT cached (streaming is inherently non-cacheable).
type CachedModel struct {
	model core.Model
	store CacheStore
}

// NewCachedModel creates a response-cached model wrapper.
func NewCachedModel(model core.Model, store CacheStore) *CachedModel {
	return &CachedModel{model: model, store: store}
}

var _ core.Model = (*CachedModel)(nil)

func (c *CachedModel) ModelName() string {
	return c.model.ModelName()
}

func (c *CachedModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	key, keyErr := cacheKey(messages, settings, params)
	if keyErr != nil {
		// Cache key computation failed; bypass cache and call model directly.
		return c.model.Request(ctx, messages, settings, params)
	}

	if resp, ok := c.store.Get(key); ok {
		return resp, nil
	}

	resp, err := c.model.Request(ctx, messages, settings, params)
	if err != nil {
		return nil, err
	}
	c.store.Set(key, resp)
	return resp, nil
}

func (c *CachedModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	// Streaming requests are not cached.
	return c.model.RequestStream(ctx, messages, settings, params)
}

// cacheKey computes a SHA-256 hash of the request parameters.
func cacheKey(messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (string, error) {
	h := sha256.New()
	enc := json.NewEncoder(h)
	if err := enc.Encode(messages); err != nil {
		return "", fmt.Errorf("cache key: encoding messages: %w", err)
	}
	if err := enc.Encode(settings); err != nil {
		return "", fmt.Errorf("cache key: encoding settings: %w", err)
	}
	if err := enc.Encode(params); err != nil {
		return "", fmt.Errorf("cache key: encoding params: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
