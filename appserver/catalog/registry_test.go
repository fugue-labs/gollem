package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/appserver/catalog/catwalk"
)

func sampleRegistry() []catwalk.Provider {
	return []catwalk.Provider{
		{
			ID: "anthropic", Name: "Anthropic", Type: catwalk.TypeAnthropic, APIKey: "$ANTHROPIC_API_KEY",
			DefaultLargeModelID: "claude-sonnet-4-6",
			Models: []catwalk.Model{
				{ID: "claude-fable-5-1", Name: "Claude Fable 5.1", CostPer1MIn: 10, CostPer1MOut: 50, ContextWindow: 1_000_000, DefaultMaxTokens: 128_000, CanReason: true, ReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"}, DefaultReasoningEffort: "high", SupportsImages: true},
				{ID: "claude-opus-5", Name: "Claude Opus 5", CostPer1MIn: 5, CostPer1MOut: 25, CostPer1MInCached: 0.5, ContextWindow: 1_000_000, DefaultMaxTokens: 64_000, CanReason: true, ReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"}, DefaultReasoningEffort: "high", SupportsImages: true},
				{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", CostPer1MIn: 3, CostPer1MOut: 15, ContextWindow: 200_000, CanReason: true, ReasoningLevels: []string{"low", "medium", "high", "max"}, DefaultReasoningEffort: "high", SupportsImages: true},
				{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", CostPer1MIn: 1, CostPer1MOut: 5, ContextWindow: 200_000, CanReason: true, SupportsImages: true},
			},
		},
		{
			ID: "openai", Name: "OpenAI", Type: catwalk.TypeOpenAI, APIKey: "$OPENAI_API_KEY", APIEndpoint: "$OPENAI_API_ENDPOINT",
			DefaultLargeModelID: "gpt-5.6-sol",
			Models: []catwalk.Model{
				{ID: "gpt-6-astra", Name: "GPT-6 Astra", ContextWindow: 1_050_000, CanReason: true, ReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"}, DefaultReasoningEffort: "medium", SupportsImages: true},
				{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", ContextWindow: 1_050_000, CanReason: true, ReasoningLevels: []string{"none", "low", "medium", "high", "xhigh", "max"}, DefaultReasoningEffort: "medium", SupportsImages: true},
				{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128_000, SupportsImages: true},
			},
		},
		{
			ID: "vertexai", Name: "VertexAI", Type: catwalk.TypeVertexAI, DefaultLargeModelID: "gemini-3.1-pro-preview",
			Models: []catwalk.Model{
				{ID: "gemini-3.1-pro-preview", Name: "Gemini 3.1 Pro", ContextWindow: 1_048_576, CanReason: true, ReasoningLevels: []string{"low", "medium", "high"}},
				{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", ContextWindow: 1_048_576},
				{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", ContextWindow: 1_000_000, CanReason: true, ReasoningLevels: []string{"low", "medium", "high", "max"}, DefaultReasoningEffort: "medium", SupportsImages: true},
				{ID: "claude-haiku-4-5-20251001", Name: "Claude 4.5 Haiku", ContextWindow: 200_000, CanReason: true},
			},
		},
		{
			ID: "xai", Name: "xAI", Type: catwalk.TypeOpenAICompat, APIKey: "$XAI_API_KEY", APIEndpoint: "https://api.x.ai/v1", DefaultLargeModelID: "grok-4.5",
			Models: []catwalk.Model{{ID: "grok-4.5", Name: "Grok 4.5", CanReason: true, ReasoningLevels: []string{"low", "high"}}},
		},
		{
			ID: "deepseek", Name: "DeepSeek", Type: catwalk.TypeOpenAICompat, APIKey: "$DEEPSEEK_API_KEY", APIEndpoint: "https://api.deepseek.com/v1",
			DefaultLargeModelID: "deepseek-v4-pro", DefaultSmallModelID: "deepseek-v4-flash",
			Models: []catwalk.Model{
				{ID: "deepseek-v4-flash", Name: "DeepSeek-V4-Flash", CostPer1MIn: 0.14, CostPer1MOut: 0.28, ContextWindow: 1_000_000, CanReason: true, ReasoningLevels: []string{"low", "high", "max"}, DefaultReasoningEffort: "high"},
				{ID: "deepseek-v4-pro", Name: "DeepSeek-V4-Pro", CostPer1MIn: 0.435, CostPer1MOut: 0.87, ContextWindow: 1_000_000, CanReason: true, ReasoningLevels: []string{"low", "high", "max"}, DefaultReasoningEffort: "high"},
			},
		},
		{
			ID: "groq", Name: "Groq", Type: catwalk.TypeOpenAICompat, APIKey: "$GROQ_API_KEY", APIEndpoint: "https://api.groq.com/openai/v1",
			DefaultLargeModelID: "openai/gpt-oss-120b",
			Models: []catwalk.Model{
				{ID: "openai/gpt-oss-120b", Name: "GPT-OSS 120B", CostPer1MIn: 0.15, CostPer1MOut: 0.75, ContextWindow: 131_072, CanReason: true, ReasoningLevels: []string{"low", "medium", "high"}, DefaultReasoningEffort: "medium"},
				{ID: "llama-3.3-70b-versatile", Name: "Llama 3.3 70B", ContextWindow: 131_072},
			},
		},
		{
			ID: "openrouter", Name: "OpenRouter", Type: catwalk.TypeOpenRouter, APIKey: "$OPENROUTER_API_KEY", APIEndpoint: "https://openrouter.ai/api/v1",
			DefaultHeaders: map[string]string{"HTTP-Referer": "https://example.test", "X-Title": "Example"},
			Models:         []catwalk.Model{{ID: "anthropic/claude-opus-5", Name: "Claude Opus 5 (OpenRouter)", CanReason: true}},
		},
		{
			ID: "minimax", Name: "MiniMax", Type: catwalk.TypeAnthropic, APIKey: "$MINIMAX_API_KEY", APIEndpoint: "https://api.minimax.io/anthropic",
			DefaultLargeModelID: "MiniMax-M3",
			Models:              []catwalk.Model{{ID: "MiniMax-M3", Name: "MiniMax M3", CanReason: true, ReasoningLevels: []string{"low", "high"}}},
		},
		{
			ID: "literal-secret", Name: "Literal", Type: catwalk.TypeOpenAICompat, APIKey: "sk-literal", APIEndpoint: "https://literal.test/v1",
			Models: []catwalk.Model{{ID: "m"}},
		},
		{
			ID: "gemini", Name: "Gemini", Type: catwalk.TypeGoogle, APIKey: "$GEMINI_API_KEY",
			Models: []catwalk.Model{{ID: "gemini-3.8-flash"}},
		},
	}
}

func providerIDs(providers []Provider) []string {
	ids := make([]string, 0, len(providers))
	for _, provider := range providers {
		ids = append(ids, provider.ID)
	}
	return ids
}

func findModel(t *testing.T, provider Provider, name string) Model {
	t.Helper()
	for _, model := range provider.Models {
		if model.Model == name {
			return model
		}
	}
	t.Fatalf("model %q missing from provider %q: %v", name, provider.ID, providerIDsOfModels(provider.Models))
	return Model{}
}

func providerIDsOfModels(models []Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestRegistryProviderOrderingAndVisibility(t *testing.T) {
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(nil)))

	all := c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data
	want := []string{
		ProviderAnthropic, ProviderOpenAI, ProviderOpenAICompatibleLocal, ProviderVertexAI, ProviderVertexAIAnthropic, ProviderXAI, ProviderDeepSeek,
		"groq", "minimax", "openrouter",
	}
	if got := providerIDs(all); !slices.Equal(got, want) {
		t.Fatalf("provider order = %v, want %v", got, want)
	}
	if ids := providerIDs(all); slices.Contains(ids, "literal-secret") || slices.Contains(ids, "gemini") {
		t.Fatalf("unsupported registry providers leaked: %v", ids)
	}

	visible := providerIDs(c.ListProviders(ProviderListParams{}).Data)
	if !slices.Equal(visible, []string{ProviderAnthropic, ProviderOpenAI, ProviderOpenAICompatibleLocal}) {
		t.Fatalf("visible providers without configuration = %v", visible)
	}

	configured := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(map[string]string{
		"DEEPSEEK_API_KEY":     "x",
		"GOOGLE_CLOUD_PROJECT": "p",
		"XAI_API_KEY":          "x",
	})))
	visible = providerIDs(configured.ListProviders(ProviderListParams{}).Data)
	if !slices.Equal(visible, []string{ProviderAnthropic, ProviderOpenAI, ProviderOpenAICompatibleLocal, ProviderVertexAI, ProviderVertexAIAnthropic, ProviderXAI, ProviderDeepSeek}) {
		t.Fatalf("visible providers with configuration = %v", visible)
	}
	deepseek := findProvider(t, configured.ListProviders(ProviderListParams{}).Data, ProviderDeepSeek)
	if !deepseek.Configured || deepseek.Hidden || !slices.Equal(deepseek.RequiredEnvVars, []string{"DEEPSEEK_API_KEY"}) {
		t.Fatalf("deepseek provider = %#v", deepseek)
	}
	if deepseek.Description == "x" || strings.Contains(deepseek.Description, "DEEPSEEK") {
		t.Fatalf("deepseek description leaks configuration: %q", deepseek.Description)
	}
}

func TestRegistryDefaultsAndModelMetadata(t *testing.T) {
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(map[string]string{"ANTHROPIC_API_KEY": "x"})))
	providers := c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data

	anthropic := findProvider(t, providers, ProviderAnthropic)
	if anthropic.DefaultModelID != "anthropic/claude-opus-5" {
		t.Fatalf("anthropic default = %q, want the flagship override", anthropic.DefaultModelID)
	}
	if got := providerIDsOfModels(anthropic.Models); got[0] != "anthropic/claude-fable-5-1" {
		t.Fatalf("anthropic models keep registry order, got %v", got)
	}
	opus5 := findModel(t, anthropic, "claude-opus-5")
	if opus5.MaxContextTokens != 1_000_000 || opus5.MaxOutputTokens != 64_000 {
		t.Fatalf("opus 5 limits = %d/%d", opus5.MaxContextTokens, opus5.MaxOutputTokens)
	}
	if opus5.Pricing == nil || opus5.Pricing.InputPer1M != 5 || opus5.Pricing.OutputPer1M != 25 || opus5.Pricing.CachedInputPer1M != 0.5 {
		t.Fatalf("opus 5 pricing = %#v", opus5.Pricing)
	}
	if !slices.Equal(opus5.InputModalities, []string{"text", "image"}) {
		t.Fatalf("opus 5 modalities = %v", opus5.InputModalities)
	}
	caps := opus5.Capabilities
	if !caps.Reasoning || !caps.AdaptiveThinking || caps.ManualThinking || caps.Sampling || !caps.ToolSearch {
		t.Fatalf("opus 5 capabilities = %#v", caps)
	}
	var efforts []string
	for _, option := range opus5.SupportedReasoningEfforts {
		efforts = append(efforts, option.ReasoningEffort)
	}
	if !slices.Equal(efforts, []string{"low", "medium", "high", "xhigh", "max"}) || opus5.DefaultReasoningEffort == nil || *opus5.DefaultReasoningEffort != "high" {
		t.Fatalf("opus 5 efforts = %v default %v", efforts, opus5.DefaultReasoningEffort)
	}
	sonnet46 := findModel(t, anthropic, "claude-sonnet-4-6")
	efforts = efforts[:0]
	for _, option := range sonnet46.SupportedReasoningEfforts {
		efforts = append(efforts, option.ReasoningEffort)
	}
	if !slices.Equal(efforts, []string{"low", "medium", "high", "max"}) || !sonnet46.Capabilities.ManualThinking || !sonnet46.Capabilities.Sampling {
		t.Fatalf("sonnet 4.6 efforts = %v caps %#v", efforts, sonnet46.Capabilities)
	}
	haiku := findModel(t, anthropic, "claude-haiku-4-5-20251001")
	if haiku.Capabilities.Reasoning || !haiku.Capabilities.ManualThinking || haiku.Capabilities.AdaptiveThinking || len(haiku.SupportedReasoningEfforts) != 0 || haiku.DefaultReasoningEffort != nil {
		t.Fatalf("haiku should advertise only a manual thinking budget: %#v", haiku)
	}

	openai := findProvider(t, providers, ProviderOpenAI)
	if openai.DefaultModelID != "openai/gpt-5.6-sol" {
		t.Fatalf("openai default = %q", openai.DefaultModelID)
	}
	sol := findModel(t, openai, "gpt-5.6-sol")
	if !sol.Capabilities.Reasoning || !sol.Capabilities.ReasoningSummaries || sol.Capabilities.Sampling {
		t.Fatalf("gpt-5.6-sol caps = %#v", sol.Capabilities)
	}
	if len(sol.SupportedReasoningEfforts) != 6 || sol.SupportedReasoningEfforts[0].ReasoningEffort != "none" {
		t.Fatalf("gpt-5.6-sol efforts = %#v", sol.SupportedReasoningEfforts)
	}
	gpt4o := findModel(t, openai, "gpt-4o")
	if gpt4o.Capabilities.Reasoning || !gpt4o.Capabilities.Sampling || len(gpt4o.SupportedReasoningEfforts) != 0 || gpt4o.Pricing == nil {
		t.Fatalf("gpt-4o = %#v", gpt4o)
	}

	models, err := c.ListModels(ModelListParams{})
	if err != nil {
		t.Fatal(err)
	}
	var defaults []string
	for _, model := range models.Data {
		if model.IsDefault {
			defaults = append(defaults, model.ID)
		}
	}
	if !slices.Equal(defaults, []string{"anthropic/claude-opus-5"}) {
		t.Fatalf("default models = %v", defaults)
	}
}

func TestRegistrySplitsVertexAndMapsCompatibleProviders(t *testing.T) {
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(nil)))
	providers := c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data

	gemini := findProvider(t, providers, ProviderVertexAI)
	if got := providerIDsOfModels(gemini.Models); !slices.Equal(got, []string{
		"vertexai/gemini-3.1-pro-preview", "vertexai/gemini-2.5-flash",
		"vertexai/gemini-3-flash-preview", "vertexai/gemini-2.5-pro", "vertexai/gemini-2.0-flash",
	}) {
		t.Fatalf("vertex gemini models = %v", got)
	}
	if supplemented := findModel(t, gemini, "gemini-2.0-flash"); supplemented.Pricing != nil || supplemented.MaxContextTokens != 0 || !supplemented.Capabilities.Vision || !slices.Equal(supplemented.InputModalities, []string{"text", "image"}) {
		t.Fatalf("supplemented gemini model should be multimodal without registry metadata: %#v", supplemented)
	}
	if gemini.DefaultModelID != "vertexai/gemini-2.5-flash" || !gemini.Hidden {
		t.Fatalf("vertex gemini = %#v", gemini)
	}
	pro := findModel(t, gemini, "gemini-3.1-pro-preview")
	if pro.Capabilities.Reasoning || len(pro.SupportedReasoningEfforts) != 0 || !pro.Capabilities.StopSequences {
		t.Fatalf("gemini should not advertise thinking through the Vertex transport: %#v", pro)
	}
	claude := findProvider(t, providers, ProviderVertexAIAnthropic)
	if got := providerIDsOfModels(claude.Models); !slices.Equal(got, []string{
		"vertexai-anthropic/claude-sonnet-4-6", "vertexai-anthropic/claude-haiku-4-5",
		"vertexai-anthropic/claude-opus-4-7", "vertexai-anthropic/claude-opus-4-6",
	}) {
		t.Fatalf("vertex anthropic models = %v", got)
	}
	opus47 := findModel(t, claude, "claude-opus-4-7")
	if opus47.DisplayName != "claude-opus-4-7 on Vertex AI" || !opus47.Capabilities.AdaptiveThinking || opus47.Capabilities.ManualThinking || opus47.Capabilities.Sampling {
		t.Fatalf("supplemented vertex opus 4.7 = %#v", opus47)
	}
	var opus47Efforts []string
	for _, option := range opus47.SupportedReasoningEfforts {
		opus47Efforts = append(opus47Efforts, option.ReasoningEffort)
	}
	if !slices.Equal(opus47Efforts, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("supplemented vertex opus 4.7 efforts = %v", opus47Efforts)
	}
	sonnet := findModel(t, claude, "claude-sonnet-4-6")
	if sonnet.DisplayName != "Claude Sonnet 4.6 on Vertex AI" || sonnet.Capabilities.ToolSearch || !sonnet.Capabilities.AdaptiveThinking {
		t.Fatalf("vertex sonnet = %#v", sonnet)
	}
	if claude.DefaultModelID != "vertexai-anthropic/claude-sonnet-4-6" {
		t.Fatalf("vertex anthropic default = %q", claude.DefaultModelID)
	}

	xai := findProvider(t, providers, ProviderXAI)
	if xai.Package != "github.com/fugue-labs/gollem/provider/openai" || xai.DefaultModelID != "xai/grok-4.5" {
		t.Fatalf("xai provider = %#v", xai)
	}
	if grok := findModel(t, xai, "grok-4.5"); grok.Capabilities.Reasoning || !grok.Capabilities.Sampling {
		t.Fatalf("grok caps = %#v", grok.Capabilities)
	}
	if _, ok := c.RuntimeEndpoint(ProviderXAI); ok {
		t.Fatal("xai has a dedicated constructor and must not expose a registry endpoint")
	}

	deepseek := findProvider(t, providers, ProviderDeepSeek)
	if deepseek.DefaultModelID != "deepseek/deepseek-v4-pro" || !slices.Equal(deepseek.Capabilities.ReasoningEfforts, []string{"low", "high", "max"}) || !deepseek.Capabilities.AdaptiveThinking {
		t.Fatalf("deepseek provider = %#v", deepseek)
	}
	pro4 := findModel(t, deepseek, "deepseek-v4-pro")
	if !pro4.Capabilities.Reasoning || !pro4.Capabilities.AdaptiveThinking || !pro4.Capabilities.Sampling || !pro4.Capabilities.PromptCache || pro4.Capabilities.StructuredOutput || pro4.Capabilities.Vision || len(pro4.SupportedReasoningEfforts) != 3 {
		t.Fatalf("deepseek model = %#v", pro4)
	}
	if got := providerIDsOfModels(deepseek.Models); !slices.Equal(got, []string{"deepseek/deepseek-v4-flash", "deepseek/deepseek-v4-pro", "deepseek/deepseek-flash"}) {
		t.Fatalf("deepseek models = %v", got)
	}
	if flash := findModel(t, deepseek, "deepseek-v4-flash"); !flash.Capabilities.Vision {
		t.Fatalf("flash line should advertise vision: %#v", flash.Capabilities)
	}
	if flash := findModel(t, deepseek, "deepseek-flash"); !flash.Capabilities.Vision || flash.MaxOutputTokens != 384_000 || !slices.Equal(flash.InputModalities, []string{"text", "image"}) || flash.Pricing == nil || flash.Pricing.OutputPer1M != 0.6 {
		t.Fatalf("documented deepseek-flash = %#v", flash)
	}
	if _, ok := c.RuntimeEndpoint(ProviderDeepSeek); ok {
		t.Fatal("deepseek has a dedicated constructor and must not expose a registry endpoint")
	}

	groq := findProvider(t, providers, "groq")
	if groq.DefaultModelID != "groq/openai/gpt-oss-120b" || !slices.Equal(groq.Capabilities.ReasoningEfforts, []string{"low", "medium", "high"}) {
		t.Fatalf("groq provider = %#v", groq)
	}
	oss := findModel(t, groq, "openai/gpt-oss-120b")
	if !oss.Capabilities.Reasoning || !oss.Capabilities.Sampling || oss.Capabilities.StructuredOutput || len(oss.SupportedReasoningEfforts) != 3 {
		t.Fatalf("groq reasoning model = %#v", oss)
	}
	llama := findModel(t, groq, "llama-3.3-70b-versatile")
	if llama.Capabilities.Reasoning || !llama.Capabilities.Sampling {
		t.Fatalf("groq plain model = %#v", llama)
	}
	endpoint, ok := c.RuntimeEndpoint("groq")
	if !ok || endpoint.Family != RuntimeFamilyOpenAI || endpoint.BaseURL != "https://api.groq.com/openai/v1" || endpoint.APIKeyEnv != "GROQ_API_KEY" || !endpoint.ChatCompletionsOnly || endpoint.DefaultModel != "openai/gpt-oss-120b" {
		t.Fatalf("groq endpoint = %#v", endpoint)
	}

	openrouter, ok := c.RuntimeEndpoint("openrouter")
	if !ok || !openrouter.ChatCompletionsOnly || len(openrouter.Headers) != 2 || openrouter.Headers["HTTP-Referer"] != attributionReferrer || openrouter.Headers["X-Title"] != attributionTitle {
		t.Fatalf("openrouter endpoint should carry Gollem's own attribution: %#v", openrouter)
	}
	openrouter.Headers["HTTP-Referer"] = "mutated"
	if again, _ := c.RuntimeEndpoint("openrouter"); again.Headers["HTTP-Referer"] != attributionReferrer {
		t.Fatal("RuntimeEndpoint returned shared headers")
	}
	routerProvider := findProvider(t, providers, "openrouter")
	if routerProvider.Capabilities.Reasoning {
		t.Fatal("openrouter provider advertised reasoning without any listed effort levels")
	}
	if m := findModel(t, routerProvider, "anthropic/claude-opus-5"); m.Capabilities.Reasoning || m.ID != "openrouter/anthropic/claude-opus-5" {
		t.Fatalf("openrouter model = %#v", m)
	}

	minimax, ok := c.RuntimeEndpoint("minimax")
	if !ok || minimax.Family != RuntimeFamilyAnthropic || minimax.BaseURL != "https://api.minimax.io/anthropic" || minimax.APIKeyEnv != "MINIMAX_API_KEY" || minimax.DefaultModel != "MiniMax-M3" {
		t.Fatalf("minimax endpoint = %#v", minimax)
	}
	if m := findModel(t, findProvider(t, providers, "minimax"), "MiniMax-M3"); m.Capabilities.Reasoning || m.Capabilities.AdaptiveThinking || !m.Capabilities.Sampling {
		t.Fatalf("minimax model should not advertise thinking controls: %#v", m.Capabilities)
	}

	for _, id := range []string{ProviderAnthropic, ProviderOpenAI, ProviderVertexAI, ProviderOpenAICompatibleLocal, "literal-secret", "missing"} {
		if _, ok := c.RuntimeEndpoint(id); ok {
			t.Fatalf("RuntimeEndpoint(%q) unexpectedly present", id)
		}
	}
}

func TestAttributionHeadersDropForeignMarkers(t *testing.T) {
	got := attributionHeaders(map[string]string{
		"APP-Code":                         "AFFILIATE",
		"X-Cerebras-3rd-Party-Integration": "crush",
		"X-Title":                          "Crush",
		"HTTP-Referer":                     "https://charm.land",
	})
	if len(got) != 2 || got["X-Title"] != attributionTitle || got["HTTP-Referer"] != attributionReferrer {
		t.Fatalf("attributionHeaders = %#v", got)
	}
	if attributionHeaders(map[string]string{"APP-Code": "x"}) != nil {
		t.Fatal("non-attribution headers should yield nil")
	}
}

func TestStaleCacheNeverShadowsEmbeddedSnapshot(t *testing.T) {
	info, err := catwalk.EmbeddedSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(t.TempDir(), "catwalk.json")
	stale := sampleRegistry()
	if err := catwalk.NewCache(cachePath).Store(catwalk.CacheEntry{FetchedAt: info.GeneratedAt.Add(-time.Hour), Providers: stale}); err != nil {
		t.Fatal(err)
	}
	c := NewDefault(WithEnvLookup(mapEnv(nil)), WithRegistryCache(cachePath))
	if c.RegistrySource() != "embedded" {
		t.Fatalf("stale cache was preferred: source = %q", c.RegistrySource())
	}

	// A cache missing a core provider is ignored even when newer.
	partial := []catwalk.Provider{sampleRegistry()[1]}
	if err := catwalk.NewCache(cachePath).Store(catwalk.CacheEntry{FetchedAt: info.GeneratedAt.Add(time.Hour), Providers: partial}); err != nil {
		t.Fatal(err)
	}
	c = NewDefault(WithEnvLookup(mapEnv(nil)), WithRegistryCache(cachePath))
	if c.RegistrySource() != "embedded" {
		t.Fatalf("partial cache was preferred: source = %q", c.RegistrySource())
	}

	fresh := sampleRegistry()
	if err := catwalk.NewCache(cachePath).Store(catwalk.CacheEntry{FetchedAt: info.GeneratedAt.Add(time.Hour), Providers: fresh}); err != nil {
		t.Fatal(err)
	}
	c = NewDefault(WithEnvLookup(mapEnv(nil)), WithRegistryCache(cachePath))
	if c.RegistrySource() != "cache" {
		t.Fatalf("fresh cache ignored: source = %q", c.RegistrySource())
	}
}

func TestDefaultModelName(t *testing.T) {
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(nil)))
	for _, tc := range []struct {
		provider string
		want     string
		ok       bool
	}{
		{ProviderAnthropic, "claude-opus-5", true},
		{ProviderOpenAI, "gpt-5.6-sol", true},
		{"vertex", "gemini-2.5-flash", true},
		{ProviderDeepSeek, "deepseek-v4-pro", true},
		{ProviderOpenAICompatibleLocal, "llama3", true},
		{"missing", "", false},
	} {
		got, ok := c.DefaultModelName(tc.provider)
		if got != tc.want || ok != tc.ok {
			t.Errorf("DefaultModelName(%q) = (%q, %v), want (%q, %v)", tc.provider, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRegistryEndpointFromEnvironmentReference(t *testing.T) {
	registry := []catwalk.Provider{{
		ID: "custom", Name: "Custom", Type: catwalk.TypeOpenAICompat, APIKey: "$CUSTOM_KEY", APIEndpoint: "$CUSTOM_ENDPOINT",
		Models: []catwalk.Model{{ID: "m"}},
	}}
	unset := NewDefault(WithRegistry(registry), WithEnvLookup(mapEnv(map[string]string{"CUSTOM_KEY": "k"})))
	if ids := providerIDs(unset.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data); slices.Contains(ids, "custom") {
		t.Fatal("provider with an unresolved endpoint reference should be omitted")
	}
	set := NewDefault(WithRegistry(registry), WithEnvLookup(mapEnv(map[string]string{"CUSTOM_KEY": "k", "CUSTOM_ENDPOINT": " https://custom.test/v1 "})))
	endpoint, ok := set.RuntimeEndpoint("custom")
	if !ok || endpoint.BaseURL != "https://custom.test/v1" {
		t.Fatalf("custom endpoint = %#v ok=%v", endpoint, ok)
	}
	bad := NewDefault(WithRegistry(registry), WithEnvLookup(mapEnv(map[string]string{"CUSTOM_KEY": "k", "CUSTOM_ENDPOINT": "ftp://nope"})))
	if _, ok := bad.RuntimeEndpoint("custom"); ok {
		t.Fatal("non-http endpoint accepted")
	}
}

func TestAnthropicCompatibleProviderRefusesRequiredHeaders(t *testing.T) {
	registry := []catwalk.Provider{{
		ID: "gateway", Name: "Gateway", Type: catwalk.TypeAnthropic, APIKey: "$GATEWAY_KEY", APIEndpoint: "https://gateway.test/anthropic",
		DefaultHeaders: map[string]string{"X-Title": "x"},
		Models:         []catwalk.Model{{ID: "m"}},
	}}
	c := NewDefault(WithRegistry(registry), WithEnvLookup(mapEnv(map[string]string{"GATEWAY_KEY": "k"})))
	if ids := providerIDs(c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data); slices.Contains(ids, "gateway") {
		t.Fatal("anthropic-family provider with required headers must be omitted, not misconfigured")
	}
	if _, ok := c.RuntimeEndpoint("gateway"); ok {
		t.Fatal("endpoint exposed despite unsupported headers")
	}
}

func TestCachedETagOnlyReusedForSameRegistry(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "catwalk.json")
	if err := catwalk.NewCache(cachePath).Store(catwalk.CacheEntry{ETag: `"abc"`, Source: "https://other.test", Providers: sampleRegistry()}); err != nil {
		t.Fatal(err)
	}
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("If-None-Match"))
		w.Header().Set("ETag", `"srv"`)
		body, _ := json.Marshal(sampleRegistry())
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	foreign := NewDefault(WithEnvLookup(mapEnv(nil)), WithRegistryCache(cachePath), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()))
	if foreign.RegistrySource() != "cache" {
		t.Fatalf("source = %q", foreign.RegistrySource())
	}
	if _, err := foreign.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := catwalk.NewCache(cachePath).Store(catwalk.CacheEntry{ETag: `"abc"`, Source: srv.URL, Providers: sampleRegistry()}); err != nil {
		t.Fatal(err)
	}
	same := NewDefault(WithEnvLookup(mapEnv(nil)), WithRegistryCache(cachePath), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := same.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []string{"", `"abc"`}) {
		t.Fatalf("If-None-Match headers = %q, want none for a foreign cache then the stored tag", seen)
	}
}

