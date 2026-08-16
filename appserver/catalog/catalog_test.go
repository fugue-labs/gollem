package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	openaiprovider "github.com/fugue-labs/gollem/provider/openai"
	vertexprovider "github.com/fugue-labs/gollem/provider/vertexai"
)

func TestProviderListReportsConfigurationWithoutSecretValues(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(map[string]string{
		"ANTHROPIC_API_KEY": "secret-value",
	})))

	resp := c.ListProviders(ProviderListParams{})
	if len(resp.Data) < 2 {
		t.Fatalf("provider/list returned %d providers", len(resp.Data))
	}

	openai := findProvider(t, resp.Data, ProviderOpenAI)
	if openai.Configured {
		t.Fatal("openai provider reported configured without OPENAI_API_KEY")
	}
	anthropic := findProvider(t, resp.Data, ProviderAnthropic)
	if !anthropic.Configured {
		t.Fatal("anthropic provider did not report configured with ANTHROPIC_API_KEY")
	}
	if len(anthropic.Models) == 0 {
		t.Fatal("anthropic provider did not include model metadata")
	}
	for _, provider := range resp.Data {
		if provider.Description == "secret-value" || provider.Name == "secret-value" {
			t.Fatalf("provider leaked env value: %#v", provider)
		}
	}
}

func TestModelListPaginationFilteringAndDefault(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(map[string]string{
		"ANTHROPIC_API_KEY": "set",
	})))
	limit := uint32(2)
	first, err := c.ListModels(ModelListParams{ProviderID: ProviderAnthropic, Limit: &limit})
	if err != nil {
		t.Fatalf("ListModels page 1: %v", err)
	}
	if len(first.Data) != 2 {
		t.Fatalf("page 1 len = %d, want 2", len(first.Data))
	}
	if first.NextCursor == nil {
		t.Fatal("page 1 missing next cursor")
	}
	if !first.Data[0].IsDefault {
		t.Fatalf("configured provider default not marked on first anthropic model: %#v", first.Data[0])
	}

	second, err := c.ListModels(ModelListParams{ProviderID: ProviderAnthropic, Limit: &limit, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("ListModels page 2: %v", err)
	}
	if len(second.Data) == 0 {
		t.Fatal("page 2 returned no models")
	}
	for _, model := range append(first.Data, second.Data...) {
		if model.ProviderID != ProviderAnthropic {
			t.Fatalf("provider filter returned %#v", model)
		}
		if model.Hidden {
			t.Fatalf("hidden model returned without includeHidden: %#v", model)
		}
	}

	badCursor := "not-a-number"
	if _, err := c.ListModels(ModelListParams{Cursor: &badCursor}); err == nil {
		t.Fatal("invalid cursor did not fail")
	}
}

func TestProviderCapabilities(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(nil)))

	caps, err := c.ProviderCapabilities(ProviderOpenAI)
	if err != nil {
		t.Fatalf("ProviderCapabilities(openai): %v", err)
	}
	if !caps.NamespaceTools || !caps.ToolCalls || !caps.StructuredOutput || !caps.Vision || !caps.Streaming {
		t.Fatalf("openai capabilities missing expected feature: %#v", caps)
	}
	if caps.Configured {
		t.Fatal("openai capabilities reported configured without env")
	}
	if caps.ToolSearch {
		t.Fatal("openai catalog advertised tool search without a catalog-listed proven model")
	}
	openAI := findProvider(t, c.ListProviders(ProviderListParams{}).Data, ProviderOpenAI)
	for _, model := range openAI.Models {
		if model.Capabilities.ToolSearch {
			t.Fatalf("OpenAI model %q advertised tool search without deterministic catalog proof", model.Model)
		}
	}
	anthropic := findProvider(t, c.ListProviders(ProviderListParams{}).Data, ProviderAnthropic)
	if !anthropic.Capabilities.ToolSearch {
		t.Fatal("anthropic catalog did not advertise proven tool search")
	}

	aggregate, err := c.ProviderCapabilities("")
	if err != nil {
		t.Fatalf("ProviderCapabilities(aggregate): %v", err)
	}
	if !aggregate.NamespaceTools || !aggregate.ToolCalls || !aggregate.Reasoning {
		t.Fatalf("aggregate capabilities missing expected feature: %#v", aggregate)
	}

	_, err = c.ProviderCapabilities("missing")
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("missing provider err = %v, want ErrProviderNotFound", err)
	}
}

