package modelutil

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/fugue-labs/gollem/core"
)

// ModelRouter selects a model based on the prompt and context.
type ModelRouter interface {
	Route(ctx context.Context, prompt string) (core.Model, error)
}

// RouterModel wraps a ModelRouter as a Model, routing each request to
// the appropriate underlying model.
type RouterModel struct {
	router ModelRouter
}

// NewRouterModel creates a Model that delegates to a router.
func NewRouterModel(router ModelRouter) *RouterModel {
	return &RouterModel{router: router}
}

func (r *RouterModel) ModelName() string {
	return "router"
}

func (r *RouterModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	prompt := extractLastUserPrompt(messages)
	model, err := r.router.Route(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("router: %w", err)
	}
	return model.Request(ctx, messages, settings, params)
}

func (r *RouterModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	prompt := extractLastUserPrompt(messages)
	model, err := r.router.Route(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("router: %w", err)
	}
	return model.RequestStream(ctx, messages, settings, params)
}

// extractLastUserPrompt extracts the last user prompt from messages.
func extractLastUserPrompt(messages []core.ModelMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if req, ok := messages[i].(core.ModelRequest); ok {
			for j := len(req.Parts) - 1; j >= 0; j-- {
				if up, ok := req.Parts[j].(core.UserPromptPart); ok {
					return up.Content
				}
			}
		}
	}
	return ""
}

// classifierRouter routes based on a classification function.
type classifierRouter struct {
	models   map[string]core.Model
	classify func(ctx context.Context, prompt string) string
}

// ClassifierRouter routes based on a classification function.
// The classify function returns a key that maps to one of the provided models.
func ClassifierRouter(models map[string]core.Model, classify func(ctx context.Context, prompt string) string) ModelRouter {
	return &classifierRouter{models: models, classify: classify}
}

func (r *classifierRouter) Route(ctx context.Context, prompt string) (core.Model, error) {
	key := r.classify(ctx, prompt)
	model, ok := r.models[key]
	if !ok {
		return nil, fmt.Errorf("classifier returned unknown key %q", key)
	}
	return model, nil
}

// thresholdRouter routes based on prompt length.
type thresholdRouter struct {
	simple    core.Model
	complex   core.Model
	threshold int
}

// ThresholdRouter routes to the complex model when the prompt exceeds
// a character length threshold, otherwise uses the simple model.
func ThresholdRouter(simple, complex core.Model, threshold int) ModelRouter {
	return &thresholdRouter{simple: simple, complex: complex, threshold: threshold}
}

func (r *thresholdRouter) Route(_ context.Context, prompt string) (core.Model, error) {
	if len(prompt) > r.threshold {
		return r.complex, nil
	}
	return r.simple, nil
}

// roundRobinRouter distributes requests across models.
type roundRobinRouter struct {
	models []core.Model
	idx    atomic.Uint64
}

// RoundRobinRouter distributes requests across models evenly.
func RoundRobinRouter(models ...core.Model) ModelRouter {
	return &roundRobinRouter{models: models}
}

func (r *roundRobinRouter) Route(_ context.Context, _ string) (core.Model, error) {
	if len(r.models) == 0 {
		return nil, errors.New("round robin: no models configured")
	}
	idx := r.idx.Add(1) - 1
	return r.models[idx%uint64(len(r.models))], nil
}