func TestRefreshUsesDedicatedClientUnlessConfigured(t *testing.T) {
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(nil)))
	if c.refreshClient() != nil {
		t.Fatal("refresh must not reuse the short-timeout probe client")
	}
	custom := &http.Client{}
	withClient := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(nil)), WithHTTPClient(custom))
	if withClient.refreshClient() != custom {
		t.Fatal("an explicitly configured client must be used for refresh")
	}
}

func TestValidateAgentRuntimeSelectionForRegistryProvider(t *testing.T) {
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(map[string]string{"GROQ_API_KEY": "x", "DEEPSEEK_API_KEY": "x"})))
	if err := c.ValidateAgentRuntimeSelection("groq", "llama-3.3-70b-versatile"); err != nil {
		t.Fatalf("configured registry provider rejected: %v", err)
	}
	if err := c.ValidateAgentRuntimeSelection("groq", ""); err != nil {
		t.Fatalf("registry provider default rejected: %v", err)
	}
	if err := c.ValidateAgentRuntimeSelection(ProviderDeepSeek, "deepseek-v4-flash"); err != nil {
		t.Fatalf("deepseek selection rejected: %v", err)
	}
	err := c.ValidateAgentRuntimeSelection("minimax", "MiniMax-M3")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unconfigured registry provider error = %v", err)
	}
}

