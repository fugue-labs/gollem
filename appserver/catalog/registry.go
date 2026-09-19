package catalog

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/fugue-labs/gollem/appserver/catalog/catwalk"
	anthropicprovider "github.com/fugue-labs/gollem/provider/anthropic"
	openaiprovider "github.com/fugue-labs/gollem/provider/openai"
	vertexprovider "github.com/fugue-labs/gollem/provider/vertexai"
	vertexanthropicprovider "github.com/fugue-labs/gollem/provider/vertexai_anthropic"
)

// ProviderXAI is the built-in xAI provider (OpenAI Responses API shape).
const ProviderXAI = "xai"

// ProviderDeepSeek is the built-in DeepSeek provider (openai.NewDeepSeek).
const ProviderDeepSeek = "deepseek"

// RuntimeEndpointFamily selects which Gollem provider implementation drives a
// registry provider.
type RuntimeEndpointFamily string

// Runtime endpoint families.
const (
	RuntimeFamilyAnthropic RuntimeEndpointFamily = "anthropic"
	RuntimeFamilyOpenAI    RuntimeEndpointFamily = "openai"
)

// RuntimeEndpoint describes how to construct a model for a registry provider
// that has no dedicated constructor in Gollem. It names the credential
// environment variable rather than carrying the secret.
type RuntimeEndpoint struct {
	ProviderID          string
	Family              RuntimeEndpointFamily
	BaseURL             string
	APIKeyEnv           string
	Headers             map[string]string
	ChatCompletionsOnly bool
	// DefaultModel is the bare model name used when a selection names none.
	DefaultModel string
}

// anthropicDefaultModel is the catalog's Anthropic default whenever the
// registry offers it. This is a deliberate divergence from the library
// constructor's default (anthropic.New without WithModel still targets Sonnet
// 4.6): the CLI and app-server resolve an empty model through the catalog,
// and for agentic coding work the flagship is the right starting point. The
// CLI help text states that the catalog default applies.
const anthropicDefaultModel = anthropicprovider.ClaudeOpus5

// coreProviderOrder lists the providers that always sort first, in order.
var coreProviderOrder = []string{
	ProviderAnthropic,
	ProviderOpenAI,
	ProviderOpenAICompatibleLocal,
	ProviderVertexAI,
	ProviderVertexAIAnthropic,
	ProviderXAI,
	ProviderDeepSeek,
}

// buildProviders turns registry data into catalog entries. Every entry is
// self-contained: callers may clone and mutate the result freely.
func (c *Catalog) buildProviders(registry []catwalk.Provider) ([]Provider, map[string]RuntimeEndpoint) {
	providers := make([]Provider, 0, len(registry)+2)
	endpoints := make(map[string]RuntimeEndpoint)

	openaiConfigured := envConfigured(c.env, "OPENAI_API_KEY") || envConfigured(c.env, "CHATGPT_ACCESS_TOKEN")
	anthropicConfigured := envConfigured(c.env, "ANTHROPIC_API_KEY")
	vertexConfigured := envConfigured(c.env, "GOOGLE_CLOUD_PROJECT") || envConfigured(c.env, "GOOGLE_APPLICATION_CREDENTIALS")

	anthropicModels := map[string]catwalk.Model{}
	for _, source := range registry {
		if source.ID == ProviderAnthropic {
			for _, entry := range source.Models {
				anthropicModels[entry.ID] = entry
			}
		}
	}

	// Registry ids map onto catalog ids (Vertex fans out to two fixed ids);
	// the first mapping wins so catalog ids stay unique.
	seen := map[string]struct{}{ProviderOpenAICompatibleLocal: {}, "test": {}}
	add := func(candidates ...Provider) {
		for _, provider := range candidates {
			if _, dup := seen[provider.ID]; dup {
				continue
			}
			seen[provider.ID] = struct{}{}
			providers = append(providers, provider)
		}
	}
	for _, source := range registry {
		if _, dup := seen[source.ID]; dup {
			continue
		}
		switch {
		case source.ID == ProviderAnthropic:
			add(c.anthropicProvider(source, anthropicConfigured))
		case source.ID == ProviderOpenAI:
			add(c.openaiProvider(source, openaiConfigured))
		case source.ID == ProviderXAI:
			add(c.xaiProvider(source))
		case source.ID == ProviderDeepSeek:
			add(c.deepSeekProvider(source))
		case source.Type == catwalk.TypeVertexAI:
			add(c.vertexProviders(source, vertexConfigured, anthropicModels)...)
		case source.Type == catwalk.TypeAnthropic:
			provider, endpoint, ok := c.anthropicCompatibleProvider(source)
			if ok {
				add(provider)
				endpoints[provider.ID] = endpoint
			}
		case source.Type == catwalk.TypeOpenAI, source.Type == catwalk.TypeOpenAICompat, source.Type == catwalk.TypeOpenRouter:
			provider, endpoint, ok := c.openaiCompatibleProvider(source)
			if ok {
				add(provider)
				endpoints[provider.ID] = endpoint
			}
		}
	}
	providers = append(providers, c.localProvider())
	sortProviders(providers)
	return providers, endpoints
}

