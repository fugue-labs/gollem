package catwalk

import "maps"

// Type identifies the wire protocol a provider speaks.
type Type string

// Provider types published by catwalk.
const (
	TypeOpenAI       Type = "openai"
	TypeOpenAICompat Type = "openai-compat"
	TypeOpenRouter   Type = "openrouter"
	TypeVercel       Type = "vercel"
	TypeAnthropic    Type = "anthropic"
	TypeGoogle       Type = "google"
	TypeAzure        Type = "azure"
	TypeBedrock      Type = "bedrock"
	TypeVertexAI     Type = "google-vertex"
)

// Provider is one inference provider in the registry.
//
// APIKey and APIEndpoint may be literal values or "$ENV_VAR" references;
// see EnvReference.
type Provider struct {
	Name                string            `json:"name"`
	ID                  string            `json:"id"`
	APIKey              string            `json:"api_key,omitempty"` // #nosec G117 -- holds a "$ENV_VAR" reference, never a secret value
	APIEndpoint         string            `json:"api_endpoint,omitempty"`
	Type                Type              `json:"type,omitempty"`
	DefaultLargeModelID string            `json:"default_large_model_id,omitempty"`
	DefaultSmallModelID string            `json:"default_small_model_id,omitempty"`
	Models              []Model           `json:"models,omitempty"`
	DefaultHeaders      map[string]string `json:"default_headers,omitempty"`
}

// Model is one model offered by a provider.
type Model struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	CostPer1MIn            float64  `json:"cost_per_1m_in"`
	CostPer1MOut           float64  `json:"cost_per_1m_out"`
	CostPer1MInCached      float64  `json:"cost_per_1m_in_cached"`
	CostPer1MOutCached     float64  `json:"cost_per_1m_out_cached"`
	ContextWindow          int64    `json:"context_window"`
	DefaultMaxTokens       int64    `json:"default_max_tokens"`
	CanReason              bool     `json:"can_reason"`
	ReasoningLevels        []string `json:"reasoning_levels,omitempty"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort,omitempty"`
	SupportsImages         bool     `json:"supports_attachments"`
}

// EnvReference reports whether value is a "$NAME" environment reference and,
// if so, returns NAME.
func EnvReference(value string) (string, bool) {
	if len(value) < 2 || value[0] != '$' {
		return "", false
	}
	name := value[1:]
	for _, r := range name {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			continue
		}
		return "", false
	}
	return name, true
}

// Clone returns a deep copy of the provider list.
func Clone(providers []Provider) []Provider {
	if providers == nil {
		return nil
	}
	out := make([]Provider, len(providers))
	for i, provider := range providers {
		out[i] = cloneProvider(provider)
	}
	return out
}

func cloneProvider(provider Provider) Provider {
	if provider.Models != nil {
		models := make([]Model, len(provider.Models))
		for i, model := range provider.Models {
			models[i] = model
			if model.ReasoningLevels != nil {
				models[i].ReasoningLevels = append([]string(nil), model.ReasoningLevels...)
			}
		}
		provider.Models = models
	}
	provider.DefaultHeaders = maps.Clone(provider.DefaultHeaders)
	return provider
}