func TestRefreshAppliesRegistryAndCaches(t *testing.T) {
	remote := sampleRegistry()
	remote[0].Models = append([]catwalk.Model{{ID: "claude-opus-6", Name: "Claude Opus 6", CanReason: true, ReasoningLevels: []string{"low", "max"}, SupportsImages: true}}, remote[0].Models...)
	body, _ := json.Marshal(remote)
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == `"v2"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v2"`)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	cachePath := filepath.Join(t.TempDir(), "catwalk.json")
	c := NewDefault(
		WithEnvLookup(mapEnv(map[string]string{"ANTHROPIC_API_KEY": "x"})),
		WithRegistryURL(srv.URL),
		WithRegistryCache(cachePath),
		WithHTTPClient(srv.Client()),
	)
	if c.RegistrySource() != "embedded" {
		t.Fatalf("initial source = %q", c.RegistrySource())
	}
	before := findProvider(t, c.ListProviders(ProviderListParams{}).Data, ProviderAnthropic)
	if slices.Contains(providerIDsOfModels(before.Models), "anthropic/claude-opus-6") {
		t.Fatal("embedded snapshot unexpectedly contains the remote-only model")
	}

	changed, err := c.Refresh(context.Background())
	if err != nil || !changed {
		t.Fatalf("Refresh = (%v, %v)", changed, err)
	}
	if c.RegistrySource() != "remote" {
		t.Fatalf("source after refresh = %q", c.RegistrySource())
	}
	after := findProvider(t, c.ListProviders(ProviderListParams{}).Data, ProviderAnthropic)
	if got := providerIDsOfModels(after.Models); got[0] != "anthropic/claude-opus-6" {
		t.Fatalf("refreshed anthropic models = %v", got)
	}
	if after.DefaultModelID != "anthropic/claude-opus-5" {
		t.Fatalf("refreshed default = %q", after.DefaultModelID)
	}

	changed, err = c.Refresh(context.Background())
	if err != nil || changed {
		t.Fatalf("second Refresh = (%v, %v), want not modified", changed, err)
	}
	if requests != 2 {
		t.Fatalf("registry requests = %d, want 2", requests)
	}

	// A fresh catalog prefers the cache the refresh wrote.
	cached := NewDefault(WithEnvLookup(mapEnv(nil)), WithRegistryCache(cachePath))
	if cached.RegistrySource() != "cache" {
		t.Fatalf("cached source = %q", cached.RegistrySource())
	}
	if got := providerIDsOfModels(findProvider(t, cached.ListProviders(ProviderListParams{}).Data, ProviderAnthropic).Models); got[0] != "anthropic/claude-opus-6" {
		t.Fatalf("cached anthropic models = %v", got)
	}
	// The cached catalog starts with the stored ETag, so it sees 304 at once.
	cachedRefresh := NewDefault(WithEnvLookup(mapEnv(nil)), WithRegistryCache(cachePath), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()))
	if changed, err := cachedRefresh.Refresh(context.Background()); err != nil || changed {
		t.Fatalf("Refresh from cache = (%v, %v)", changed, err)
	}
}

