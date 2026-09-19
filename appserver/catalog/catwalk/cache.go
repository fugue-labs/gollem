package catwalk

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cacheVersion is bumped when the on-disk shape changes so stale files are
// ignored instead of misread.
const cacheVersion = 1

// Cache persists a fetched registry (plus its ETag) on disk.
type Cache struct {
	path string
}

// CacheEntry is what Cache stores.
type CacheEntry struct {
	Version   int        `json:"version"`
	ETag      string     `json:"etag,omitempty"`
	FetchedAt time.Time  `json:"fetchedAt"`
	Source    string     `json:"source,omitempty"`
	Providers []Provider `json:"providers"`
}

// NewCache returns a cache backed by path. An empty path disables caching:
// Load reports os.ErrNotExist and Store is a no-op.
func NewCache(path string) *Cache {
	return &Cache{path: path}
}

// CachePathEnv overrides DefaultCachePath. Setting it to an empty string
// disables the on-disk cache, which keeps tests and sandboxes hermetic.
const CachePathEnv = "GOLLEM_MODEL_REGISTRY_CACHE"

// DefaultCachePath returns the registry cache file: $GOLLEM_MODEL_REGISTRY_CACHE
// when set (empty disables caching), otherwise a per-user cache file, or ""
// when the user cache directory cannot be determined.
func DefaultCachePath() string {
	if value, ok := os.LookupEnv(CachePathEnv); ok {
		return strings.TrimSpace(value)
	}
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "gollem", "catwalk-providers.json")
}

// Path returns the cache file path ("" when disabled).
func (c *Cache) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// Load reads and validates the cached entry. A missing file yields
// os.ErrNotExist; a corrupt or outdated file yields a descriptive error.
func (c *Cache) Load() (CacheEntry, error) {
	if c == nil || c.path == "" {
		return CacheEntry{}, os.ErrNotExist
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return CacheEntry{}, err
	}
	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return CacheEntry{}, fmt.Errorf("catwalk: decode cache %s: %w", c.path, err)
	}
	if entry.Version != cacheVersion {
		return CacheEntry{}, fmt.Errorf("catwalk: cache %s has version %d, want %d", c.path, entry.Version, cacheVersion)
	}
	if err := Validate(entry.Providers); err != nil {
		return CacheEntry{}, fmt.Errorf("catwalk: cache %s: %w", c.path, err)
	}
	if len(entry.Providers) == 0 {
		return CacheEntry{}, errors.New("catwalk: cache holds no providers")
	}
	return entry, nil
}

// Store writes the entry atomically (temp file + rename) with 0600 perms.
func (c *Cache) Store(entry CacheEntry) error {
	if c == nil || c.path == "" {
		return nil
	}
	if err := Validate(entry.Providers); err != nil {
		return err
	}
	if len(entry.Providers) == 0 {
		return errors.New("catwalk: refusing to cache an empty registry")
	}
	entry.Version = cacheVersion
	if entry.FetchedAt.IsZero() {
		entry.FetchedAt = time.Now().UTC()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("catwalk: encode cache: %w", err)
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("catwalk: create cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".catwalk-providers-*.tmp")
	if err != nil {
		return fmt.Errorf("catwalk: create temp cache: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("catwalk: write cache: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("catwalk: chmod cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("catwalk: close cache: %w", err)
	}
	if err := os.Rename(tmpPath, c.path); err != nil { // #nosec G703 -- cache path is process configuration, not request input
		cleanup()
		return fmt.Errorf("catwalk: publish cache: %w", err)
	}
	return nil
}