// sortProviders orders core providers first, then the rest by id.
func sortProviders(providers []Provider) {
	sort.SliceStable(providers, func(i, j int) bool {
		ri, rj := coreRank(providers[i].ID), coreRank(providers[j].ID)
		if ri != rj {
			return ri < rj
		}
		return providers[i].ID < providers[j].ID
	})
}

func coreRank(id string) int {
	if index := slices.Index(coreProviderOrder, id); index >= 0 {
		return index
	}
	return len(coreProviderOrder)
}

func (c *Catalog) anthropicProvider(source catwalk.Provider, configured bool) Provider {
	provider := Provider{
		ID:              ProviderAnthropic,
		Name:            "Anthropic",
		Package:         "github.com/fugue-labs/gollem/provider/anthropic",
		Description:     "Anthropic Messages API models through Gollem's provider-neutral core.Model interface.",
		Configured:      configured,
		RequiredEnvVars: []string{"ANTHROPIC_API_KEY"},
		AuthModes:       []string{"api-key"},
		Capabilities: ProviderCapabilities{
			ToolCalls:        true,
			StructuredOutput: true,
			Vision:           true,
			Streaming:        true,
			PromptCache:      true,
			Sampling:         true,
			StopSequences:    true,
			ToolSearch:       true,
			Reasoning:        true,
			ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"},
			AdaptiveThinking: true,
			ManualThinking:   true,
		},
	}
	for _, entry := range source.Models {
		provider.Models = append(provider.Models, anthropicModel(ProviderAnthropic, source.Name, entry, ""))
	}
	provider.DefaultModelID = pickDefault(provider, source.DefaultLargeModelID, anthropicDefaultModel)
	return provider
}

// anthropicModel derives capabilities from the same predicates the Anthropic
// request builder uses, so the catalog never advertises a control the
// provider would reject.
func anthropicModel(providerID, providerName string, entry catwalk.Model, nameSuffix string) Model {
	caps := ModelCapabilities{
		ToolCalls:        true,
		StructuredOutput: true,
		Vision:           entry.SupportsImages,
		Streaming:        true,
		PromptCache:      true,
		StopSequences:    true,
		ToolSearch:       anthropicprovider.SupportsToolSearch(entry.ID),
		Sampling:         anthropicprovider.SupportsSampling(entry.ID),
		Reasoning:        entry.CanReason && anthropicprovider.SupportsEffort(entry.ID),
		AdaptiveThinking: anthropicprovider.SupportsAdaptiveThinking(entry.ID),
	}
	// Manual budgets follow the request builder's own rule: every reasoning
	// model outside the adaptive-only generations accepts budget_tokens,
	// including models without effort control (Haiku 4.5, Sonnet 4.5).
	caps.ManualThinking = entry.CanReason && anthropicprovider.SupportsManualThinking(entry.ID)
	var efforts []string
	for _, effort := range entry.ReasoningLevels {
		if anthropicprovider.SupportsEffortValue(entry.ID, effort) {
			efforts = append(efforts, effort)
		}
	}
	if caps.Reasoning && len(efforts) == 0 {
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
			if anthropicprovider.SupportsEffortValue(entry.ID, effort) {
				efforts = append(efforts, effort)
			}
		}
	}
	return registryModel(providerID, providerName, entry, nameSuffix, caps, efforts)
}

