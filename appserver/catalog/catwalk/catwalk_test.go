package catwalk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedSnapshotParsesAndCoversCoreProviders(t *testing.T) {
	providers, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	byID := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		if !SupportedType(provider.Type) {
			t.Fatalf("snapshot contains unsupported provider type %q (%s)", provider.Type, provider.ID)
		}
		byID[provider.ID] = provider
	}
	for _, id := range []string{"anthropic", "openai", "vertexai", "deepseek", "xai"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("snapshot missing provider %q", id)
		}
	}
	anthropic := byID["anthropic"]
	if len(anthropic.Models) == 0 || anthropic.Models[0].ID != "claude-fable-5-1" {
		t.Fatalf("anthropic snapshot should lead with claude-fable-5-1, got %#v", anthropic.Models)
	}
	var sawOpus5 bool
	for _, model := range anthropic.Models {
		if model.ID == "claude-opus-5" {
			sawOpus5 = true
			if model.ContextWindow != 1_000_000 || !model.CanReason {
				t.Fatalf("claude-opus-5 metadata unexpected: %#v", model)
			}
		}
	}
	if !sawOpus5 {
		t.Fatal("anthropic snapshot missing claude-opus-5")
	}

	// Embedded returns copies; mutating one must not leak into the next.
	providers[0].Models[0].ID = "mutated"
	again, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded again: %v", err)
	}
	if again[0].Models[0].ID == "mutated" {
		t.Fatal("Embedded returned shared state")
	}
}

func TestEnvReference(t *testing.T) {
	cases := []struct {
		in   string
		name string
		ok   bool
	}{
		{"$ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY", true},
		{"$hf_token", "hf_token", true},
		{"$", "", false},
		{"", "", false},
		{"sk-literal", "", false},
		{"$BAD-NAME", "", false},
		{"$WITH SPACE", "", false},
	}
	for _, tc := range cases {
		name, ok := EnvReference(tc.in)
		if name != tc.name || ok != tc.ok {
			t.Errorf("EnvReference(%q) = (%q, %v), want (%q, %v)", tc.in, name, ok, tc.name, tc.ok)
		}
	}
}

func TestParseRejectsStructuralProblems(t *testing.T) {
	cases := map[string]string{
		"not json":            `{`,
		"missing provider id": `[{"name":"x","models":[{"id":"m"}]}]`,
		"duplicate provider":  `[{"id":"a"},{"id":"a"}]`,
		"missing model id":    `[{"id":"a","models":[{"name":"m"}]}]`,
		"duplicate model":     `[{"id":"a","models":[{"id":"m"},{"id":"m"}]}]`,
	}
	for name, data := range cases {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: Parse accepted %s", name, data)
		}
	}
	if _, err := Parse([]byte(`[]`)); err != nil {
		t.Fatalf("Parse rejected an empty list: %v", err)
	}
}

func TestFilterSupported(t *testing.T) {
	in := []Provider{
		{ID: "anthropic", Type: TypeAnthropic, APIKey: "$ANTHROPIC_API_KEY", Models: []Model{{ID: "m"}}},
		{ID: "literal-key", Type: TypeOpenAICompat, APIKey: "sk-literal", Models: []Model{{ID: "m"}}},
		{ID: "no-key", Type: TypeOpenAICompat, Models: []Model{{ID: "m"}}},
		{ID: "no-models", Type: TypeOpenAICompat, APIKey: "$K"},
		{ID: "vertexai", Type: TypeVertexAI, Models: []Model{{ID: "m"}}},
		{ID: "bedrock", Type: TypeBedrock, Models: []Model{{ID: "m"}}},
		{ID: "gemini", Type: TypeGoogle, APIKey: "$GEMINI_API_KEY", Models: []Model{{ID: "m"}}},
		{ID: "openrouter", Type: TypeOpenRouter, APIKey: "$OPENROUTER_API_KEY", Models: []Model{{ID: "m"}}},
	}
	got := FilterSupported(in)
	var ids []string
	for _, provider := range got {
		ids = append(ids, provider.ID)
	}
	want := "anthropic,vertexai,openrouter"
	if strings.Join(ids, ",") != want {
		t.Fatalf("FilterSupported kept %v, want %s", ids, want)
	}
	// The filtered list must be independent from the input.
	got[0].Models[0].ID = "changed"
	if in[0].Models[0].ID == "changed" {
		t.Fatal("FilterSupported returned aliased models")
	}
}

