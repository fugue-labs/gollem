package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/appserver/catalog"
	"github.com/fugue-labs/gollem/provider/anthropic"
	"github.com/fugue-labs/gollem/provider/openai"
)

func TestNewRegistryModelBuildsTransportPerFamily(t *testing.T) {
	httpClient := &http.Client{Timeout: time.Second}
	t.Setenv("DEEPSEEK_API_KEY", " sk-deepseek ")
	t.Setenv("MINIMAX_API_KEY", "sk-minimax")

	openaiEndpoint := catalog.RuntimeEndpoint{
		ProviderID:          "deepseek",
		Family:              catalog.RuntimeFamilyOpenAI,
		BaseURL:             "https://api.deepseek.com/v1",
		APIKeyEnv:           "DEEPSEEK_API_KEY",
		Headers:             map[string]string{"X-Title": "Gollem"},
		ChatCompletionsOnly: true,
		DefaultModel:        "deepseek-v4-pro",
	}
	model, err := newRegistryModel(openaiEndpoint, "", httpClient, nil)
	if err != nil {
		t.Fatalf("newRegistryModel(openai family): %v", err)
	}
	provider, ok := model.(*openai.Provider)
	if !ok {
		t.Fatalf("model type = %T, want *openai.Provider", model)
	}
	if provider.ModelName() != "deepseek-v4-pro" {
		t.Fatalf("default model = %q", provider.ModelName())
	}
	explicit, err := newRegistryModel(openaiEndpoint, "deepseek-v4-flash", httpClient, nil)
	if err != nil || explicit.ModelName() != "deepseek-v4-flash" {
		t.Fatalf("explicit model = %v, %v", explicit, err)
	}

	anthropicEndpoint := catalog.RuntimeEndpoint{
		ProviderID:   "minimax",
		Family:       catalog.RuntimeFamilyAnthropic,
		BaseURL:      "https://api.minimax.io/anthropic",
		APIKeyEnv:    "MINIMAX_API_KEY",
		DefaultModel: "MiniMax-M3",
	}
	model, err = newRegistryModel(anthropicEndpoint, "", httpClient, nil)
	if err != nil {
		t.Fatalf("newRegistryModel(anthropic family): %v", err)
	}
	if _, ok := model.(*anthropic.Provider); !ok || model.ModelName() != "MiniMax-M3" {
		t.Fatalf("anthropic-family model = %T %q", model, model.ModelName())
	}
}

func TestNewRegistryModelRejectsMissingConfiguration(t *testing.T) {
	httpClient := &http.Client{Timeout: time.Second}
	t.Setenv("DEEPSEEK_API_KEY", "")
	endpoint := catalog.RuntimeEndpoint{
		ProviderID:   "deepseek",
		Family:       catalog.RuntimeFamilyOpenAI,
		BaseURL:      "https://api.deepseek.com/v1",
		APIKeyEnv:    "DEEPSEEK_API_KEY",
		DefaultModel: "deepseek-v4-pro",
	}
	_, err := newRegistryModel(endpoint, "", httpClient, nil)
	if err == nil || !strings.Contains(err.Error(), "requires DEEPSEEK_API_KEY") {
		t.Fatalf("missing key error = %v", err)
	}
	injected, err := newRegistryModel(endpoint, "", httpClient, func(name string) (string, bool) {
		return "from-lookup", name == "DEEPSEEK_API_KEY"
	})
	if err != nil || injected == nil {
		t.Fatalf("injected lookup should supply the key: %v", err)
	}

	t.Setenv("DEEPSEEK_API_KEY", "set")
	endpoint.DefaultModel = ""
	_, err = newRegistryModel(endpoint, "", httpClient, nil)
	if err == nil || !strings.Contains(err.Error(), "no default model") {
		t.Fatalf("missing model error = %v", err)
	}

	endpoint.Family = "bedrock"
	_, err = newRegistryModel(endpoint, "m", httpClient, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported transport family") {
		t.Fatalf("unsupported family error = %v", err)
	}
}

func TestCreateModelFallsBackToRegistryProviders(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "sk-groq")
	model, err := createModel("groq", "", "", "", time.Second)
	if err != nil {
		t.Fatalf("createModel(groq): %v", err)
	}
	provider, ok := model.(*openai.Provider)
	if !ok || provider.IsDeepSeek() || model.ModelName() == "" {
		t.Fatalf("groq model = %T %q", model, model.ModelName())
	}

	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek")
	model, err = createModel("deepseek", "", "", "", time.Second)
	if err != nil {
		t.Fatalf("createModel(deepseek): %v", err)
	}
	if provider, ok := model.(*openai.Provider); !ok || !provider.IsDeepSeek() || model.ModelName() != openai.DeepSeekV4Pro {
		t.Fatalf("deepseek model = %T %q", model, model.ModelName())
	}

	_, err = createModel("not-a-provider", "", "", "", time.Second)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unknown provider error = %v", err)
	}
}

func TestCreateModelUsesCatalogDefaults(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	anthropicModel, err := createModel("anthropic", "", "", "", time.Second)
	if err != nil {
		t.Fatalf("createModel(anthropic): %v", err)
	}
	if anthropicModel.ModelName() != anthropic.ClaudeOpus5 {
		t.Fatalf("anthropic default = %q, want the catalog default %q", anthropicModel.ModelName(), anthropic.ClaudeOpus5)
	}
	openaiModel, err := createModel("openai", "", "", "", time.Second)
	if err != nil {
		t.Fatalf("createModel(openai): %v", err)
	}
	if openaiModel.ModelName() != openai.GPT56Sol {
		t.Fatalf("openai default = %q, want the catalog default %q", openaiModel.ModelName(), openai.GPT56Sol)
	}
	explicit, err := createModel("openai", "gpt-4o", "", "", time.Second)
	if err != nil || explicit.ModelName() != "gpt-4o" {
		t.Fatalf("explicit model = %v, %v", explicit, err)
	}
	if testModel, err := createModel("test", "", "", "", time.Second); err != nil || testModel == nil {
		t.Fatalf("test fixture = %v, %v", testModel, err)
	}
}

func TestRegistryRefreshEnabled(t *testing.T) {
	for value, want := range map[string]bool{"": true, "1": true, "true": true, "0": false, " off ": false, "False": false, "no": false} {
		t.Setenv(registryRefreshEnv, value)
		if got := registryRefreshEnabled(); got != want {
			t.Errorf("registryRefreshEnabled() with %q = %v, want %v", value, got, want)
		}
	}
}