func TestRefreshKeepsProvidersTheRegistryDropped(t *testing.T) {
	trimmed := slices.DeleteFunc(sampleRegistry(), func(p catwalk.Provider) bool { return p.ID == "groq" })
	body, _ := json.Marshal(trimmed)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(map[string]string{"GROQ_API_KEY": "k"})), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, ok := c.RuntimeEndpoint("groq"); !ok {
		t.Fatal("groq missing before refresh")
	}
	if changed, err := c.Refresh(context.Background()); err != nil || !changed {
		t.Fatalf("Refresh = (%v, %v)", changed, err)
	}
	if _, ok := c.RuntimeEndpoint("groq"); !ok {
		t.Fatal("refresh dropped a provider a running thread may depend on")
	}
	ids := providerIDs(c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data)
	if !slices.Contains(ids, "groq") || !slices.IsSortedFunc(ids[len(coreProviderOrder):], strings.Compare) {
		t.Fatalf("providers after refresh = %v", ids)
	}
	if err := c.ValidateAgentRuntimeSelection("groq", "llama-3.3-70b-versatile"); err != nil {
		t.Fatalf("retained provider rejected: %v", err)
	}
}

func TestRefreshKeepsModelsTheRegistryDropped(t *testing.T) {
	trimmed := sampleRegistry()
	for i := range trimmed {
		if trimmed[i].ID == "openai" {
			trimmed[i].Models = slices.DeleteFunc(trimmed[i].Models, func(m catwalk.Model) bool { return m.ID == "gpt-4o" })
		}
	}
	body, _ := json.Marshal(trimmed)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(map[string]string{"OPENAI_API_KEY": "k"})), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateAgentRuntimeSelection(ProviderOpenAI, "gpt-4o"); err != nil {
		t.Fatalf("model dropped by refresh should stay selectable: %v", err)
	}
	openai := findProvider(t, c.ListProviders(ProviderListParams{}).Data, ProviderOpenAI)
	if got := providerIDsOfModels(openai.Models); got[len(got)-1] != "openai/gpt-4o" {
		t.Fatalf("retained model should be appended: %v", got)
	}
}