func (c *Catalog) openaiProvider(source catwalk.Provider, configured bool) Provider {
	provider := Provider{
		ID:              ProviderOpenAI,
		Name:            "OpenAI",
		Package:         "github.com/fugue-labs/gollem/provider/openai",
		Description:     "OpenAI Responses and Chat Completions models through Gollem's provider-neutral core.Model interface.",
		Configured:      configured,
		RequiredEnvVars: []string{"OPENAI_API_KEY"},
		OptionalEnvVars: []string{"OPENAI_BASE_URL", "OPENAI_SERVICE_TIER", "CHATGPT_ACCESS_TOKEN"},
		AuthModes:       []string{"api-key", "chatgpt-subscription"},
		Capabilities: ProviderCapabilities{
			NamespaceTools:     true,
			ToolCalls:          true,
			StructuredOutput:   true,
			Vision:             true,
			Streaming:          true,
			PromptCache:        true,
			Sampling:           true,
			Reasoning:          true,
			ReasoningEfforts:   []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
			ReasoningSummaries: true,
		},
	}
	for _, entry := range source.Models {
		caps := ModelCapabilities{
			ToolCalls:          true,
			StructuredOutput:   true,
			Vision:             entry.SupportsImages,
			Streaming:          true,
			PromptCache:        true,
			Reasoning:          entry.CanReason,
			ReasoningSummaries: entry.CanReason,
			// OpenAI reasoning models reject temperature/top_p.
			Sampling: !entry.CanReason,
		}
		provider.Models = append(provider.Models, registryModel(ProviderOpenAI, source.Name, entry, "", caps, entry.ReasoningLevels))
	}
	provider.DefaultModelID = pickDefault(provider, source.DefaultLargeModelID, c.openaiCompatibilityDefault())
	return provider
}

// openaiCompatibilityDefault returns the model to prefer as the OpenAI default
// when OPENAI_BASE_URL points away from OpenAI (a LiteLLM or other
// Chat-Completions-only proxy). The registry default is a GPT-5 model that
// the transport routes to the Responses API, which such proxies often lack;
// the provider's own long-standing default keeps them working.
func (c *Catalog) openaiCompatibilityDefault() string {
	value, _ := c.env("OPENAI_BASE_URL")
	base := strings.TrimSpace(value)
	if base == "" || strings.Contains(base, "openai.com") {
		return ""
	}
	return openaiprovider.GPT4o
}

func (c *Catalog) xaiProvider(source catwalk.Provider) Provider {
	keyEnv := "XAI_API_KEY"
	if env, ok := catwalk.EnvReference(source.APIKey); ok {
		keyEnv = env
	}
	configured := envConfigured(c.env, keyEnv)
	provider := Provider{
		ID:              ProviderXAI,
		Name:            firstNonEmpty(source.Name, "xAI"),
		Package:         "github.com/fugue-labs/gollem/provider/openai",
		Description:     "xAI Grok models through the OpenAI Responses API shape.",
		Configured:      configured,
		Hidden:          !configured,
		RequiredEnvVars: []string{keyEnv},
		AuthModes:       []string{"api-key"},
		Capabilities: ProviderCapabilities{
			ToolCalls:   true,
			Vision:      true,
			Streaming:   true,
			PromptCache: true,
			Sampling:    true,
		},
	}
	for _, entry := range source.Models {
		// The Responses transport does not forward reasoning effort to
		// non-OpenAI endpoints, so effort control is not advertised here.
		// Sampling is forwarded unchanged and xAI accepts it.
		caps := ModelCapabilities{
			ToolCalls:   true,
			Vision:      entry.SupportsImages,
			Streaming:   true,
			PromptCache: true,
			Sampling:    true,
		}
		provider.Models = append(provider.Models, registryModel(ProviderXAI, source.Name, entry, "", caps, nil))
	}
	provider.DefaultModelID = pickDefault(provider, source.DefaultLargeModelID, "")
	return provider
}

