package catalog

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/appserver/catalog/catwalk"
	"github.com/fugue-labs/gollem/appserver/protocol"
	openaiprovider "github.com/fugue-labs/gollem/provider/openai"
)

const (
	ProviderOpenAI                = "openai"
	ProviderOpenAICompatibleLocal = "openai-compatible-local"
	ProviderAnthropic             = "anthropic"
	ProviderVertexAI              = "vertexai"
	ProviderVertexAIAnthropic     = "vertexai-anthropic"
)

var ErrProviderNotFound = errors.New("provider not found")

const localEndpointProbeTimeout = 5 * time.Second

type EnvLookup func(string) (string, bool)

type Option func(*Catalog)

// WithEnvLookup overrides how configuration is discovered (tests, sandboxes).
func WithEnvLookup(lookup EnvLookup) Option {
	return func(c *Catalog) {
		if lookup != nil {
			c.env = lookup
		}
	}
}

// WithHTTPClient sets the client used for local endpoint probes and registry
// refreshes.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Catalog) {
		if client != nil {
			c.httpClient = client
			c.httpClientSet = true
		}
	}
}

// WithRegistry replaces the embedded provider registry. It disables the disk
// cache (neither read nor written) and becomes the trusted snapshot Refresh
// pins remote data against, so embedders keep control of every endpoint and
// credential reference they supplied.
func WithRegistry(providers []catwalk.Provider) Option {
	return func(c *Catalog) {
		c.registryOverride = catwalk.FilterSupported(providers)
		c.registryPinned = true
	}
}

// WithRegistryCache stores refreshed registry data at path and prefers it
// over the embedded snapshot on construction. An empty path disables the
// cache.
func WithRegistryCache(path string) Option {
	return func(c *Catalog) {
		c.cache = catwalk.NewCache(path)
	}
}

// WithRegistryURL selects the registry base URL used by Refresh. An empty
// value keeps the default ($CATWALK_URL, then the hosted registry).
func WithRegistryURL(url string) Option {
	return func(c *Catalog) {
		c.registryURL = url
	}
}

// Catalog exposes providers, models, and runtime endpoints derived from the
// catwalk registry plus local configuration. It is safe for concurrent use;
// Refresh swaps the derived data atomically.
type Catalog struct {
	env           EnvLookup
	httpClient    *http.Client
	httpClientSet bool
	cache         *catwalk.Cache
	registryURL   string

	registryPinned   bool
	registryOverride []catwalk.Provider
	// trusted is the snapshot remote data is pinned against: the embedded
	// registry, or the WithRegistry override.
	trusted []catwalk.Provider

	// refreshMu serializes Refresh so overlapping fetches apply in order.
	refreshMu sync.Mutex

	mu             sync.RWMutex
	registry       []catwalk.Provider // merged registry the catalog is built from
	registrySource string
	etag           string
	providers      []Provider
	endpoints      map[string]RuntimeEndpoint
}