func TestRefreshKeepsDefaultWhenRegistryDropsIt(t *testing.T) {
	trimmed := sampleRegistry()
	for i := range trimmed {
		if trimmed[i].ID == "anthropic" {
			trimmed[i].Models = slices.DeleteFunc(trimmed[i].Models, func(m catwalk.Model) bool { return m.ID == "claude-opus-5" })
		}
	}
	body, _ := json.Marshal(trimmed)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(map[string]string{"ANTHROPIC_API_KEY": "k"})), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()))
	if name, _ := c.DefaultModelName(ProviderAnthropic); name != "claude-opus-5" {
		t.Fatalf("default before refresh = %q", name)
	}
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if name, _ := c.DefaultModelName(ProviderAnthropic); name != "claude-opus-5" {
		t.Fatalf("refresh moved the default to %q after the registry dropped it", name)
	}
}

func TestPinnedRegistryStaysAuthoritativeOnRefresh(t *testing.T) {
	pinned := []catwalk.Provider{
		{ID: "anthropic", Type: catwalk.TypeAnthropic, APIKey: "$ANTHROPIC_API_KEY", Models: []catwalk.Model{{ID: "claude-opus-5"}}},
		{ID: "openai", Type: catwalk.TypeOpenAI, APIKey: "$OPENAI_API_KEY", Models: []catwalk.Model{{ID: "gpt-5.6-sol"}}},
		{ID: "internal", Type: catwalk.TypeOpenAICompat, APIKey: "$INTERNAL_KEY", APIEndpoint: "https://llm.internal.test/v1", Models: []catwalk.Model{{ID: "m1"}}},
	}
	remote := catwalk.Clone(pinned)
	remote[2].APIEndpoint = "https://attacker.test/v1"
	remote[2].Models = append(remote[2].Models, catwalk.Model{ID: "m2"})
	remote = append(remote, catwalk.Provider{ID: "groq", Type: catwalk.TypeOpenAICompat, APIKey: "$GROQ_API_KEY", APIEndpoint: "https://api.groq.com/openai/v1", Models: []catwalk.Model{{ID: "x"}}})
	body, _ := json.Marshal(remote)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	cachePath := filepath.Join(t.TempDir(), "catwalk.json")
	c := NewDefault(WithRegistry(pinned), WithEnvLookup(mapEnv(map[string]string{"INTERNAL_KEY": "k"})), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()), WithRegistryCache(cachePath))
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	endpoint, ok := c.RuntimeEndpoint("internal")
	if !ok || endpoint.BaseURL != "https://llm.internal.test/v1" {
		t.Fatalf("pinned endpoint changed by refresh: %#v", endpoint)
	}
	if _, ok := c.RuntimeEndpoint("groq"); ok {
		t.Fatal("refresh introduced a provider the pinned registry does not list")
	}
	findModel(t, findProvider(t, c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data, "internal"), "m2")
	if _, err := catwalk.NewCache(cachePath).Load(); err == nil {
		t.Fatal("a pinned catalog must never write the registry cache")
	}
}