// deepSeekProvider maps the registry's DeepSeek entry onto openai.NewDeepSeek,
// which speaks DeepSeek's thinking toggle and reasoning_content natively.
func (c *Catalog) deepSeekProvider(source catwalk.Provider) Provider {
	keyEnv := openaiprovider.DeepSeekAPIKeyEnv
	if env, ok := catwalk.EnvReference(source.APIKey); ok {
		keyEnv = env
	}
	configured := envConfigured(c.env, keyEnv)
	provider := Provider{
		ID:              ProviderDeepSeek,
		Name:            firstNonEmpty(source.Name, "DeepSeek"),
		Package:         "github.com/fugue-labs/gollem/provider/openai",
		Description:     "DeepSeek V4 models through DeepSeek's Chat Completions API with native thinking support.",
		Configured:      configured,
		Hidden:          !configured,
		RequiredEnvVars: []string{keyEnv},
		OptionalEnvVars: []string{openaiprovider.DeepSeekBaseURLEnv},
		AuthModes:       []string{"api-key"},
		Capabilities: ProviderCapabilities{
			ToolCalls:        true,
			Streaming:        true,
			PromptCache:      true,
			Sampling:         true,
			Reasoning:        true,
			AdaptiveThinking: true,
		},
	}
	effortSet := map[string]struct{}{}
	models := supplementModels(source.Models, deepSeekModels, nil)
	for _, entry := range models {
		reasoning := entry.CanReason && len(entry.ReasoningLevels) > 0
		caps := ModelCapabilities{
			ToolCalls:   true,
			Streaming:   true,
			PromptCache: true,
			Vision:      entry.SupportsImages || openaiprovider.DeepSeekModelSupportsVision(entry.ID),
			// DeepSeek accepts sampling alongside thinking; the thinking
			// toggle is exposed as the adaptive-thinking control.
			Sampling:         true,
			Reasoning:        reasoning,
			AdaptiveThinking: entry.CanReason,
		}
		var efforts []string
		if reasoning {
			efforts = entry.ReasoningLevels
			for _, effort := range efforts {
				effortSet[effort] = struct{}{}
			}
		}
		provider.Models = append(provider.Models, registryModel(ProviderDeepSeek, source.Name, entry, "", caps, efforts))
	}
	for _, effort := range []string{"low", "medium", "high", "max"} {
		if _, ok := effortSet[effort]; ok {
			provider.Capabilities.ReasoningEfforts = append(provider.Capabilities.ReasoningEfforts, effort)
		}
	}
	provider.Capabilities.Reasoning = len(effortSet) > 0
	provider.DefaultModelID = pickDefault(provider, source.DefaultLargeModelID, "")
	return provider
}

// deepSeekModels are the ids DeepSeek documents as served; the registry may
// list only the versioned aliases.
// Prices are DeepSeek's published off-peak USD rates per 1M tokens.
var deepSeekModels = []catwalk.Model{
	{ID: openaiprovider.DeepSeekV4Pro, Name: "DeepSeek V4 Pro", ContextWindow: 1_000_000, DefaultMaxTokens: openaiprovider.DeepSeekMaxOutputTokens, CostPer1MIn: 0.66, CostPer1MOut: 1.98, CostPer1MInCached: 0.022, CanReason: true, ReasoningLevels: []string{"low", "high", "max"}, DefaultReasoningEffort: "high"},
	{ID: openaiprovider.DeepSeekFlash, Name: "DeepSeek Flash", ContextWindow: 1_000_000, DefaultMaxTokens: openaiprovider.DeepSeekMaxOutputTokens, CostPer1MIn: 0.15, CostPer1MOut: 0.6, CostPer1MInCached: 0.003, CanReason: true, ReasoningLevels: []string{"low", "high", "max"}, DefaultReasoningEffort: "high", SupportsImages: true},
}

// vertexGeminiModels and vertexClaudeModels are the models Gollem's Vertex
// transports are known to drive. They are merged with the registry's Vertex
// entry so a registry that tracks fewer models never hides a supported one.
var (
	vertexGeminiModels = []string{
		vertexprovider.Gemini31ProPreview,
		vertexprovider.Gemini3FlashPreview,
		vertexprovider.Gemini25Pro,
		vertexprovider.Gemini25Flash,
		vertexprovider.Gemini20Flash,
	}
	vertexClaudeModels = []string{
		vertexanthropicprovider.ClaudeOpus47,
		vertexanthropicprovider.ClaudeOpus46,
		vertexanthropicprovider.ClaudeSonnet46,
		vertexanthropicprovider.ClaudeHaiku45,
	}
)

// supplementVertexModels appends known-supported models the registry omitted.
// Metadata is borrowed from `known` (the registry's first-party entry for the
// same model id) when available. A constant is skipped when the registry
// already lists it or a dated variant of it (same id prefix).
func supplementVertexModels(models []catwalk.Model, constants []string, known map[string]catwalk.Model) []catwalk.Model {
	defaults := make([]catwalk.Model, 0, len(constants))
	for _, id := range constants {
		// Every Vertex model Gollem drives is multimodal.
		defaults = append(defaults, catwalk.Model{ID: id, Name: id, CanReason: strings.HasPrefix(id, "claude"), SupportsImages: true})
	}
	return supplementModels(models, defaults, known)
}