// NewDefault builds a catalog from the embedded registry snapshot, replaced by
// a valid on-disk cache when one is configured.
func NewDefault(opts ...Option) *Catalog {
	c := &Catalog{
		env:        os.LookupEnv,
		httpClient: &http.Client{Timeout: localEndpointProbeTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	registry := c.registryOverride
	c.registryOverride = nil
	c.registrySource = "registry"
	if c.registryPinned {
		c.cache = nil
	} else {
		registry, c.registrySource = nil, "embedded"
		if embedded, err := catwalk.Embedded(); err == nil {
			// The snapshot goes through the same gate as remote data so a
			// hand-edited or outdated providers.json cannot bypass it.
			if supported := catwalk.FilterSupported(embedded); validateRegistry(supported) == nil {
				registry = supported
			}
		}
	}
	c.trusted = catwalk.Clone(registry)
	if !c.registryPinned {
		if entry, err := c.cache.Load(); err == nil {
			cached := pinTrustedRegistryData(catwalk.FilterSupported(entry.Providers), c.trusted)
			// A cache only overlays the embedded snapshot when it is both
			// usable and newer than the snapshot compiled into this build.
			// It is merged, never substituted: providers or models the
			// snapshot knows (including ones a newer build added) survive.
			if validateRegistry(cached) == nil && cacheIsNewerThanEmbedded(entry) {
				registry = mergeRegistry(registry, cached)
				c.registrySource = "cache"
				// An ETag only means something to the registry that issued it.
				if entry.Source == c.resolvedRegistryURL() {
					c.etag = entry.ETag
				}
			}
		}
	}
	c.registry = registry
	c.providers, c.endpoints = c.buildProviders(registry)
	return c
}

func cacheIsNewerThanEmbedded(entry catwalk.CacheEntry) bool {
	info, err := catwalk.EmbeddedSnapshot()
	if err != nil {
		return true
	}
	return entry.FetchedAt.After(info.GeneratedAt)
}

// EnvLookup returns the configuration lookup the catalog judges providers
// with, so runtimes read credentials from the same source.
func (c *Catalog) EnvLookup() EnvLookup {
	c = ensureCatalog(c)
	return c.env
}

// DefaultModelName returns the bare model name the catalog advertises as the
// provider's default, so runtimes run the model the catalog validated.
func (c *Catalog) DefaultModelName(providerID string) (string, bool) {
	c = ensureCatalog(c)
	providerID = normalizeProviderID(providerID)
	for _, provider := range c.snapshot() {
		if provider.ID != providerID || provider.DefaultModelID == "" {
			continue
		}
		return strings.TrimPrefix(provider.DefaultModelID, provider.ID+"/"), true
	}
	return "", false
}

// resolvedRegistryURL is the registry base URL Refresh will use, resolved
// through the catalog's own configuration lookup.
func (c *Catalog) resolvedRegistryURL() string {
	return catwalk.ResolveURL(c.env, c.registryURL)
}

// refreshClient returns the HTTP client for registry downloads: an explicitly
// configured client, otherwise the catwalk default (30s timeout). The catalog's
// own short-timeout client is reserved for local endpoint probes.
func (c *Catalog) refreshClient() *http.Client {
	if c.httpClientSet {
		return c.httpClient
	}
	return nil
}

// snapshot returns the current provider slice. The slice is never mutated in
// place after publication, so callers may read it without holding the lock.
func (c *Catalog) snapshot() []Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.providers
}

// RegistrySource reports where the current registry came from: "embedded",
// "cache", "registry" (pinned via WithRegistry), or "remote" after a
// successful Refresh.
func (c *Catalog) RegistrySource() string {
	c = ensureCatalog(c)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registrySource
}

// Refresh fetches the registry and, when it changed, rebuilds the catalog and
// updates the cache. It returns true when new data was applied. A registry
// that cannot be reached leaves the catalog untouched.
func (c *Catalog) Refresh(ctx context.Context) (bool, error) {
	c = ensureCatalog(c)
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.RLock()
	etag := c.etag
	c.mu.RUnlock()

	client := catwalk.NewClient(c.resolvedRegistryURL(), c.refreshClient())
	fetched, newETag, err := client.Fetch(ctx, etag)
	if errors.Is(err, catwalk.ErrNotModified) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	supported := pinTrustedRegistryData(catwalk.FilterSupported(fetched), c.trusted)
	if err := validateRegistry(supported); err != nil {
		return false, err
	}
	// Never drop what a running thread may already be bound to: a provider
	// or model the registry stops listing keeps its last known entry. The
	// merge happens on registry data so the in-memory catalog and the cache
	// (and therefore the next process) agree.
	c.mu.RLock()
	merged := mergeRegistry(c.registry, supported)
	c.mu.RUnlock()
	providers, endpoints := c.buildProviders(merged)
	c.mu.Lock()
	c.registry = merged
	c.etag = newETag
	c.registrySource = "remote"
	c.providers = providers
	c.endpoints = endpoints
	c.mu.Unlock()

	if err := c.cache.Store(catwalk.CacheEntry{
		ETag:      newETag,
		FetchedAt: time.Now().UTC(),
		Source:    client.BaseURL(),
		Providers: merged,
	}); err != nil {
		return true, fmt.Errorf("catalog: registry applied but not cached: %w", err)
	}
	return true, nil
}

// mergeRegistry overlays incoming registry data on base. Incoming entries win
// for providers and models they list (fresher metadata); base providers and
// models missing from incoming are kept, appended after the incoming ones.
// Neither input is modified.
func mergeRegistry(base, incoming []catwalk.Provider) []catwalk.Provider {
	merged := catwalk.Clone(incoming)
	for _, previous := range base {
		index := slices.IndexFunc(merged, func(p catwalk.Provider) bool { return p.ID == previous.ID })
		if index < 0 {
			merged = append(merged, catwalk.Clone([]catwalk.Provider{previous})...)
			continue
		}
		for _, model := range previous.Models {
			if !slices.ContainsFunc(merged[index].Models, func(m catwalk.Model) bool { return m.ID == model.ID }) {
				model.ReasoningLevels = slices.Clone(model.ReasoningLevels)
				merged[index].Models = append(merged[index].Models, model)
			}
		}
	}
	return merged
}

// pinTrustedRegistryData constrains data that arrived over the network or from
// the on-disk cache. Only providers the embedded snapshot already knows are
// accepted, and for those the endpoint, credential reference, attribution
// headers, and default model come from the snapshot: remote data refreshes
// model metadata (the part that goes stale) but can never introduce a new
// place to send a credential or silently change which model runs. New
// providers arrive with a Gollem release that regenerates the snapshot.
//
// Any id-derived rule for new providers (for example "$<ID>_API_KEY") is
// unsafe because the id itself is attacker-chosen: an entry named "github"
// could claim $GITHUB_TOKEN. So there is no such rule.
func pinTrustedRegistryData(incoming, trusted []catwalk.Provider) []catwalk.Provider {
	known := make(map[string]catwalk.Provider, len(trusted))
	for _, provider := range trusted {
		known[provider.ID] = provider
	}
	out := make([]catwalk.Provider, 0, len(incoming))
	for _, provider := range incoming {
		pinned, ok := known[provider.ID]
		if !ok || pinned.Type != provider.Type {
			continue
		}
		provider.APIEndpoint = pinned.APIEndpoint
		provider.APIKey = pinned.APIKey
		provider.DefaultHeaders = pinned.DefaultHeaders
		if pinned.DefaultLargeModelID != "" {
			provider.DefaultLargeModelID = pinned.DefaultLargeModelID
		}
		out = append(out, provider)
	}
	return out
}

// coreRegistryProviders must be present for a fetched registry to replace the
// current one; a payload without them is treated as truncated or foreign.
var coreRegistryProviders = []string{ProviderAnthropic, ProviderOpenAI}

func validateRegistry(providers []catwalk.Provider) error {
	if len(providers) == 0 {
		return errors.New("catalog: registry contains no supported providers")
	}
	for _, id := range coreRegistryProviders {
		if !slices.ContainsFunc(providers, func(p catwalk.Provider) bool { return p.ID == id }) {
			return fmt.Errorf("catalog: registry is missing core provider %q", id)
		}
	}
	return nil
}

// RuntimeEndpoint returns the endpoint configuration for a registry-backed
// provider that Gollem drives through a generic Anthropic or OpenAI
// transport. Built-in providers (anthropic, openai, xai, vertexai, local)
// have dedicated constructors and report false here.
func (c *Catalog) RuntimeEndpoint(providerID string) (RuntimeEndpoint, bool) {
	c = ensureCatalog(c)
	providerID = normalizeProviderID(providerID)
	c.mu.RLock()
	defer c.mu.RUnlock()
	endpoint, ok := c.endpoints[providerID]
	if !ok {
		return RuntimeEndpoint{}, false
	}
	endpoint.Headers = maps.Clone(endpoint.Headers)
	return endpoint, true
}

type protocolPricing = protocol.ModelCatalogPricing

type Provider = protocol.ProviderCatalogEntry
type ProviderCapabilities = protocol.ProviderCatalogCapabilities
type ModelCapabilities = protocol.ModelCatalogCapabilities
type ReasoningEffortOption = protocol.ModelCatalogReasoningEffortOption
type ModelServiceTier = protocol.ModelCatalogServiceTier
type ModelUpgradeInfo = protocol.ModelCatalogUpgradeInfo
type ModelAvailabilityNux = protocol.ModelCatalogAvailabilityNux
type Model = protocol.ModelCatalogEntry
type ModelListParams = protocol.ModelCatalogListParams
type ModelListResponse = protocol.ModelCatalogListResponse
type ProviderListParams = protocol.ProviderListParams
type ProviderListResponse = protocol.ProviderListResponse
type ProviderHealthProbeParams = protocol.ProviderHealthProbeParams
type ProviderHealthProbeResponse = protocol.ProviderHealthProbeResponse
type ProviderHealthProbeStatus = protocol.ProviderHealthProbeStatus

const (
	ProviderHealthAvailable     = protocol.ProviderHealthAvailable
	ProviderHealthUnavailable   = protocol.ProviderHealthUnavailable
	ProviderHealthNotConfigured = protocol.ProviderHealthNotConfigured
	ProviderHealthUnsupported   = protocol.ProviderHealthUnsupported
)

type CapabilitiesReadParams struct {
	ProviderID    string `json:"providerId,omitempty"`
	Provider      string `json:"provider,omitempty"`
	ModelProvider string `json:"modelProvider,omitempty"`
}

type ToolListParams struct {
	IncludeUnavailable bool   `json:"includeUnavailable,omitempty"`
	Category           string `json:"category,omitempty"`
	Source             string `json:"source,omitempty"`
}

type ToolServices struct {
	Filesystem   bool
	Process      bool
	Git          bool
	Cache        bool
	Config       bool
	MCP          bool
	Skills       bool
	Runtime      bool
	Interactions bool
	Memory       bool
	FileRecovery bool
}

type Tool struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	DisplayName        string   `json:"displayName"`
	Description        string   `json:"description"`
	Source             string   `json:"source"`
	Category           string   `json:"category"`
	Kind               string   `json:"kind"`
	Methods            []string `json:"methods"`
	Enabled            bool     `json:"enabled"`
	Available          bool     `json:"available"`
	Mutation           bool     `json:"mutation"`
	RequiresApproval   bool     `json:"requiresApproval"`
	ServiceConfigured  bool     `json:"serviceConfigured"`
	UnavailableReason  string   `json:"unavailableReason,omitempty"`
	CodexCompatible    bool     `json:"codexCompatible"`
	GollemExtension    bool     `json:"gollemExtension"`
	OutputNotification string   `json:"outputNotification,omitempty"`
}