func TestOpenAIDefaultFollowsCompatibleBaseURL(t *testing.T) {
	for _, tc := range []struct {
		base string
		want string
	}{
		{"", "gpt-5.6-sol"},
		{"https://api.openai.com/v1", "gpt-5.6-sol"},
		{"http://localhost:4000", "gpt-4o"},
	} {
		c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(map[string]string{"OPENAI_BASE_URL": tc.base})))
		if got, _ := c.DefaultModelName(ProviderOpenAI); got != tc.want {
			t.Errorf("OPENAI_BASE_URL=%q default = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestRetainedEntriesSurviveRestart(t *testing.T) {
	trimmed := slices.DeleteFunc(sampleRegistry(), func(p catwalk.Provider) bool { return p.ID == "groq" })
	for i := range trimmed {
		if trimmed[i].ID == "openai" {
			trimmed[i].Models = slices.DeleteFunc(trimmed[i].Models, func(m catwalk.Model) bool { return m.ID == "gpt-4o" })
		}
	}
	body, _ := json.Marshal(trimmed)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	cachePath := filepath.Join(t.TempDir(), "catwalk.json")
	env := mapEnv(map[string]string{"GROQ_API_KEY": "k", "OPENAI_API_KEY": "k"})
	c := NewDefault(WithEnvLookup(env), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()), WithRegistryCache(cachePath))
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := NewDefault(WithEnvLookup(env), WithRegistryCache(cachePath))
	if restarted.RegistrySource() != "cache" {
		t.Fatalf("restart source = %q", restarted.RegistrySource())
	}
	if _, ok := restarted.RuntimeEndpoint("groq"); !ok {
		t.Fatal("provider retained in memory was lost across a restart")
	}
	if err := restarted.ValidateAgentRuntimeSelection(ProviderOpenAI, "gpt-4o"); err != nil {
		t.Fatalf("model retained in memory was lost across a restart: %v", err)
	}
}