// undatedClaudeID strips a trailing "-YYYYMMDD" snapshot date from a Claude id.
func undatedClaudeID(id string) (string, bool) {
	index := strings.LastIndex(id, "-")
	if index < 0 || len(id)-index-1 != 8 {
		return id, false
	}
	base := id[:index]
	if !isDatedVariant(id, base) {
		return id, false
	}
	return base, true
}

// isDatedVariant reports whether id is base followed by "-" and a numeric
// snapshot date (claude-haiku-4-5-20251001 for claude-haiku-4-5). A sibling
// with a non-numeric suffix (gemini-2.5-flash-lite) is a different model.
func isDatedVariant(id, base string) bool {
	suffix, ok := strings.CutPrefix(id, base+"-")
	if !ok || suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// supplementModels appends each default whose id (or a dated variant of it)
// is missing from models. Metadata comes from `known` when it lists the same
// id, otherwise from the default itself.
func supplementModels(models []catwalk.Model, defaults []catwalk.Model, known map[string]catwalk.Model) []catwalk.Model {
	out := slices.Clone(models)
	for _, fallback := range defaults {
		id := fallback.ID
		if slices.ContainsFunc(out, func(m catwalk.Model) bool { return m.ID == id || isDatedVariant(m.ID, id) }) {
			continue
		}
		entry, ok := known[id]
		if !ok {
			entry = fallback
		}
		entry.ID = id
		out = append(out, entry)
	}
	return out
}

func (c *Catalog) vertexProviders(source catwalk.Provider, configured bool, anthropicModels map[string]catwalk.Model) []Provider {
	gemini := Provider{
		ID:              ProviderVertexAI,
		Name:            "Vertex AI Gemini",
		Package:         "github.com/fugue-labs/gollem/provider/vertexai",
		Description:     "Google Gemini models through Vertex AI and Gollem's provider-neutral core.Model interface.",
		Configured:      configured,
		Hidden:          !configured,
		RequiredEnvVars: []string{"GOOGLE_CLOUD_PROJECT"},
		OptionalEnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS", "VERTEXAI_CACHED_CONTENT"},
		AuthModes:       []string{"application-default-credentials", "service-account"},
		Capabilities: ProviderCapabilities{
			ToolCalls:        true,
			StructuredOutput: true,
			Vision:           true,
			Streaming:        true,
			PromptCache:      true,
			Sampling:         true,
			StopSequences:    true,
		},
	}
	claude := Provider{
		ID:              ProviderVertexAIAnthropic,
		Name:            "Vertex AI Anthropic",
		Package:         "github.com/fugue-labs/gollem/provider/vertexai_anthropic",
		Description:     "Anthropic Claude models accessed through Google Vertex AI rawPredict.",
		Configured:      configured,
		Hidden:          !configured,
		RequiredEnvVars: []string{"GOOGLE_CLOUD_PROJECT"},
		OptionalEnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS"},
		AuthModes:       []string{"application-default-credentials", "service-account"},
		Capabilities: ProviderCapabilities{
			ToolCalls:        true,
			StructuredOutput: true,
			Vision:           true,
			Streaming:        true,
			PromptCache:      true,
			Sampling:         true,
			StopSequences:    true,
			Reasoning:        true,
			ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"},
			AdaptiveThinking: true,
			ManualThinking:   true,
		},
	}
	var geminiDefault, claudeDefault string
	var geminiSource, claudeSource []catwalk.Model
	for _, entry := range source.Models {
		if strings.HasPrefix(strings.ToLower(entry.ID), "claude") {
			// Vertex model ids carry no "-YYYYMMDD" suffix (snapshots use
			// "@date"); the registry lists first-party dated ids, so map
			// them to the alias rawPredict accepts.
			if alias, ok := undatedClaudeID(entry.ID); ok {
				if entry.ID == source.DefaultLargeModelID {
					source.DefaultLargeModelID = alias
				}
				entry.ID = alias
			}
			if slices.ContainsFunc(claudeSource, func(m catwalk.Model) bool { return m.ID == entry.ID }) {
				continue
			}
			claudeSource = append(claudeSource, entry)
		} else {
			geminiSource = append(geminiSource, entry)
		}
	}
	claudeSource = supplementVertexModels(claudeSource, vertexClaudeModels, anthropicModels)
	geminiSource = supplementVertexModels(geminiSource, vertexGeminiModels, nil)
	for _, entry := range append(claudeSource, geminiSource...) {
		if strings.HasPrefix(strings.ToLower(entry.ID), "claude") {
			model := anthropicModel(ProviderVertexAIAnthropic, "Vertex AI", entry, " on Vertex AI")
			// The Vertex transport has no tool-search support.
			model.Capabilities.ToolSearch = false
			claude.Models = append(claude.Models, model)
			if entry.ID == source.DefaultLargeModelID {
				claudeDefault = entry.ID
			}
			continue
		}
		// The Vertex Gemini transport does not expose thinking controls yet,
		// so reasoning is not advertised even where the registry reports it.
		caps := ModelCapabilities{
			ToolCalls:        true,
			StructuredOutput: true,
			Vision:           entry.SupportsImages,
			Streaming:        true,
			PromptCache:      true,
			Sampling:         true,
			StopSequences:    true,
		}
		gemini.Models = append(gemini.Models, registryModel(ProviderVertexAI, "Vertex AI", entry, "", caps, nil))
		if entry.ID == source.DefaultLargeModelID {
			geminiDefault = entry.ID
		}
	}
	// Defaults are pinned to the transports' own defaults; registry order is
	// not a contract.
	gemini.DefaultModelID = pickDefault(gemini, geminiDefault, vertexprovider.Gemini25Flash)
	claude.DefaultModelID = pickDefault(claude, claudeDefault, vertexanthropicprovider.ClaudeSonnet46)
	var out []Provider
	if len(gemini.Models) > 0 {
		out = append(out, gemini)
	}
	if len(claude.Models) > 0 {
		out = append(out, claude)
	}
	return out
}

// anthropicCompatibleProvider maps a third-party Anthropic-Messages-compatible
// endpoint onto Gollem's Anthropic provider with a custom base URL. Thinking
// and effort controls are not advertised: the registry does not state which
// of Anthropic's extensions each endpoint honours.
func (c *Catalog) anthropicCompatibleProvider(source catwalk.Provider) (Provider, RuntimeEndpoint, bool) {
	endpoint, ok := c.registryEndpoint(source, RuntimeFamilyAnthropic)
	if !ok {
		return Provider{}, RuntimeEndpoint{}, false
	}
	provider := Provider{
		ID:              source.ID,
		Name:            firstNonEmpty(source.Name, source.ID),
		Package:         "github.com/fugue-labs/gollem/provider/anthropic",
		Description:     firstNonEmpty(source.Name, source.ID) + " models through an Anthropic Messages-compatible endpoint.",
		Configured:      envConfigured(c.env, endpoint.APIKeyEnv),
		RequiredEnvVars: []string{endpoint.APIKeyEnv},
		AuthModes:       []string{"api-key"},
		Capabilities: ProviderCapabilities{
			ToolCalls:     true,
			Vision:        true,
			Streaming:     true,
			Sampling:      true,
			StopSequences: true,
		},
	}
	provider.Hidden = !provider.Configured
	for _, entry := range source.Models {
		caps := ModelCapabilities{
			ToolCalls:     true,
			Vision:        entry.SupportsImages,
			Streaming:     true,
			Sampling:      true,
			StopSequences: true,
		}
		provider.Models = append(provider.Models, registryModel(source.ID, source.Name, entry, "", caps, nil))
	}
	provider.DefaultModelID = pickDefault(provider, source.DefaultLargeModelID, "")
	endpoint.DefaultModel = strings.TrimPrefix(provider.DefaultModelID, provider.ID+"/")
	return provider, endpoint, true
}

// openaiCompatibleProvider maps a Chat-Completions-compatible endpoint onto
// Gollem's OpenAI provider. Registry providers of type "openai" that are not
// OpenAI itself keep the automatic Responses/Chat Completions fallback; the
// "openai-compat" and "openrouter" types are pinned to Chat Completions.
func (c *Catalog) openaiCompatibleProvider(source catwalk.Provider) (Provider, RuntimeEndpoint, bool) {
	endpoint, ok := c.registryEndpoint(source, RuntimeFamilyOpenAI)
	if !ok {
		return Provider{}, RuntimeEndpoint{}, false
	}
	endpoint.ChatCompletionsOnly = source.Type != catwalk.TypeOpenAI
	name := firstNonEmpty(source.Name, source.ID)
	provider := Provider{
		ID:              source.ID,
		Name:            name,
		Package:         "github.com/fugue-labs/gollem/provider/openai",
		Description:     name + " models through an OpenAI-compatible endpoint.",
		Configured:      envConfigured(c.env, endpoint.APIKeyEnv),
		RequiredEnvVars: []string{endpoint.APIKeyEnv},
		AuthModes:       []string{"api-key"},
		Capabilities: ProviderCapabilities{
			ToolCalls: true,
			Vision:    true,
			Streaming: true,
			Sampling:  true,
			Reasoning: true,
		},
	}
	provider.Hidden = !provider.Configured
	effortSet := map[string]struct{}{}
	for _, entry := range source.Models {
		reasoning := entry.CanReason && len(entry.ReasoningLevels) > 0
		// Only OpenAI's own reasoning models reject temperature; gateways
		// receive sampling parameters unchanged.
		caps := ModelCapabilities{
			ToolCalls: true,
			Vision:    entry.SupportsImages,
			Streaming: true,
			Reasoning: reasoning,
			Sampling:  true,
		}
		var efforts []string
		if reasoning {
			efforts = entry.ReasoningLevels
			for _, effort := range efforts {
				effortSet[effort] = struct{}{}
			}
		}
		provider.Models = append(provider.Models, registryModel(source.ID, source.Name, entry, "", caps, efforts))
	}
	if len(effortSet) == 0 {
		provider.Capabilities.Reasoning = false
	} else {
		for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
			if _, ok := effortSet[effort]; ok {
				provider.Capabilities.ReasoningEfforts = append(provider.Capabilities.ReasoningEfforts, effort)
			}
		}
	}
	provider.DefaultModelID = pickDefault(provider, source.DefaultLargeModelID, "")
	endpoint.DefaultModel = strings.TrimPrefix(provider.DefaultModelID, provider.ID+"/")
	return provider, endpoint, true
}

