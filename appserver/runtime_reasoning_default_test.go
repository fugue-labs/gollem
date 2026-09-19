package appserver

import (
	"testing"

	"github.com/fugue-labs/gollem/core"

	"github.com/fugue-labs/gollem/appserver/catalog"
	"github.com/fugue-labs/gollem/appserver/catalog/catwalk"
)

// An empty model runs the provider's DefaultModelID, so capability checks
// must judge that model even when it is not listed first and another
// provider holds the list-wide IsDefault flag.
func TestSelectedRuntimeCatalogModelUsesProviderDefault(t *testing.T) {
	registry := []catwalk.Provider{
		{ID: "anthropic", Type: catwalk.TypeAnthropic, APIKey: "$ANTHROPIC_API_KEY", Models: []catwalk.Model{{ID: "claude-opus-5", CanReason: true, ReasoningLevels: []string{"low", "high"}}}},
		{ID: "openai", Type: catwalk.TypeOpenAI, APIKey: "$OPENAI_API_KEY", DefaultLargeModelID: "gpt-5.6-sol", Models: []catwalk.Model{
			{ID: "gpt-6-astra", CanReason: true, ReasoningLevels: []string{"low", "high"}},
			{ID: "gpt-5.6-sol", CanReason: true, ReasoningLevels: []string{"none", "low", "high"}},
		}},
	}
	env := map[string]string{"ANTHROPIC_API_KEY": "k", "OPENAI_API_KEY": "k"}
	c := catalog.NewDefault(catalog.WithRegistry(registry), catalog.WithEnvLookup(func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}))
	selected, err := selectedRuntimeCatalogModel(c, RuntimeModelSelection{ProviderID: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Model != "gpt-5.6-sol" {
		t.Fatalf("empty-model capability checks judged %q, want the provider default gpt-5.6-sol", selected.Model)
	}
	if err := c.ValidateAgentRuntimeSelection("openai", ""); err != nil {
		t.Fatalf("provider default rejected: %v", err)
	}
}

func TestRuntimeSelectionDefaultsGovernCapabilityChecks(t *testing.T) {
	registry := []catwalk.Provider{
		{ID: "openai", Type: catwalk.TypeOpenAI, APIKey: "$OPENAI_API_KEY", DefaultLargeModelID: "gpt-5.6-sol", Models: []catwalk.Model{
			{ID: "gpt-5.6-sol", CanReason: true, ReasoningLevels: []string{"low", "high"}},
			{ID: "gpt-4o"},
		}},
		{ID: "anthropic", Type: catwalk.TypeAnthropic, APIKey: "$ANTHROPIC_API_KEY", Models: []catwalk.Model{{ID: "claude-opus-5"}}},
	}
	c := catalog.NewDefault(catalog.WithRegistry(registry), catalog.WithEnvLookup(func(name string) (string, bool) {
		return "k", name == "OPENAI_API_KEY"
	}))
	server := &Server{catalog: c}
	WithRuntimeSelectionDefaults(RuntimeModelSelection{ProviderID: "openai", Model: "gpt-4o"})(server)
	resolved := server.resolveRuntimeSelection(RuntimeModelSelection{})
	if resolved.ProviderID != "openai" || resolved.Model != "gpt-4o" {
		t.Fatalf("resolved selection = %#v", resolved)
	}
	effort := "high"
	if err := server.validateRuntimeSelection(RuntimeModelSelection{}, core.ModelSettings{ReasoningEffort: &effort}); err == nil {
		t.Fatal("reasoning effort accepted for the --model default gpt-4o, which has no reasoning")
	}
	explicit := server.resolveRuntimeSelection(RuntimeModelSelection{ProviderID: "anthropic", Model: "claude-opus-5"})
	if explicit.ProviderID != "anthropic" || explicit.Model != "claude-opus-5" {
		t.Fatalf("explicit selection overridden: %#v", explicit)
	}
}