func TestThinkingCapabilitiesAreModelSpecific(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(nil)))
	cases := []struct {
		provider string
		model    string
		adaptive bool
		manual   bool
	}{
		{ProviderAnthropic, "claude-sonnet-4-6", true, true},
		{ProviderAnthropic, "claude-opus-4-7", true, false},
		{ProviderAnthropic, "claude-haiku-4-5-20251001", false, false},
		{ProviderVertexAIAnthropic, "claude-sonnet-4-6", true, true},
		{ProviderVertexAIAnthropic, "claude-opus-4-7", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			provider := findProvider(t, c.providers, tc.provider)
			for _, model := range provider.Models {
				if model.Model != tc.model {
					continue
				}
				if model.Capabilities.AdaptiveThinking != tc.adaptive || model.Capabilities.ManualThinking != tc.manual {
					t.Fatalf("thinking capabilities = %#v, want adaptive=%t manual=%t", model.Capabilities, tc.adaptive, tc.manual)
				}
				return
			}
			t.Fatalf("model %q missing from provider %#v", tc.model, provider.Models)
		})
	}
}

func TestStopSequenceCapabilitiesAreModelSpecific(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(nil)))
	for _, tc := range []struct {
		provider string
		model    string
		want     bool
	}{
		{ProviderOpenAI, "gpt-4o", false},
		{ProviderOpenAICompatibleLocal, "llama3", false},
		{ProviderAnthropic, "claude-sonnet-4-6", true},
		{ProviderAnthropic, "claude-haiku-4-5-20251001", true},
		{ProviderVertexAI, vertexprovider.Gemini25Flash, true},
		{ProviderVertexAIAnthropic, "claude-opus-4-7", true},
	} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			provider := findProvider(t, c.providers, tc.provider)
			for _, model := range provider.Models {
				if model.Model != tc.model {
					continue
				}
				if model.Capabilities.StopSequences != tc.want {
					t.Fatalf("stop sequence capability = %t, want %t", model.Capabilities.StopSequences, tc.want)
				}
				return
			}
			t.Fatalf("model %q missing from provider %#v", tc.model, provider.Models)
		})
	}
	for _, tc := range []struct {
		provider string
		want     bool
	}{
		{ProviderOpenAI, false},
		{ProviderOpenAICompatibleLocal, false},
		{ProviderAnthropic, true},
		{ProviderVertexAI, true},
		{ProviderVertexAIAnthropic, true},
	} {
		provider := findProvider(t, c.providers, tc.provider)
		if provider.Capabilities.StopSequences != tc.want {
			t.Errorf("provider %q stop sequence capability = %t, want %t", tc.provider, provider.Capabilities.StopSequences, tc.want)
		}
	}
}

func TestToolSearchCapabilityIsModelSpecific(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(nil)))
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-6", true},
		{"claude-opus-4-6", true},
		{"claude-opus-4-7", true},
		{"claude-opus-4-8", true},
		{"claude-fable-5", false},
		{"claude-haiku-4-5-20251001", false},
	}
	provider := findProvider(t, c.providers, ProviderAnthropic)
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			for _, model := range provider.Models {
				if model.Model != tc.model {
					continue
				}
				if model.Capabilities.ToolSearch != tc.want {
					t.Fatalf("tool-search capability = %t, want %t", model.Capabilities.ToolSearch, tc.want)
				}
				return
			}
			t.Fatalf("model %q missing from provider %#v", tc.model, provider.Models)
		})
	}
}