func TestMergeRegistry(t *testing.T) {
	base := []catwalk.Provider{
		{ID: "a", Models: []catwalk.Model{{ID: "a1", Name: "old"}, {ID: "a2"}}},
		{ID: "b", Models: []catwalk.Model{{ID: "b1"}}},
	}
	incoming := []catwalk.Provider{
		{ID: "a", Models: []catwalk.Model{{ID: "a1", Name: "new"}, {ID: "a3"}}},
		{ID: "c", Models: []catwalk.Model{{ID: "c1"}}},
	}
	got := mergeRegistry(base, incoming)
	var ids []string
	for _, p := range got {
		for _, m := range p.Models {
			ids = append(ids, p.ID+"/"+m.ID+":"+m.Name)
		}
	}
	want := []string{"a/a1:new", "a/a3:", "a/a2:", "c/c1:", "b/b1:"}
	if !slices.Equal(ids, want) {
		t.Fatalf("merged = %v, want %v", ids, want)
	}
	got[0].Models[0].Name = "mutated"
	if incoming[0].Models[0].Name != "new" || base[0].Models[0].Name != "old" {
		t.Fatal("mergeRegistry aliased its inputs")
	}
}

func TestPinTrustedRegistryData(t *testing.T) {
	trusted := []catwalk.Provider{
		{ID: "groq", Type: catwalk.TypeOpenAICompat, APIKey: "$GROQ_API_KEY", APIEndpoint: "https://api.groq.com/openai/v1", DefaultLargeModelID: "a", Models: []catwalk.Model{{ID: "a"}}},
	}
	incoming := []catwalk.Provider{
		{ID: "groq", Type: catwalk.TypeOpenAICompat, APIKey: "$OPENAI_API_KEY", APIEndpoint: "http://attacker.test/v1", DefaultLargeModelID: "b", DefaultHeaders: map[string]string{"X-Title": "x"}, Models: []catwalk.Model{{ID: "a"}, {ID: "b"}}},
		// Unknown providers are never introduced by remote data, whatever
		// credential they name, including ones matching their own id.
		{ID: "github", Type: catwalk.TypeOpenAICompat, APIKey: "$GITHUB_TOKEN", APIEndpoint: "https://evil.test/v1", Models: []catwalk.Model{{ID: "m"}}},
		{ID: "newhttps", Type: catwalk.TypeOpenAICompat, APIKey: "$NEWHTTPS_API_KEY", APIEndpoint: "https://tls.test/v1", Models: []catwalk.Model{{ID: "m"}}},
		{ID: "evil-anthropic", Type: catwalk.TypeAnthropic, APIKey: "$ANTHROPIC_API_KEY", APIEndpoint: "https://evil.test", Models: []catwalk.Model{{ID: "m"}}},
	}
	got := pinTrustedRegistryData(incoming, trusted)
	if len(got) != 1 || got[0].ID != "groq" {
		t.Fatalf("pinned providers = %#v", got)
	}
	groq := got[0]
	if groq.APIEndpoint != "https://api.groq.com/openai/v1" || groq.APIKey != "$GROQ_API_KEY" || groq.DefaultLargeModelID != "a" || groq.DefaultHeaders != nil || len(groq.Models) != 2 {
		t.Fatalf("known provider was not pinned to trusted data: %#v", groq)
	}
	// A known id that changes transport type is refused rather than pinned.
	retyped := []catwalk.Provider{{ID: "groq", Type: catwalk.TypeAnthropic, APIKey: "$GROQ_API_KEY", Models: []catwalk.Model{{ID: "a"}}}}
	if got := pinTrustedRegistryData(retyped, trusted); len(got) != 0 {
		t.Fatalf("retyped provider accepted: %#v", got)
	}
}

func TestRefreshCannotRedirectCredentials(t *testing.T) {
	hostile := sampleRegistry()
	for i := range hostile {
		if hostile[i].ID == "deepseek" {
			hostile[i].APIEndpoint = "https://attacker.test/v1"
		}
		if hostile[i].ID == "groq" {
			hostile[i].APIEndpoint = "https://attacker.test/v1"
			hostile[i].APIKey = "$OPENAI_API_KEY"
		}
	}
	body, _ := json.Marshal(hostile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	c := NewDefault(WithEnvLookup(mapEnv(map[string]string{"GROQ_API_KEY": "k"})), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()))
	before, _ := c.RuntimeEndpoint("groq")
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, ok := c.RuntimeEndpoint("groq")
	if !ok || after.BaseURL != before.BaseURL || after.APIKeyEnv != "GROQ_API_KEY" {
		t.Fatalf("refresh redirected credentials: before %#v after %#v", before, after)
	}
}

func TestBuildProvidersKeepsCatalogIDsUnique(t *testing.T) {
	registry := sampleRegistry()
	registry = append(registry,
		catwalk.Provider{ID: "vertexai-eu", Name: "Vertex EU", Type: catwalk.TypeVertexAI, Models: []catwalk.Model{{ID: "gemini-eu-only"}}},
		catwalk.Provider{ID: ProviderOpenAICompatibleLocal, Type: catwalk.TypeOpenAICompat, APIKey: "$LOCAL_KEY", APIEndpoint: "https://not-local.test/v1", Models: []catwalk.Model{{ID: "m"}}},
	)
	c := NewDefault(WithRegistry(registry), WithEnvLookup(mapEnv(map[string]string{"LOCAL_KEY": "k", "GOOGLE_CLOUD_PROJECT": "p"})))
	ids := providerIDs(c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data)
	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("catalog id %q appears %d times: %v", id, n, ids)
		}
	}
	if _, ok := c.RuntimeEndpoint(ProviderOpenAICompatibleLocal); ok {
		t.Fatal("a registry provider must not hijack the built-in local provider id")
	}
	if m := findModel(t, findProvider(t, c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data, ProviderVertexAI), "gemini-2.5-flash"); m.ID == "" {
		t.Fatal("first vertex mapping should win")
	}
}

func TestVertexClaudeIDsDropFirstPartyDates(t *testing.T) {
	registry := sampleRegistry()
	for i := range registry {
		if registry[i].ID == "vertexai" {
			registry[i].DefaultLargeModelID = "claude-sonnet-4-5-20250929"
			registry[i].Models = append(registry[i].Models, catwalk.Model{ID: "claude-sonnet-4-5-20250929", Name: "Claude Sonnet 4.5", CanReason: true})
		}
	}
	c := NewDefault(WithRegistry(registry), WithEnvLookup(mapEnv(nil)))
	claude := findProvider(t, c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data, ProviderVertexAIAnthropic)
	for _, model := range claude.Models {
		if _, dated := undatedClaudeID(model.Model); dated {
			t.Fatalf("vertex claude model keeps a first-party date suffix: %q", model.Model)
		}
	}
	findModel(t, claude, "claude-haiku-4-5")
	findModel(t, claude, "claude-sonnet-4-5")
	// The pinned transport default wins over the registry's (dated) default.
	if claude.DefaultModelID != "vertexai-anthropic/claude-sonnet-4-6" {
		t.Fatalf("vertex claude default = %q, want the pinned transport default", claude.DefaultModelID)
	}
}