type ToolListResponse struct {
	Data  []Tool `json:"data"`
	Tools []Tool `json:"tools"`
}

func (c *Catalog) ListProviders(params ProviderListParams) ProviderListResponse {
	c = ensureCatalog(c)
	includeHidden := boolValue(params.IncludeHidden)
	current := c.snapshot()
	providers := make([]Provider, 0, len(current))
	for _, provider := range current {
		if provider.Hidden && !includeHidden {
			continue
		}
		if params.ConfiguredOnly && !provider.Configured {
			continue
		}
		providers = append(providers, cloneProvider(provider))
	}
	return ProviderListResponse{
		Data:      providers,
		Providers: cloneProviders(providers),
	}
}

func (c *Catalog) ListModels(params ModelListParams) (ModelListResponse, error) {
	c = ensureCatalog(c)
	includeHidden := boolValue(params.IncludeHidden)
	providerFilter := providerFilter(params.ProviderID, params.ProviderIDs)

	current := c.snapshot()
	var models []Model
	for _, provider := range current {
		if len(providerFilter) > 0 {
			if _, ok := providerFilter[provider.ID]; !ok {
				continue
			}
		}
		if provider.Hidden && !includeHidden {
			continue
		}
		for _, model := range provider.Models {
			if model.Hidden && !includeHidden {
				continue
			}
			models = append(models, cloneModel(model))
		}
	}
	markDefaultModel(models, current)

	total := len(models)
	if total == 0 {
		return ModelListResponse{Data: []Model{}}, nil
	}
	limit := total
	if params.Limit != nil && *params.Limit > 0 {
		limit = min(int(*params.Limit), total)
	}
	start := 0
	if params.Cursor != nil && strings.TrimSpace(*params.Cursor) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(*params.Cursor))
		if err != nil || n < 0 {
			return ModelListResponse{}, fmt.Errorf("invalid cursor %q", *params.Cursor)
		}
		if n > total {
			return ModelListResponse{}, fmt.Errorf("cursor %d exceeds total models %d", n, total)
		}
		start = n
	}
	end := min(start+limit, total)
	var next *string
	if end < total {
		cursor := strconv.Itoa(end)
		next = &cursor
	}
	return ModelListResponse{Data: models[start:end], NextCursor: next}, nil
}