func TestReasoningSummaryCapabilityIsModelSpecific(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(nil)))
	cases := []struct {
		provider string
		model    string
		want     bool
	}{
		{ProviderOpenAI, "gpt-4o", false},
		{ProviderOpenAI, "gpt-5", true},
		{ProviderOpenAI, "gpt-5-mini", true},
		{ProviderOpenAI, "gpt-5-codex", true},
		{ProviderAnthropic, "claude-sonnet-4-6", false},
		{ProviderVertexAIAnthropic, "claude-sonnet-4-6", false},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			provider := findProvider(t, c.providers, tc.provider)
			for _, model := range provider.Models {
				if model.Model != tc.model {
					continue
				}
				if model.Capabilities.ReasoningSummaries != tc.want {
					t.Fatalf("reasoning summary capability = %t, want %t", model.Capabilities.ReasoningSummaries, tc.want)
				}
				return
			}
			t.Fatalf("model %q missing from provider %#v", tc.model, provider.Models)
		})
	}

	for _, tc := range []struct {
		provider string
		want     bool
	}{
		{ProviderOpenAI, true},
		{ProviderAnthropic, false},
		{ProviderVertexAIAnthropic, false},
	} {
		provider := findProvider(t, c.providers, tc.provider)
		if provider.Capabilities.ReasoningSummaries != tc.want {
			t.Fatalf("provider %q reasoning summaries = %t, want %t", tc.provider, provider.Capabilities.ReasoningSummaries, tc.want)
		}
	}
}

func TestReasoningEffortMetadataIsModelSpecific(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(nil)))
	for _, provider := range c.providers {
		for _, model := range provider.Models {
			name := provider.ID + "/" + model.Model
			if !model.Capabilities.Reasoning {
				if len(model.SupportedReasoningEfforts) != 0 {
					t.Errorf("%s exposes reasoning efforts without reasoning capability: %#v", name, model.SupportedReasoningEfforts)
				}
				if model.DefaultReasoningEffort != nil {
					t.Errorf("%s exposes default reasoning effort without reasoning capability: %q", name, *model.DefaultReasoningEffort)
				}
				continue
			}
			if len(model.SupportedReasoningEfforts) == 0 {
				t.Errorf("%s advertises reasoning without effort options", name)
			}
			if model.DefaultReasoningEffort == nil {
				t.Errorf("%s advertises reasoning without a default effort", name)
				continue
			}
			if !slices.ContainsFunc(model.SupportedReasoningEfforts, func(option ReasoningEffortOption) bool {
				return option.ReasoningEffort == *model.DefaultReasoningEffort
			}) {
				t.Errorf("%s default reasoning effort %q is absent from %#v", name, *model.DefaultReasoningEffort, model.SupportedReasoningEfforts)
			}
		}
	}
}

func TestValidateAgentRuntimeSelection(t *testing.T) {
	c := NewDefault(WithEnvLookup(mapEnv(map[string]string{
		"OPENAI_API_KEY": "configured",
	})))

	if err := c.ValidateAgentRuntimeSelection(ProviderOpenAI, "gpt-5"); err != nil {
		t.Fatalf("ValidateAgentRuntimeSelection configured model: %v", err)
	}
	mutatedToolCapability := false
	for providerIndex := range c.providers {
		provider := &c.providers[providerIndex]
		if provider.ID != ProviderOpenAI {
			continue
		}
		for modelIndex := range provider.Models {
			model := &provider.Models[modelIndex]
			if model.Model != "gpt-4o" {
				continue
			}
			model.Capabilities.ToolCalls = false
			mutatedToolCapability = true
			err := c.ValidateAgentRuntimeSelection(ProviderOpenAI, model.Model)
			if err == nil || !strings.Contains(err.Error(), "does not advertise streaming agent tool use") {
				t.Fatalf("ValidateAgentRuntimeSelection missing tool capability error = %v", err)
			}
			break
		}
		break
	}
	if !mutatedToolCapability {
		t.Fatal("OpenAI gpt-4o catalog fixture is missing")
	}

	for _, tc := range []struct {
		name       string
		providerID string
		model      string
		want       string
	}{
		{
			name:       "unconfigured provider",
			providerID: ProviderAnthropic,
			model:      "claude-sonnet-4-6",
			want:       "not configured",
		},
		{
			name:       "unknown provider",
			providerID: "unsupported-provider",
			model:      "model",
			want:       "provider selection is unavailable",
		},
		{
			name:       "unknown model",
			providerID: ProviderOpenAI,
			model:      "unlisted-model",
			want:       "model selection is unavailable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := c.ValidateAgentRuntimeSelection(tc.providerID, tc.model)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateAgentRuntimeSelection(%q, %q) error = %v, want %q", tc.providerID, tc.model, err, tc.want)
			}
		})
	}
}

