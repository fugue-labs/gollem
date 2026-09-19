package openai

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fugue-labs/gollem/core"
)

// DeepSeek model identifiers (https://api-docs.deepseek.com/quick_start/pricing).
const (
	DeepSeekV4Pro   = "deepseek-v4-pro"
	DeepSeekV4Flash = "deepseek-v4-flash"
	DeepSeekFlash   = "deepseek-flash"
)

const (
	deepSeekDefaultBaseURL = "https://api.deepseek.com"
	deepSeekDefaultModel   = DeepSeekV4Pro
	// DeepSeekMaxOutputTokens is the API's max_tokens ceiling. When the caller
	// sets no MaxTokens the field is omitted so the API applies its own
	// mode-dependent default (8K plain, 64K thinking, 128K at max effort).
	DeepSeekMaxOutputTokens = 384_000

	// DeepSeekAPIKeyEnv and DeepSeekBaseURLEnv configure NewDeepSeek.
	DeepSeekAPIKeyEnv  = "DEEPSEEK_API_KEY"
	DeepSeekBaseURLEnv = "DEEPSEEK_BASE_URL"

	// deepSeekEmptyToolOutput replaces empty tool results; the API rejects
	// tool messages without content.
	deepSeekEmptyToolOutput = "(no output)"
)

// NewDeepSeek creates a provider for DeepSeek's Chat Completions API
// (https://api.deepseek.com). Compared with a plain OpenAI-compatible
// endpoint it:
//
//   - sends the V4 thinking toggle ({"thinking": {"type": "enabled"|"disabled"}})
//     from ModelSettings.AdaptiveThinking and ModelSettings.ReasoningEffort,
//   - surfaces reasoning_content as core.ThinkingPart / core.ThinkingPartDelta,
//   - replays reasoning_content on every prior assistant turn, which the API
//     requires whenever tools are in play (it ignores the field otherwise),
//   - uses max_tokens (not max_completion_tokens) and never the Responses API.
//
// The API key comes from DEEPSEEK_API_KEY (or WithAPIKey); DEEPSEEK_BASE_URL
// (or WithBaseURL) overrides the endpoint.
func NewDeepSeek(opts ...Option) *Provider {
	baseURL := strings.TrimSpace(os.Getenv(DeepSeekBaseURLEnv))
	if baseURL == "" {
		baseURL = deepSeekDefaultBaseURL
	}
	allOpts := []Option{
		withoutOpenAIKeyFallback(),
		WithBaseURL(baseURL),
		WithModel(deepSeekDefaultModel),
		WithChatCompletionsOnly(),
	}
	if key := strings.TrimSpace(os.Getenv(DeepSeekAPIKeyEnv)); key != "" {
		allOpts = append(allOpts, WithAPIKey(key))
	}
	allOpts = append(allOpts, opts...)
	p := New(allOpts...)
	p.deepseek = true
	return p
}

// withoutOpenAIKeyFallback stops New from defaulting the API key to
// OPENAI_API_KEY; that secret must never be sent to another vendor.
func withoutOpenAIKeyFallback() Option {
	return func(p *Provider) {
		p.noOpenAIKeyFallback = true
	}
}

// configuredMaxTokens returns the WithMaxTokens value, or 0 when unset.
func (p *Provider) configuredMaxTokens() int {
	if p.maxTokensSet {
		return p.maxTokens
	}
	return 0
}

// IsDeepSeek reports whether the provider targets DeepSeek's API.
func (p *Provider) IsDeepSeek() bool {
	return p.deepseek
}

// deepSeekEffort maps a Gollem reasoning effort onto the values DeepSeek
// accepts (https://api-docs.deepseek.com/guides/thinking_mode): "medium"
// rounds up to "high" as the API itself documents; "none" disables thinking.
func deepSeekEffort(effort string) (string, bool) {
	switch effort {
	case "low", "high", "max", "none":
		return effort, true
	case "medium":
		return "high", true
	default:
		return "", false
	}
}

// applyDeepSeekRequest reshapes a Chat Completions request for DeepSeek.
// configuredMax is the provider-level WithMaxTokens value (0 when unset).
func applyDeepSeekRequest(req *apiRequest, settings *core.ModelSettings, configuredMax int) error {
	// DeepSeek reads max_tokens; max_completion_tokens is ignored. Only an
	// explicit value (per request, else per provider) is forwarded so the
	// API's mode-dependent default applies otherwise.
	req.MaxTokens = 0
	maxTokens := configuredMax
	if settings != nil && settings.MaxTokens != nil && *settings.MaxTokens > 0 {
		maxTokens = *settings.MaxTokens
	}
	if maxTokens > 0 {
		req.MaxTokensLegacy = min(maxTokens, DeepSeekMaxOutputTokens)
	}
	// Not supported by the endpoint; New() only sets these for openai.com.
	req.PromptCacheKey = ""
	req.PromptCacheRetention = ""
	req.ServiceTier = ""

	if settings != nil {
		adaptiveSet := settings.AdaptiveThinking != nil
		adaptive := adaptiveSet && *settings.AdaptiveThinking
		effort := ""
		if settings.ReasoningEffort != nil {
			effort = strings.TrimSpace(*settings.ReasoningEffort)
		}
		if effort != "" {
			mapped, ok := deepSeekEffort(effort)
			if !ok {
				return fmt.Errorf("openai/deepseek: reasoning effort %q is not supported; use low, medium, high, or max", effort)
			}
			effort = mapped
		}
		if effort == "none" {
			// "none" is the API's spelling of thinking off.
			effort = ""
			adaptive, adaptiveSet = false, true
		}
		if settings.ThinkingBudget != nil && *settings.ThinkingBudget > 0 && !adaptiveSet && effort == "" {
			// A manual budget has no DeepSeek equivalent; treat it as a
			// request to think.
			adaptive, adaptiveSet = true, true
		}
		switch {
		case adaptiveSet && !adaptive && effort != "":
			return errors.New("openai/deepseek: AdaptiveThinking=false conflicts with ReasoningEffort; thinking must be enabled to set an effort")
		case adaptiveSet && !adaptive:
			req.Thinking = &apiThinking{Type: "disabled"}
			req.ReasoningEffort = nil
		case adaptive || effort != "":
			req.Thinking = &apiThinking{Type: "enabled"}
			if effort != "" {
				// The reference harness sends the OpenAI-style top-level
				// field; the API reference documents it inside "thinking".
				// Both carry the same value.
				req.ReasoningEffort = &effort
				req.Thinking.ReasoningEffort = effort
			}
		}
	}

	for i := range req.Messages {
		msg := &req.Messages[i]
		switch msg.Role {
		case "assistant":
			// With tools in play the API requires reasoning_content on every
			// earlier assistant turn (400 otherwise); without tools it is
			// ignored, so always replaying is the safe choice.
			msg.emitReasoning = msg.ReasoningContent != ""
		case "tool":
			if msg.Content == "" && len(msg.ContentParts) == 0 {
				msg.Content = deepSeekEmptyToolOutput
			}
		}
	}
	return nil
}

// apiThinking is DeepSeek's thinking toggle.
type apiThinking struct {
	Type            string `json:"type"` // "enabled" or "disabled"
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// DeepSeekModelSupportsVision reports whether a DeepSeek model accepts image
// input: the Flash line does, V4 Pro does not.
func DeepSeekModelSupportsVision(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "flash")
}

func deepSeekRequestError(err error) error {
	return fmt.Errorf("openai: failed to build request: %w", err)
}