func (c *Catalog) ProviderCapabilities(providerID string) (ProviderCapabilities, error) {
	c = ensureCatalog(c)
	providerID = normalizeProviderID(providerID)
	current := c.snapshot()
	if providerID != "" {
		for _, provider := range current {
			if provider.ID == providerID {
				caps := cloneCapabilities(provider.Capabilities)
				caps.ProviderID = provider.ID
				caps.Configured = provider.Configured
				caps.RequiresConfigurationEnv = append([]string(nil), provider.RequiredEnvVars...)
				return caps, nil
			}
		}
		return ProviderCapabilities{}, fmt.Errorf("%w: %s", ErrProviderNotFound, providerID)
	}
	var aggregate ProviderCapabilities
	for _, provider := range current {
		aggregate.NamespaceTools = aggregate.NamespaceTools || provider.Capabilities.NamespaceTools
		aggregate.ImageGeneration = aggregate.ImageGeneration || provider.Capabilities.ImageGeneration
		aggregate.WebSearch = aggregate.WebSearch || provider.Capabilities.WebSearch
		aggregate.ToolCalls = aggregate.ToolCalls || provider.Capabilities.ToolCalls
		aggregate.StructuredOutput = aggregate.StructuredOutput || provider.Capabilities.StructuredOutput
		aggregate.Vision = aggregate.Vision || provider.Capabilities.Vision
		aggregate.Streaming = aggregate.Streaming || provider.Capabilities.Streaming
		aggregate.PromptCache = aggregate.PromptCache || provider.Capabilities.PromptCache
		aggregate.Sampling = aggregate.Sampling || provider.Capabilities.Sampling
		aggregate.StopSequences = aggregate.StopSequences || provider.Capabilities.StopSequences
		aggregate.ToolSearch = aggregate.ToolSearch || provider.Capabilities.ToolSearch
		aggregate.Reasoning = aggregate.Reasoning || provider.Capabilities.Reasoning
		aggregate.ReasoningSummaries = aggregate.ReasoningSummaries || provider.Capabilities.ReasoningSummaries
		aggregate.AdaptiveThinking = aggregate.AdaptiveThinking || provider.Capabilities.AdaptiveThinking
		aggregate.ManualThinking = aggregate.ManualThinking || provider.Capabilities.ManualThinking
		aggregate.Configured = aggregate.Configured || provider.Configured
		aggregate.ReasoningEfforts = appendUnique(aggregate.ReasoningEfforts, provider.Capabilities.ReasoningEfforts...)
	}
	return aggregate, nil
}

