package modelutil

import (
	"context"
	"errors"

	"github.com/fugue-labs/gollem/core"
)

// ModelProfile describes a model's capabilities.
type ModelProfile struct {
	SupportsToolCalls        bool   `json:"supports_tool_calls"`
	SupportsStructuredOutput bool   `json:"supports_structured_output"`
	SupportsVision           bool   `json:"supports_vision"`
	SupportsStreaming         bool   `json:"supports_streaming"`
	MaxContextTokens         int    `json:"max_context_tokens,omitempty"`
	MaxOutputTokens          int    `json:"max_output_tokens,omitempty"`
	ProviderName             string `json:"provider_name,omitempty"`
}

// Profiled is an optional interface models can implement to declare capabilities.
type Profiled interface {
	Profile() ModelProfile
}

// GetProfile returns the model's profile if it implements Profiled,
// or a default profile assuming full capabilities.
func GetProfile(model core.Model) ModelProfile {
	if p, ok := model.(Profiled); ok {
		return p.Profile()
	}
	// Default: assume full capabilities.
	return ModelProfile{
		SupportsToolCalls:        true,
		SupportsStructuredOutput: true,
		SupportsVision:           true,
		SupportsStreaming:         true,
	}
}

// capabilityRouter routes to the first model matching required capabilities.
type capabilityRouter struct {
	models   []core.Model
	required ModelProfile
}

// NewCapabilityRouter creates a router that selects the first model
// matching the required capabilities.
func NewCapabilityRouter(models []core.Model, required ModelProfile) ModelRouter {
	return &capabilityRouter{models: models, required: required}
}

func (r *capabilityRouter) Route(_ context.Context, _ string) (core.Model, error) {
	for _, m := range r.models {
		p := GetProfile(m)
		if matchesProfile(p, r.required) {
			return m, nil
		}
	}
	return nil, errors.New("capability router: no model matches required capabilities")
}

// matchesProfile checks if profile p satisfies the required capabilities.
func matchesProfile(p, required ModelProfile) bool {
	if required.SupportsToolCalls && !p.SupportsToolCalls {
		return false
	}
	if required.SupportsStructuredOutput && !p.SupportsStructuredOutput {
		return false
	}
	if required.SupportsVision && !p.SupportsVision {
		return false
	}
	if required.SupportsStreaming && !p.SupportsStreaming {
		return false
	}
	if required.MaxContextTokens > 0 && p.MaxContextTokens > 0 && p.MaxContextTokens < required.MaxContextTokens {
		return false
	}
	if required.MaxOutputTokens > 0 && p.MaxOutputTokens > 0 && p.MaxOutputTokens < required.MaxOutputTokens {
		return false
	}
	return true
}