// registryEndpoint resolves a third-party provider's endpoint. It requires an
// environment-referenced API key and a usable base URL; literal secrets in the
// registry are never honoured.
func (c *Catalog) registryEndpoint(source catwalk.Provider, family RuntimeEndpointFamily) (RuntimeEndpoint, bool) {
	keyEnv, ok := catwalk.EnvReference(source.APIKey)
	if !ok {
		return RuntimeEndpoint{}, false
	}
	baseURL := strings.TrimSpace(source.APIEndpoint)
	if env, isRef := catwalk.EnvReference(baseURL); isRef {
		value, _ := c.env(env)
		baseURL = strings.TrimSpace(value)
	}
	// Credentials only travel over TLS. Loopback mirrors for local testing
	// belong to the openai-compatible-local provider, not the registry.
	if !strings.HasPrefix(baseURL, "https://") {
		return RuntimeEndpoint{}, false
	}
	if family == RuntimeFamilyAnthropic {
		// The Anthropic transport appends "/v1/messages" verbatim.
		baseURL = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	}
	// Only the OpenAI transport forwards attribution headers; refusing the
	// endpoint is better than silently dropping headers the gateway requires.
	if family == RuntimeFamilyAnthropic && len(source.DefaultHeaders) > 0 {
		return RuntimeEndpoint{}, false
	}
	return RuntimeEndpoint{
		ProviderID: source.ID,
		Family:     family,
		BaseURL:    baseURL,
		APIKeyEnv:  keyEnv,
		Headers:    attributionHeaders(source.DefaultHeaders),
	}, true
}