// ValidateAgentRuntimeSelection proves that a catalog selection can support
// Gollem's streaming, tool-capable agent runtime. It deliberately returns only
// catalog identities and capability names, never configuration values.
func (c *Catalog) ValidateAgentRuntimeSelection(providerID, modelName string) error {
	c = ensureCatalog(c)
	providerID = normalizeProviderID(providerID)
	modelName = strings.TrimSpace(modelName)
	if providerID == "" {
		return errors.New("provider selection is unavailable")
	}

	current := c.snapshot()
	var selectedProvider *Provider
	for index := range current {
		provider := &current[index]
		if provider.ID == providerID {
			selectedProvider = provider
			break
		}
	}
	if selectedProvider == nil {
		return fmt.Errorf("provider selection is unavailable for %q", providerID)
	}
	if !selectedProvider.Configured {
		return fmt.Errorf("provider %q is not configured", selectedProvider.ID)
	}
	if !selectedProvider.Capabilities.Streaming || !selectedProvider.Capabilities.ToolCalls {
		return fmt.Errorf("provider %q does not advertise streaming agent tool use", selectedProvider.ID)
	}

	for index := range selectedProvider.Models {
		model := &selectedProvider.Models[index]
		if modelName == "" {
			// The runtime runs the provider's DefaultModelID (see
			// DefaultModelName); validate exactly that model.
			if model.ID != selectedProvider.DefaultModelID {
				continue
			}
		} else if model.ID != modelName && model.Model != modelName {
			continue
		}
		if !model.Capabilities.Streaming || !model.Capabilities.ToolCalls {
			return fmt.Errorf("model %q does not advertise streaming agent tool use", model.ID)
		}
		return nil
	}
	if modelName == "" {
		return fmt.Errorf("provider %q has no available default model", selectedProvider.ID)
	}
	return fmt.Errorf("model selection is unavailable for %q", modelName)
}

