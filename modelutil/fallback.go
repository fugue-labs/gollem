package modelutil

import (
	"context"
	"fmt"

	"github.com/fugue-labs/gollem/core"
)

// FallbackModel wraps multiple models, trying each in order until one succeeds.
// If a model request fails, the next model in the chain is tried.
// This is useful for reliability (e.g., try Claude, fall back to GPT-4).
type FallbackModel struct {
	models []core.Model
}

// Compile-time interface check.
var _ core.Model = (*FallbackModel)(nil)

// NewFallbackModel creates a model that tries each model in order.
// At least two models must be provided.
func NewFallbackModel(primary core.Model, fallbacks ...core.Model) *FallbackModel {
	models := make([]core.Model, 0, 1+len(fallbacks))
	models = append(models, primary)
	models = append(models, fallbacks...)
	return &FallbackModel{models: models}
}

func (f *FallbackModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	var lastErr error
	for _, m := range f.models {
		resp, err := m.Request(ctx, messages, settings, params)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("all models failed, last error: %w", lastErr)
}

func (f *FallbackModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	var lastErr error
	for _, m := range f.models {
		resp, err := m.RequestStream(ctx, messages, settings, params)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("all models failed, last error: %w", lastErr)
}

func (f *FallbackModel) ModelName() string {
	if len(f.models) > 0 {
		return f.models[0].ModelName() + "+fallback"
	}
	return "fallback"
}