func TestLocalOpenAICompatibleProviderUsesExplicitSafeConfiguration(t *testing.T) {
	const baseURL = "http://127.0.0.1:8765/v1"
	const model = "local-tool-model"
	const token = "local-secret-value"
	c := NewDefault(WithEnvLookup(mapEnv(map[string]string{
		openaiprovider.LocalEndpointBaseURLEnv: baseURL,
		openaiprovider.LocalEndpointModelEnv:   model,
		openaiprovider.LocalEndpointAPIKeyEnv:  token,
	})))

	provider := findProvider(t, c.ListProviders(ProviderListParams{}).Data, ProviderOpenAICompatibleLocal)
	if !provider.Configured {
		t.Fatal("explicit valid local profile was not configured")
	}
	if !provider.Capabilities.ToolCalls || !provider.Capabilities.Streaming || provider.Capabilities.StructuredOutput || provider.Capabilities.Vision {
		t.Fatalf("local provider capabilities = %#v", provider.Capabilities)
	}
	models, err := c.ListModels(ModelListParams{ProviderID: ProviderOpenAICompatibleLocal})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models.Data) != 1 || models.Data[0].Model != model || !models.Data[0].Capabilities.ToolCalls || !models.Data[0].Capabilities.Streaming {
		t.Fatalf("local provider models = %#v", models.Data)
	}
	if models.Data[0].DefaultReasoningEffort != nil || len(models.Data[0].SupportedReasoningEfforts) != 0 {
		t.Fatalf("local provider reasoning metadata = default %#v, efforts %#v; want unavailable", models.Data[0].DefaultReasoningEffort, models.Data[0].SupportedReasoningEfforts)
	}

	encoded, err := json.Marshal(provider)
	if err != nil {
		t.Fatalf("marshal provider: %v", err)
	}
	for _, secret := range []string{baseURL, token} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("provider metadata leaked local configuration %q: %s", secret, encoded)
		}
	}
}

func TestLocalOpenAICompatibleProviderRequiresExplicitValidConfiguration(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"unset": nil,
		"remote endpoint": {
			openaiprovider.LocalEndpointBaseURLEnv: "https://example.com/v1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := NewDefault(WithEnvLookup(mapEnv(env)))
			provider := findProvider(t, c.ListProviders(ProviderListParams{}).Data, ProviderOpenAICompatibleLocal)
			if provider.Configured {
				t.Fatalf("local provider was configured for %#v", env)
			}
		})
	}
}

func TestProbeProviderReportsBoundedLocalHealth(t *testing.T) {
	const token = "local-probe-secret"
	var authorization string
	available := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("probe request = %s %s, want GET /v1/models", r.Method, r.URL.Path)
		}
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer available.Close()

	configuredEnv := map[string]string{
		openaiprovider.LocalEndpointBaseURLEnv: available.URL,
		openaiprovider.LocalEndpointModelEnv:   "local-tool-model",
		openaiprovider.LocalEndpointAPIKeyEnv:  token,
	}
	c := NewDefault(WithEnvLookup(mapEnv(configuredEnv)))
	response, err := c.ProbeProvider(context.Background(), ProviderHealthProbeParams{ProviderID: ProviderOpenAICompatibleLocal})
	if err != nil {
		t.Fatalf("ProbeProvider: %v", err)
	}
	if response.ProviderID != ProviderOpenAICompatibleLocal || response.Status != ProviderHealthAvailable {
		t.Fatalf("available probe = %#v", response)
	}
	if authorization != "Bearer "+token {
		t.Fatalf("Authorization = %q, want local token", authorization)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	for _, secret := range []string{available.URL, token} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("probe response leaked local configuration %q: %s", secret, encoded)
		}
	}

	notConfigured := NewDefault(WithEnvLookup(mapEnv(nil)))
	response, err = notConfigured.ProbeProvider(context.Background(), ProviderHealthProbeParams{ProviderID: ProviderOpenAICompatibleLocal})
	if err != nil || response.Status != ProviderHealthNotConfigured {
		t.Fatalf("not-configured probe = %#v, %v", response, err)
	}

	unsupported, err := c.ProbeProvider(context.Background(), ProviderHealthProbeParams{ProviderID: ProviderOpenAI})
	if err != nil || unsupported.Status != ProviderHealthUnsupported {
		t.Fatalf("unsupported probe = %#v, %v", unsupported, err)
	}

	_, err = c.ProbeProvider(context.Background(), ProviderHealthProbeParams{ProviderID: "unknown-provider"})
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("unknown provider error = %v, want ErrProviderNotFound", err)
	}
}