// ProbeProvider reports bounded health for the selected provider. The only
// network operation permitted here is a discovery request to an explicitly
// configured loopback OpenAI-compatible endpoint.
func (c *Catalog) ProbeProvider(ctx context.Context, params ProviderHealthProbeParams) (ProviderHealthProbeResponse, error) {
	c = ensureCatalog(c)
	providerID := normalizeProviderID(params.ProviderID)
	if !c.hasProvider(providerID) {
		return ProviderHealthProbeResponse{}, fmt.Errorf("%w: %s", ErrProviderNotFound, providerID)
	}
	response := ProviderHealthProbeResponse{ProviderID: providerID}
	if providerID != ProviderOpenAICompatibleLocal {
		response.Status = protocol.ProviderHealthUnsupported
		return response, nil
	}

	config, configured := c.localEndpointConfig()
	if !configured {
		response.Status = protocol.ProviderHealthNotConfigured
		return response, nil
	}
	response.Status = protocol.ProviderHealthAvailable
	if openaiprovider.ProbeLocalEndpoint(ctx, config, c.httpClient) != nil {
		response.Status = protocol.ProviderHealthUnavailable
	}
	return response, nil
}

func ListTools(params ToolListParams, services ToolServices) ToolListResponse {
	tools := builtinTools(services)
	filtered := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if !params.IncludeUnavailable && !tool.Available {
			continue
		}
		if params.Category != "" && tool.Category != params.Category {
			continue
		}
		if params.Source != "" && tool.Source != params.Source {
			continue
		}
		filtered = append(filtered, cloneTool(tool))
	}
	return ToolListResponse{
		Data:  filtered,
		Tools: cloneTools(filtered),
	}
}

func (c *Catalog) localEndpointConfig() (openaiprovider.LocalEndpointConfig, bool) {
	config, err := openaiprovider.LocalEndpointConfigFromLookup(c.env)
	// Merely having a safe default is not evidence that a local server exists.
	// Require an explicit local-profile setting before offering it as runnable.
	configured := err == nil && envConfigured(c.env,
		openaiprovider.LocalEndpointBaseURLEnv,
		openaiprovider.LocalEndpointModelEnv,
		openaiprovider.LocalEndpointAPIKeyEnv,
	)
	return config, configured
}

func (c *Catalog) hasProvider(providerID string) bool {
	for _, provider := range c.snapshot() {
		if provider.ID == providerID {
			return true
		}
	}
	return false
}

func builtinTools(services ToolServices) []Tool {
	return []Tool{
		tool("fs", "Filesystem", "Read, write, copy, remove, inspect, and watch files under the configured workspace root.", "workspace", []string{"fs/readFile", "fs/writeFile", "fs/createDirectory", "fs/readDirectory", "fs/getMetadata", "fs/remove", "fs/copy", "fs/watch", "fs/unwatch"}, services.Filesystem, true, true, "fs/changed", true, false),
		tool("process", "Process", "Spawn, stream, inspect, write to, resize, terminate, kill, and clean workspace-scoped processes.", "runtime", []string{"command/exec", "command/exec/write", "command/exec/resize", "command/exec/terminate", "process/spawn", "process/writeStdin", "process/resizePty", "process/kill", "thread/shellCommand", "thread/backgroundTerminals/list", "thread/backgroundTerminals/read", "thread/backgroundTerminals/terminate", "thread/backgroundTerminals/write", "thread/backgroundTerminals/resize", "thread/backgroundTerminals/clean"}, services.Process, true, true, "process/outputDelta", true, false),
		tool("git", "Git", "Read repository status and diffs, commit changes, and manage scoped worktrees.", "source-control", []string{"git/status", "git/diff", "git/commit", "git/worktree/list", "git/worktree/create"}, services.Git, true, true, "", false, true),
		tool("thread-store", "Thread store", "List, search, read, unsubscribe, compact, rollback, archive, unarchive, delete, fork, inject history, rename, and configure durable Gollem app-server threads.", "conversation", []string{"thread/list", "thread/search", "thread/loaded/list", "thread/unsubscribe", "thread/read", "thread/fork", "thread/compact/start", "thread/rollback", "thread/archive", "thread/unarchive", "thread/delete", "thread/settings/update", "thread/goal/get", "thread/goal/set", "thread/goal/clear", "thread/metadata/update", "thread/memoryMode/set", "thread/name/set", "thread/turns/list", "thread/items/list", "thread/inject_items"}, true, false, false, "", true, false),
		tool("file-change-revert", "File change revert", "Restore one exact workspace file state from durable private recovery evidence.", "workspace", []string{"item/fileChange/revert"}, services.Filesystem && services.FileRecovery, true, true, "fs/changed", true, false),
		tool("turn-runtime", "Turn runtime", "Start, resume, interrupt, steer, and retry provider-neutral Gollem app-server turns.", "runtime", []string{"thread/start", "thread/resume", "turn/start", "turn/interrupt", "turn/steer", "turn/retry"}, services.Runtime, true, false, "turn/started", true, true),
		tool("interactions", "Interactions", "Request the connected client clock, user input, dynamic tool calls, and MCP elicitation.", "runtime", []string{"currentTime/read", "item/tool/requestUserInput", "item/tool/call", "mcpServer/elicitation/request"}, services.Interactions, false, false, "serverRequest/resolved", true, false),
		tool("provider-catalog", "Provider catalog", "List provider, model, capability, and app-server tool metadata for Slang controls.", "configuration", []string{"provider/list", "provider/health/probe", "model/list", "modelProvider/capabilities/read", "provider/capabilities/read", "tool/list"}, true, false, false, "", true, true),
		tool("config", "Configuration", "Read and update app-server config, environment metadata, permission profiles, collaboration modes, and feature flags.", "configuration", []string{"config/read", "config/value/write", "config/batchWrite", "configRequirements/read", "config/mcpServer/reload", "environment/info", "environment/add", "permissionProfile/list", "collaborationMode/list", "experimentalFeature/list", "experimentalFeature/enablement/set"}, services.Config, true, false, "", true, false),
		tool("mcp", "MCP servers", "List MCP server startup status, read MCP resources, and call MCP tools through registered Gollem MCP clients.", "runtime", []string{"mcpServerStatus/list", "mcpServer/resource/read", "mcpServer/tool/call"}, services.MCP, true, true, "", true, false),
		tool("skills", "Skills and plugins", "List workspace-scoped skills and read plugin manifests and skill content from configured roots.", "configuration", []string{"skills/list", "plugin/list", "plugin/installed", "plugin/read", "plugin/skill/read"}, services.Skills, false, false, "", true, false),
		tool("cache", "Cache", "Read provider-reported cache telemetry and run deterministic normalization benchmark fixtures.", "runtime", []string{"cache/stats", "cache/benchmark"}, services.Cache, false, false, "", false, true),
		tool("memory", "Memory", "Reset local Gollem memory artifacts without changing persisted thread memory modes.", "conversation", []string{"memory/reset"}, services.Memory, true, true, "", true, false),
	}
}

