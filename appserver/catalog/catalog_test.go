package catalog

import (
	"errors"
	"testing"
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
	if !containsMethod(processTool.Methods, "thread/shellCommand") || !containsMethod(processTool.Methods, "thread/backgroundTerminals/list") || !containsMethod(processTool.Methods, "thread/backgroundTerminals/clean") {
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