func TestProbeProviderReportsUnavailableWithoutEndpointDetails(t *testing.T) {
	const token = "unavailable-local-probe-secret"
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("endpoint unavailable"))
	}))
	defer unavailable.Close()
	c := NewDefault(WithEnvLookup(mapEnv(map[string]string{
		openaiprovider.LocalEndpointBaseURLEnv: unavailable.URL,
		openaiprovider.LocalEndpointModelEnv:   "local-tool-model",
		openaiprovider.LocalEndpointAPIKeyEnv:  token,
	})))

	response, err := c.ProbeProvider(context.Background(), ProviderHealthProbeParams{ProviderID: ProviderOpenAICompatibleLocal})
	if err != nil || response.Status != ProviderHealthUnavailable {
		t.Fatalf("unavailable probe = %#v, %v", response, err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	for _, secret := range []string{unavailable.URL, token} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("probe response leaked local configuration %q: %s", secret, encoded)
		}
	}
}

func TestToolListAvailability(t *testing.T) {
	resp := ListTools(ToolListParams{}, ToolServices{Filesystem: true})
	if len(resp.Data) == 0 {
		t.Fatal("tool/list returned no available tools")
	}
	if findTool(resp.Data, "process") != nil {
		t.Fatal("unavailable process tool returned without includeUnavailable")
	}
	fs := findTool(resp.Data, "fs")
	if fs == nil || !fs.Available || !fs.Mutation || !fs.RequiresApproval {
		t.Fatalf("filesystem tool metadata = %#v", fs)
	}
	if !containsMethod(fs.Methods, "fs/watch") || !containsMethod(fs.Methods, "fs/unwatch") {
		t.Fatalf("filesystem tool methods = %#v", fs.Methods)
	}
	processTool := findTool(ListTools(ToolListParams{}, ToolServices{Process: true}).Data, "process")
	if processTool == nil || !processTool.Available || !processTool.Mutation || !processTool.RequiresApproval {
		t.Fatalf("process tool metadata = %#v", processTool)
	}
	if !containsMethod(processTool.Methods, "thread/shellCommand") || !containsMethod(processTool.Methods, "thread/backgroundTerminals/list") || !containsMethod(processTool.Methods, "thread/backgroundTerminals/read") || !containsMethod(processTool.Methods, "thread/backgroundTerminals/write") || !containsMethod(processTool.Methods, "thread/backgroundTerminals/resize") || !containsMethod(processTool.Methods, "thread/backgroundTerminals/clean") {
		t.Fatalf("process tool methods = %#v", processTool.Methods)
	}

	withUnavailable := ListTools(ToolListParams{IncludeUnavailable: true}, ToolServices{Filesystem: true})
	process := findTool(withUnavailable.Data, "process")
	if process == nil || process.Available || process.UnavailableReason == "" {
		t.Fatalf("process unavailable metadata = %#v", process)
	}
	cacheTool := findTool(ListTools(ToolListParams{}, ToolServices{Cache: true}).Data, "cache")
	if cacheTool == nil || !cacheTool.Available || !cacheTool.GollemExtension {
		t.Fatalf("cache tool metadata = %#v", cacheTool)
	}
	providerCatalogTool := findTool(ListTools(ToolListParams{}, ToolServices{}).Data, "provider-catalog")
	if providerCatalogTool == nil || !providerCatalogTool.GollemExtension || !containsMethod(providerCatalogTool.Methods, "provider/health/probe") {
		t.Fatalf("provider catalog tool metadata = %#v", providerCatalogTool)
	}
	memoryTool := findTool(ListTools(ToolListParams{}, ToolServices{Memory: true}).Data, "memory")
	if memoryTool == nil || !memoryTool.Available || memoryTool.GollemExtension || !memoryTool.CodexCompatible || !memoryTool.Mutation {
		t.Fatalf("memory tool metadata = %#v", memoryTool)
	}
	if !containsMethod(memoryTool.Methods, "memory/reset") {
		t.Fatalf("memory tool methods = %#v", memoryTool.Methods)
	}
	configTool := findTool(ListTools(ToolListParams{}, ToolServices{Config: true}).Data, "config")
	if configTool == nil || !configTool.Available || !configTool.CodexCompatible || configTool.GollemExtension {
		t.Fatalf("config tool metadata = %#v", configTool)
	}
	if !containsMethod(configTool.Methods, "config/read") || !containsMethod(configTool.Methods, "permissionProfile/list") {
		t.Fatalf("config tool methods = %#v", configTool.Methods)
	}
	mcpTool := findTool(ListTools(ToolListParams{}, ToolServices{MCP: true}).Data, "mcp")
	if mcpTool == nil || !mcpTool.Available || !mcpTool.CodexCompatible || mcpTool.GollemExtension || !mcpTool.RequiresApproval {
		t.Fatalf("mcp tool metadata = %#v", mcpTool)
	}
	if !containsMethod(mcpTool.Methods, "mcpServerStatus/list") || !containsMethod(mcpTool.Methods, "mcpServer/tool/call") {
		t.Fatalf("mcp tool methods = %#v", mcpTool.Methods)
	}
	skillsTool := findTool(ListTools(ToolListParams{}, ToolServices{Skills: true}).Data, "skills")
	if skillsTool == nil || !skillsTool.Available || !skillsTool.CodexCompatible || skillsTool.Mutation || skillsTool.RequiresApproval {
		t.Fatalf("skills tool metadata = %#v", skillsTool)
	}
	if !containsMethod(skillsTool.Methods, "skills/list") || !containsMethod(skillsTool.Methods, "plugin/skill/read") {
		t.Fatalf("skills tool methods = %#v", skillsTool.Methods)
	}
	threadStoreTool := findTool(ListTools(ToolListParams{}, ToolServices{}).Data, "thread-store")
	if threadStoreTool == nil || !threadStoreTool.Available || !threadStoreTool.CodexCompatible || threadStoreTool.Mutation {
		t.Fatalf("thread-store tool metadata = %#v", threadStoreTool)
	}
	if !containsMethod(threadStoreTool.Methods, "thread/search") || !containsMethod(threadStoreTool.Methods, "thread/loaded/list") || !containsMethod(threadStoreTool.Methods, "thread/unsubscribe") || !containsMethod(threadStoreTool.Methods, "thread/compact/start") || !containsMethod(threadStoreTool.Methods, "thread/rollback") || !containsMethod(threadStoreTool.Methods, "thread/inject_items") || !containsMethod(threadStoreTool.Methods, "thread/goal/set") || !containsMethod(threadStoreTool.Methods, "thread/memoryMode/set") || !containsMethod(threadStoreTool.Methods, "thread/name/set") {
		t.Fatalf("thread-store tool methods = %#v", threadStoreTool.Methods)
	}
	revertTool := findTool(ListTools(ToolListParams{}, ToolServices{Filesystem: true, FileRecovery: true}).Data, "file-change-revert")
	if revertTool == nil || !revertTool.Available || !revertTool.Mutation || !revertTool.RequiresApproval ||
		!containsMethod(revertTool.Methods, "item/fileChange/revert") {
		t.Fatalf("file-change-revert tool metadata = %#v", revertTool)
	}
	if unavailable := findTool(ListTools(ToolListParams{IncludeUnavailable: true}, ToolServices{Filesystem: true}).Data, "file-change-revert"); unavailable == nil || unavailable.Available {
		t.Fatalf("file-change-revert advertised without durable recovery = %#v", unavailable)
	}
}

func mapEnv(values map[string]string) EnvLookup {
	return func(key string) (string, bool) {
		if values == nil {
			return "", false
		}
		value, ok := values[key]
		return value, ok
	}
}

func findProvider(t *testing.T, providers []Provider, id string) Provider {
	t.Helper()
	for _, provider := range providers {
		if provider.ID == id {
			return provider
		}
	}
	t.Fatalf("provider %q not found in %#v", id, providers)
	return Provider{}
}

func findTool(tools []Tool, id string) *Tool {
	for i := range tools {
		if tools[i].ID == id {
			return &tools[i]
		}
	}
	return nil
}

func containsMethod(methods []string, want string) bool {
	for _, method := range methods {
		if method == want {
			return true
		}
	}
	return false
}