func tool(id, displayName, description, category string, methods []string, configured, mutation, approval bool, outputNotification string, codexCompatible, extension bool) Tool {
	reason := ""
	if !configured {
		reason = "service is not configured in this app-server instance"
	}
	return Tool{
		ID:                 id,
		Name:               id,
		DisplayName:        displayName,
		Description:        description,
		Source:             "builtin",
		Category:           category,
		Kind:               "app-server",
		Methods:            append([]string(nil), methods...),
		Enabled:            configured,
		Available:          configured,
		Mutation:           mutation,
		RequiresApproval:   approval,
		ServiceConfigured:  configured,
		UnavailableReason:  reason,
		CodexCompatible:    codexCompatible,
		GollemExtension:    extension,
		OutputNotification: outputNotification,
	}
}

func model(providerID, name, displayName, description string, hidden bool, modalities []string, personality bool, caps ModelCapabilities, efforts []string, defaultEffort string) Model {
	var (
		supportedEfforts []ReasoningEffortOption
		defaultOption    *string
	)
	if caps.Reasoning {
		supportedEfforts = effortOptions(efforts)
		defaultOption = stringPointer(defaultEffort)
	}
	return Model{
		ID:                        modelID(providerID, name),
		ProviderID:                providerID,
		Model:                     name,
		DisplayName:               displayName,
		Description:               description,
		Hidden:                    hidden,
		SupportedReasoningEfforts: supportedEfforts,
		DefaultReasoningEffort:    defaultOption,
		InputModalities:           append([]string(nil), modalities...),
		SupportsPersonality:       personality,
		AdditionalSpeedTiers:      []string{},
		ServiceTiers:              []ModelServiceTier{},
		Capabilities:              caps,
	}
}

func stringPointer(value string) *string {
	return &value
}

func capabilities(toolCalls, structured, vision, streaming, promptCache, reasoning, toolSearch bool, sampling ...bool) ModelCapabilities {
	return ModelCapabilities{
		ToolCalls:        toolCalls,
		StructuredOutput: structured,
		Vision:           vision,
		Streaming:        streaming,
		PromptCache:      promptCache,
		Sampling:         len(sampling) == 1 && sampling[0],
		Reasoning:        reasoning,
		ToolSearch:       toolSearch,
	}
}