func TestClientFetchHonoursETagAndLimits(t *testing.T) {
	registry := []Provider{{ID: "openai", Type: TypeOpenAI, APIKey: "$OPENAI_API_KEY", Models: []Model{{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol"}}}}
	body, _ := json.Marshal(registry)
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v2/providers" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := NewClient(srv.URL+"/", srv.Client())
	if client.BaseURL() != srv.URL {
		t.Fatalf("BaseURL = %q, want %q", client.BaseURL(), srv.URL)
	}
	providers, etag, err := client.Fetch(context.Background(), "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if etag != `"v1"` || len(providers) != 1 || providers[0].ID != "openai" {
		t.Fatalf("Fetch returned etag=%q providers=%#v", etag, providers)
	}
	_, etag, err = client.Fetch(context.Background(), `"v1"`)
	if !errors.Is(err, ErrNotModified) || etag != `"v1"` {
		t.Fatalf("Fetch with current etag = (%q, %v), want ErrNotModified", etag, err)
	}
	if requests != 2 {
		t.Fatalf("server saw %d requests, want 2", requests)
	}
}

func TestClientFetchRejectsBadResponses(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"status": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"empty":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) },
		"corrupt": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[{"id":"a"},{"id":"a"}]`))
		},
	}
	for name, handler := range cases {
		srv := httptest.NewServer(handler)
		_, _, err := NewClient(srv.URL, srv.Client()).Fetch(context.Background(), "")
		srv.Close()
		if err == nil {
			t.Errorf("%s: Fetch accepted a bad response", name)
		}
	}
}

func TestResolveURL(t *testing.T) {
	lookup := func(name string) (string, bool) {
		if name == URLEnv {
			return " https://env.test/ ", true
		}
		return "", false
	}
	if got := ResolveURL(lookup, ""); got != "https://env.test" {
		t.Fatalf("env = %q", got)
	}
	if got := ResolveURL(lookup, "https://override.test/"); got != "https://override.test" {
		t.Fatalf("override = %q", got)
	}
	if got := ResolveURL(func(string) (string, bool) { return "", false }, ""); got != DefaultURL {
		t.Fatalf("default = %q", got)
	}
}

func TestClientDefaultsToEnvThenHostedURL(t *testing.T) {
	t.Setenv(URLEnv, "http://mirror.local/")
	if got := NewClient("", nil).BaseURL(); got != "http://mirror.local" {
		t.Fatalf("BaseURL with env = %q", got)
	}
	t.Setenv(URLEnv, "")
	if got := NewClient("", nil).BaseURL(); got != DefaultURL {
		t.Fatalf("BaseURL default = %q", got)
	}
}

func TestDefaultCachePathHonoursEnvOverride(t *testing.T) {
	t.Setenv(CachePathEnv, "")
	if got := DefaultCachePath(); got != "" {
		t.Fatalf("empty override should disable the cache, got %q", got)
	}
	t.Setenv(CachePathEnv, " /tmp/registry-cache.json ")
	if got := DefaultCachePath(); got != "/tmp/registry-cache.json" {
		t.Fatalf("override = %q", got)
	}
}

func TestCacheRoundTripAndRejections(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(filepath.Join(dir, "nested", "providers.json"))
	if _, err := cache.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load before Store = %v, want ErrNotExist", err)
	}
	entry := CacheEntry{
		ETag:      `"abc"`,
		Source:    "https://example.test",
		Providers: []Provider{{ID: "deepseek", Type: TypeOpenAICompat, APIKey: "$DEEPSEEK_API_KEY", Models: []Model{{ID: "deepseek-v4-pro"}}}},
	}
	if err := cache.Store(entry); err != nil {
		t.Fatalf("Store: %v", err)
	}
	info, err := os.Stat(cache.Path())
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache perms = %o, want 600", info.Mode().Perm())
	}
	loaded, err := cache.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ETag != `"abc"` || loaded.Source != entry.Source || loaded.Version != cacheVersion || loaded.FetchedAt.IsZero() {
		t.Fatalf("Load returned %#v", loaded)
	}
	if len(loaded.Providers) != 1 || loaded.Providers[0].ID != "deepseek" {
		t.Fatalf("Load providers = %#v", loaded.Providers)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "nested", ".catwalk-providers-*.tmp")); len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}

	if err := cache.Store(CacheEntry{}); err == nil {
		t.Fatal("Store accepted an empty registry")
	}
	if err := cache.Store(CacheEntry{Providers: []Provider{{ID: "a"}, {ID: "a"}}}); err == nil {
		t.Fatal("Store accepted duplicate providers")
	}

	stale, _ := json.Marshal(CacheEntry{Version: cacheVersion + 1, FetchedAt: time.Now(), Providers: entry.Providers})
	if err := os.WriteFile(cache.Path(), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(); err == nil {
		t.Fatal("Load accepted a cache with the wrong version")
	}
	if err := os.WriteFile(cache.Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(); err == nil {
		t.Fatal("Load accepted a corrupt cache")
	}

	var disabled *Cache
	if _, err := disabled.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nil cache Load = %v", err)
	}
	if err := disabled.Store(entry); err != nil {
		t.Fatalf("nil cache Store = %v", err)
	}
	if NewCache("").Path() != "" {
		t.Fatal("empty cache path should stay empty")
	}
}
