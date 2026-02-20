package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// CacheStore is the interface for a response cache backend.
type CacheStore interface {
	Get(key string) (*ModelResponse, bool)
	Set(key string, response *ModelResponse)
}

// MemoryCache is an in-memory CacheStore with optional TTL.
type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]memoryCacheEntry
	ttl     time.Duration // zero means no expiration
}

type memoryCacheEntry struct {
	response  *ModelResponse
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

func (c *MemoryCache) Get(key string) (*ModelResponse, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if c.ttl > 0 && time.Since(entry.createdAt) > c.ttl {
		// Expired — remove lazily.
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil, false
	}
	return entry.response, true
}

func (c *MemoryCache) Set(key string, response *ModelResponse) {
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
	model Model
	store CacheStore
}

// NewCachedModel creates a response-cached model wrapper.
func NewCachedModel(model Model, store CacheStore) *CachedModel {
	return &CachedModel{model: model, store: store}
}

var _ Model = (*CachedModel)(nil)

func (c *CachedModel) ModelName() string {
	return c.model.ModelName()
}

func (c *CachedModel) Request(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (*ModelResponse, error) {
	key := cacheKey(messages, settings, params)

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

func (c *CachedModel) RequestStream(ctx context.Context, messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) (StreamedResponse, error) {
	// Streaming requests are not cached.
	return c.model.RequestStream(ctx, messages, settings, params)
}

// cacheKey computes a SHA-256 hash of the request parameters.
func cacheKey(messages []ModelMessage, settings *ModelSettings, params *ModelRequestParameters) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(messages)
	_ = enc.Encode(settings)
	_ = enc.Encode(params)
	return hex.EncodeToString(h.Sum(nil))
}