func effortOptions(efforts []string) []ReasoningEffortOption {
	if len(efforts) == 0 {
		efforts = []string{"low", "medium", "high"}
	}
	out := make([]ReasoningEffortOption, 0, len(efforts))
	for _, effort := range efforts {
		out = append(out, ReasoningEffortOption{
			ReasoningEffort: effort,
			Description:     effortDescription(effort),
		})
	}
	return out
}

func effortDescription(effort string) string {
	switch effort {
	case "minimal":
		return "Fastest response with minimal reasoning."
	case "low":
		return "Lower-latency reasoning."
	case "medium":
		return "Balanced reasoning and latency."
	case "high":
		return "Deeper reasoning for complex work."
	case "xhigh":
		return "Extended high-depth reasoning when supported."
	case "max":
		return "Maximum provider-supported reasoning effort."
	default:
		return "Provider-supported reasoning effort."
	}
}

func modelID(providerID, name string) string {
	return providerID + "/" + name
}

func ensureCatalog(c *Catalog) *Catalog {
	if c != nil {
		return c
	}
	return NewDefault()
}

func envConfigured(lookup EnvLookup, names ...string) bool {
	for _, name := range names {
		if value, ok := lookup(name); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func providerFilter(providerID string, providerIDs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(providerIDs)+1)
	if providerID != "" {
		out[normalizeProviderID(providerID)] = struct{}{}
	}
	for _, id := range providerIDs {
		if id != "" {
			out[normalizeProviderID(id)] = struct{}{}
		}
	}
	return out
}

func normalizeProviderID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "vertex", "gemini", "google", "google-vertex":
		return ProviderVertexAI
	case "vertex-anthropic", "vertexaianthropic", "vertex_ai_anthropic":
		return ProviderVertexAIAnthropic
	default:
		return id
	}
}

func markDefaultModel(models []Model, providers []Provider) {
	defaultID := ""
	for _, provider := range providers {
		if provider.Configured {
			defaultID = provider.DefaultModelID
			break
		}
	}
	if defaultID == "" {
		for _, provider := range providers {
			if provider.ID == ProviderOpenAI {
				defaultID = provider.DefaultModelID
				break
			}
		}
	}
	for i := range models {
		models[i].IsDefault = models[i].ID == defaultID
	}
	if !slices.ContainsFunc(models, func(model Model) bool { return model.IsDefault }) && len(models) > 0 {
		models[0].IsDefault = true
	}
}

func cloneProvider(provider Provider) Provider {
	provider.RequiredEnvVars = append([]string(nil), provider.RequiredEnvVars...)
	provider.OptionalEnvVars = append([]string(nil), provider.OptionalEnvVars...)
	provider.AuthModes = append([]string(nil), provider.AuthModes...)
	provider.Capabilities = cloneCapabilities(provider.Capabilities)
	provider.Models = cloneModels(provider.Models)
	return provider
}

func cloneProviders(providers []Provider) []Provider {
	out := make([]Provider, len(providers))
	for i, provider := range providers {
		out[i] = cloneProvider(provider)
	}
	return out
}

func cloneModel(model Model) Model {
	model.SupportedReasoningEfforts = append([]ReasoningEffortOption(nil), model.SupportedReasoningEfforts...)
	model.InputModalities = append([]string(nil), model.InputModalities...)
	model.AdditionalSpeedTiers = append([]string(nil), model.AdditionalSpeedTiers...)
	model.ServiceTiers = append([]ModelServiceTier(nil), model.ServiceTiers...)
	if model.Pricing != nil {
		pricing := *model.Pricing
		model.Pricing = &pricing
	}
	return model
}

func cloneModels(models []Model) []Model {
	out := make([]Model, len(models))
	for i, model := range models {
		out[i] = cloneModel(model)
	}
	return out
}

func cloneCapabilities(caps ProviderCapabilities) ProviderCapabilities {
	caps.ReasoningEfforts = append([]string(nil), caps.ReasoningEfforts...)
	caps.RequiresConfigurationEnv = append([]string(nil), caps.RequiresConfigurationEnv...)
	return caps
}

func cloneTool(tool Tool) Tool {
	tool.Methods = append([]string(nil), tool.Methods...)
	return tool
}

func cloneTools(tools []Tool) []Tool {
	out := make([]Tool, len(tools))
	for i, tool := range tools {
		out[i] = cloneTool(tool)
	}
	return out
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		if addition == "" || slices.Contains(values, addition) {
			continue
		}
		values = append(values, addition)
	}
	return values
}