// Gollem's own attribution for gateways that ask who is calling.
const (
	attributionTitle    = "Gollem"
	attributionReferrer = "https://github.com/fugue-labs/gollem"
)

// attributionHeaders keeps only the well-known attribution headers from a
// registry entry and rewrites them to identify Gollem. Anything else (affiliate
// codes, third-party integration markers) belongs to the registry's origin
// client and is dropped.
func attributionHeaders(defaults map[string]string) map[string]string {
	var out map[string]string
	for name := range defaults {
		var value string
		switch strings.ToLower(name) {
		case "x-title":
			value = attributionTitle
		case "http-referer":
			value = attributionReferrer
		default:
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = value
	}
	return out
}

func (c *Catalog) localProvider() Provider {
	localConfig, localConfigured := c.localEndpointConfig()
	localModel := "llama3"
	if localConfigured {
		localModel = localConfig.Model
	}
	return Provider{
		ID:             ProviderOpenAICompatibleLocal,
		Name:           "Local OpenAI-compatible",
		Package:        "github.com/fugue-labs/gollem/provider/openai",
		Description:    "A loopback-only OpenAI Chat Completions endpoint with a configured local model fallback.",
		Configured:     localConfigured,
		DefaultModelID: modelID(ProviderOpenAICompatibleLocal, localModel),
		OptionalEnvVars: []string{
			openaiprovider.LocalEndpointBaseURLEnv,
			openaiprovider.LocalEndpointModelEnv,
			openaiprovider.LocalEndpointAPIKeyEnv,
		},
		AuthModes: []string{"local-loopback"},
		Capabilities: ProviderCapabilities{
			ToolCalls: true,
			Streaming: true,
		},
		Models: []Model{
			model(
				ProviderOpenAICompatibleLocal,
				localModel,
				localModel,
				"Configured local OpenAI-compatible Chat Completions model. Structured output and multimodal capabilities remain unavailable until separately verified.",
				false,
				[]string{"text"},
				false,
				capabilities(true, false, false, true, false, false, false),
				nil,
				"low",
			),
		},
	}
}

// registryModel builds a catalog entry from registry metadata.
func registryModel(providerID, providerName string, entry catwalk.Model, nameSuffix string, caps ModelCapabilities, efforts []string) Model {
	name := firstNonEmpty(entry.Name, entry.ID) + nameSuffix
	modalities := []string{"text"}
	if entry.SupportsImages {
		modalities = append(modalities, "image")
	}
	defaultEffort := entry.DefaultReasoningEffort
	if caps.Reasoning && len(efforts) > 0 && !slices.Contains(efforts, defaultEffort) {
		defaultEffort = efforts[len(efforts)/2]
	}
	out := model(
		providerID,
		entry.ID,
		name,
		fmt.Sprintf("%s (%s) from %s.", name, entry.ID, firstNonEmpty(providerName, providerID)),
		false,
		modalities,
		false,
		caps,
		efforts,
		defaultEffort,
	)
	if caps.Reasoning && len(efforts) == 0 {
		// model() substitutes a low/medium/high default; only advertise
		// efforts the source actually listed.
		out.SupportedReasoningEfforts = []ReasoningEffortOption{}
		out.DefaultReasoningEffort = nil
	}
	if entry.ContextWindow > 0 {
		out.MaxContextTokens = clampInt(entry.ContextWindow)
	}
	if entry.DefaultMaxTokens > 0 {
		out.MaxOutputTokens = clampInt(entry.DefaultMaxTokens)
	}
	// A supplemented model with no registry metadata at all gets no pricing
	// rather than a misleading free price. Registry models that are free
	// still report zeros because they carry a context window.
	if entry.ContextWindow > 0 || entry.DefaultMaxTokens > 0 || entry.CostPer1MIn > 0 || entry.CostPer1MOut > 0 || entry.CostPer1MInCached > 0 || entry.CostPer1MOutCached > 0 {
		out.Pricing = &protocolPricing{
			InputPer1M:        entry.CostPer1MIn,
			OutputPer1M:       entry.CostPer1MOut,
			CachedInputPer1M:  entry.CostPer1MInCached,
			CachedOutputPer1M: entry.CostPer1MOutCached,
		}
	}
	return out
}

// pickDefault returns the provider's default model id, preferring
// `preferred`, then the registry's own default, then the first model.
func pickDefault(provider Provider, registryDefault, preferred string) string {
	has := func(name string) bool {
		return name != "" && slices.ContainsFunc(provider.Models, func(m Model) bool { return m.Model == name })
	}
	switch {
	case has(preferred):
		return modelID(provider.ID, preferred)
	case has(registryDefault):
		return modelID(provider.ID, registryDefault)
	case len(provider.Models) > 0:
		return provider.Models[0].ID
	default:
		return ""
	}
}

func clampInt(value int64) int {
	const maxInt = int64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	return int(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