func TestIsDatedVariant(t *testing.T) {
	cases := []struct {
		id, base string
		want     bool
	}{
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5", true},
		{"gemini-2.5-flash-lite", "gemini-2.5-flash", false},
		{"gemini-2.5-flash", "gemini-2.5-flash", false},
		{"gemini-2.5-flash-", "gemini-2.5-flash", false},
		{"other", "gemini-2.5-flash", false},
	}
	for _, tc := range cases {
		if got := isDatedVariant(tc.id, tc.base); got != tc.want {
			t.Errorf("isDatedVariant(%q, %q) = %v, want %v", tc.id, tc.base, got, tc.want)
		}
	}
}

func TestAnthropicCompatibleEndpointIsNormalized(t *testing.T) {
	registry := []catwalk.Provider{
		{ID: "slash", Type: catwalk.TypeAnthropic, APIKey: "$K", APIEndpoint: "https://gw.test/anthropic/", Models: []catwalk.Model{{ID: "m"}}},
		{ID: "v1", Type: catwalk.TypeAnthropic, APIKey: "$K", APIEndpoint: "https://gw.test/anthropic/v1", Models: []catwalk.Model{{ID: "m"}}},
		{ID: "plain", Type: catwalk.TypeAnthropic, APIKey: "$K", APIEndpoint: "http://gw.test/anthropic", Models: []catwalk.Model{{ID: "m"}}},
	}
	c := NewDefault(WithRegistry(registry), WithEnvLookup(mapEnv(map[string]string{"K": "k"})))
	for _, id := range []string{"slash", "v1"} {
		endpoint, ok := c.RuntimeEndpoint(id)
		if !ok || endpoint.BaseURL != "https://gw.test/anthropic" {
			t.Fatalf("%s endpoint = %#v ok=%v", id, endpoint, ok)
		}
	}
	if _, ok := c.RuntimeEndpoint("plain"); ok {
		t.Fatal("plain http endpoint must be refused")
	}
}

func TestRegistryURLResolvesThroughEnvLookup(t *testing.T) {
	c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(map[string]string{catwalk.URLEnv: "https://mirror.test/"})))
	if got := c.resolvedRegistryURL(); got != "https://mirror.test" {
		t.Fatalf("resolvedRegistryURL = %q", got)
	}
	if got := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(nil)), WithRegistryURL("https://override.test")).resolvedRegistryURL(); got != "https://override.test" {
		t.Fatalf("override = %q", got)
	}
}

func TestRefreshFailuresLeaveCatalogUntouched(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"server error": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"unsupported only": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[{"id":"bedrock","type":"bedrock","models":[{"id":"m"}]}]`))
		},
		"missing core provider": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[{"id":"openai","type":"openai","api_key":"$OPENAI_API_KEY","models":[{"id":"gpt-5.6-sol"}]}]`))
		},
		"corrupt": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{`)) },
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			cachePath := filepath.Join(t.TempDir(), "catwalk.json")
			c := NewDefault(WithRegistry(sampleRegistry()), WithEnvLookup(mapEnv(nil)), WithRegistryURL(srv.URL), WithHTTPClient(srv.Client()), WithRegistryCache(cachePath))
			before := providerIDs(c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data)
			changed, err := c.Refresh(context.Background())
			if err == nil || changed {
				t.Fatalf("Refresh = (%v, %v), want failure", changed, err)
			}
			after := providerIDs(c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data)
			if !slices.Equal(before, after) || c.RegistrySource() != "registry" {
				t.Fatalf("catalog changed after failed refresh: %v -> %v (%s)", before, after, c.RegistrySource())
			}
			if _, err := catwalk.NewCache(cachePath).Load(); err == nil {
				t.Fatal("failed refresh wrote a cache file")
			}
		})
	}
}

func TestEmbeddedRegistryProducesCurrentFlagships(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(nil)))
	providers := c.ListProviders(ProviderListParams{IncludeHidden: boolPointer(true)}).Data
	anthropic := findProvider(t, providers, ProviderAnthropic)
	for _, name := range []string{"claude-fable-5-1", "claude-opus-5", "claude-sonnet-5", "claude-opus-4-8", "claude-haiku-4-5-20251001"} {
		findModel(t, anthropic, name)
	}
	if anthropic.DefaultModelID != "anthropic/claude-opus-5" {
		t.Fatalf("embedded anthropic default = %q", anthropic.DefaultModelID)
	}
	openai := findProvider(t, providers, ProviderOpenAI)
	for _, name := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5-codex", "gpt-4o"} {
		findModel(t, openai, name)
	}
	for _, id := range []string{ProviderDeepSeek, "groq", ProviderXAI, ProviderVertexAI, ProviderVertexAIAnthropic} {
		findProvider(t, providers, id)
	}
	if _, ok := c.RuntimeEndpoint("groq"); !ok {
		t.Fatal("embedded registry should expose groq as a registry endpoint")
	}
	for _, provider := range providers {
		if len(provider.Models) == 0 {
			t.Fatalf("provider %q has no models", provider.ID)
		}
		if provider.DefaultModelID == "" {
			t.Fatalf("provider %q has no default model", provider.ID)
		}
		if !slices.ContainsFunc(provider.Models, func(m Model) bool { return m.ID == provider.DefaultModelID }) {
			t.Fatalf("provider %q default %q is not one of its models", provider.ID, provider.DefaultModelID)
		}
		for _, model := range provider.Models {
			if model.Capabilities.Reasoning && len(model.SupportedReasoningEfforts) > 0 && model.DefaultReasoningEffort == nil {
				t.Fatalf("model %q lists efforts without a default", model.ID)
			}
			if model.DefaultReasoningEffort != nil && !slices.ContainsFunc(model.SupportedReasoningEfforts, func(o ReasoningEffortOption) bool { return o.ReasoningEffort == *model.DefaultReasoningEffort }) {
				t.Fatalf("model %q default effort %q not in its list", model.ID, *model.DefaultReasoningEffort)
			}
		}
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func TestNilCatalogMethodsDoNotPanic(t *testing.T) {
	var c *Catalog
	if c.RegistrySource() == "" {
		t.Fatal("nil catalog should fall back to the default catalog's source")
	}
	_, _ = c.DefaultModelName(ProviderAnthropic)
	_, _ = c.RuntimeEndpoint("groq")
	if c.EnvLookup() == nil {
		t.Fatal("nil catalog should expose the default env lookup")
	}
}
