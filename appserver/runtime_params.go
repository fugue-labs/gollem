package appserver

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/core"
	"github.com/google/uuid"
)

type RuntimeModelParams = protocol.RuntimeModelParams
type threadStartParams = protocol.ThreadRunStartParams

type threadResumeParams struct {
	ID       string         `json:"id,omitempty"`
	ThreadID string         `json:"threadId,omitempty"`
	Prompt   string         `json:"prompt,omitempty"`
	Message  string         `json:"message,omitempty"`
	Text     string         `json:"text,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	RuntimeModelParams
}

func (p threadResumeParams) turnStartParams() turnStartParams {
	return turnStartParams(p)
}

type threadSettingsUpdateParams struct {
	ID       string         `json:"id,omitempty"`
	ThreadID string         `json:"threadId,omitempty"`
	Settings map[string]any `json:"settings,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Replace  bool           `json:"replace,omitempty"`
}

const (
	threadGoalSettingKey       = "goal"
	threadMemoryModeSettingKey = "memoryMode"
)

type turnStartParams = protocol.TurnRunStartParams
type turnIDParams = protocol.TurnRunInterruptParams

type turnRetryParams struct {
	ID             string         `json:"id,omitempty"`
	TurnID         string         `json:"turnId,omitempty"`
	IdempotencyKey *string        `json:"idempotencyKey,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	Message        string         `json:"message,omitempty"`
	Text           string         `json:"text,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	RuntimeModelParams
}

func (p turnRetryParams) retryIdempotencyKey() (string, error) {
	if p.IdempotencyKey == nil {
		return uuid.NewString(), nil
	}
	key := strings.TrimSpace(*p.IdempotencyKey)
	if key == "" || len(key) > 256 {
		return "", errors.New("idempotencyKey must contain 1 to 256 bytes")
	}
	return key, nil
}

func runtimePromptFromStartParams(prompt, message, text string, input json.RawMessage) string {
	return strings.TrimSpace(firstNonEmpty(prompt, message, text, runtimePromptFromInput(input)))
}

func runtimeSelectionFromParams(providerID, provider, model string) RuntimeModelSelection {
	return RuntimeModelSelection{
		ProviderID: strings.TrimSpace(providerID),
		Provider:   strings.TrimSpace(provider),
		Model:      strings.TrimSpace(model),
	}
}

func runtimeSelectionFromInput(input json.RawMessage) RuntimeModelSelection {
	var stored runtimeTurnInput
	if len(input) == 0 || json.Unmarshal(input, &stored) != nil {
		return RuntimeModelSelection{}
	}
	return RuntimeModelSelection{
		ProviderID: strings.TrimSpace(stored.ProviderID),
		Provider:   strings.TrimSpace(stored.Provider),
		Model:      strings.TrimSpace(stored.Model),
	}
}

func mergeRuntimeSelection(primary, fallback RuntimeModelSelection) RuntimeModelSelection {
	if primary.ProviderID == "" {
		primary.ProviderID = fallback.ProviderID
	}
	if primary.Provider == "" {
		primary.Provider = fallback.Provider
	}
	if primary.Model == "" {
		primary.Model = fallback.Model
	}
	return primary
}

func runtimeSelectionWithThreadDefaults(selection RuntimeModelSelection, settings map[string]any) RuntimeModelSelection {
	if selection.ProviderID == "" {
		selection.ProviderID = stringSetting(settings, "providerId")
	}
	if selection.Provider == "" {
		selection.Provider = stringSetting(settings, "provider")
	}
	if selection.Model == "" {
		selection.Model = stringSetting(settings, "model")
	}
	return selection
}

func runtimeModelSettingsFromParams(params RuntimeModelParams) core.ModelSettings {
	settings := core.ModelSettings{
		MaxTokens:          params.MaxTokens,
		Temperature:        params.Temperature,
		TopP:               params.TopP,
		ThinkingBudget:     params.ThinkingBudget,
		AdaptiveThinking:   params.AdaptiveThinking,
		ReasoningEffort:    params.ReasoningEffort,
		ReasoningSummary:   params.ReasoningSummary,
		PromptCacheEnabled: params.PromptCacheEnabled,
		StopSequences:      append([]string(nil), params.StopSequences...),
	}
	if settings.ReasoningEffort == nil {
		if effort := stringSetting(params.Settings, "reasoningEffort"); effort != "" {
			settings.ReasoningEffort = &effort
		}
	}
	return settings
}

func runtimeModelSettingsFromInput(input json.RawMessage) core.ModelSettings {
	var stored runtimeTurnInput
	if len(input) == 0 || json.Unmarshal(input, &stored) != nil {
		return core.ModelSettings{}
	}
	settings := core.ModelSettings{
		MaxTokens:          cloneInt(stored.MaxTokens),
		Temperature:        cloneFloat64(stored.Temperature),
		TopP:               cloneFloat64(stored.TopP),
		ThinkingBudget:     cloneInt(stored.ThinkingBudget),
		AdaptiveThinking:   cloneBool(stored.AdaptiveThinking),
		ReasoningSummary:   cloneString(stored.ReasoningSummary),
		PromptCacheEnabled: cloneBool(stored.PromptCacheEnabled),
		StopSequences:      append([]string(nil), stored.StopSequences...),
	}
	if effort := strings.TrimSpace(stored.ReasoningEffort); effort != "" {
		settings.ReasoningEffort = &effort
	}
	return settings
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneString(value string) *string {
	if value == "" {
		return nil
	}
	cloned := value
	return &cloned
}

func runtimeModelSettingsWithThreadDefaults(
	settings core.ModelSettings,
	threadSettings map[string]any,
) core.ModelSettings {
	if settings.ReasoningEffort == nil {
		if effort := stringSetting(threadSettings, "reasoningEffort"); effort != "" {
			settings.ReasoningEffort = &effort
		}
	}
	return settings
}

func mergeRuntimeModelSettings(primary, fallback core.ModelSettings) core.ModelSettings {
	if primary.MaxTokens == nil {
		primary.MaxTokens = cloneInt(fallback.MaxTokens)
	}
	if primary.Temperature == nil {
		primary.Temperature = cloneFloat64(fallback.Temperature)
	}
	if primary.TopP == nil {
		primary.TopP = cloneFloat64(fallback.TopP)
	}
	if primary.ThinkingBudget == nil {
		primary.ThinkingBudget = cloneInt(fallback.ThinkingBudget)
	}
	if primary.AdaptiveThinking == nil {
		primary.AdaptiveThinking = cloneBool(fallback.AdaptiveThinking)
	}
	if primary.ReasoningEffort == nil {
		primary.ReasoningEffort = cloneString(runtimeReasoningEffort(fallback))
	}
	if primary.ReasoningSummary == nil {
		primary.ReasoningSummary = cloneString(runtimeReasoningSummary(fallback))
	}
	if primary.PromptCacheEnabled == nil {
		primary.PromptCacheEnabled = cloneBool(fallback.PromptCacheEnabled)
	}
	if len(primary.StopSequences) == 0 {
		primary.StopSequences = append([]string(nil), fallback.StopSequences...)
	}
	return primary
}

func mergeRuntimeReasoningIntoSettings(
	settings map[string]any,
	reasoningEffort *string,
) map[string]any {
	if reasoningEffort == nil {
		return settings
	}
	effort := strings.TrimSpace(*reasoningEffort)
	if effort == "" {
		return settings
	}
	if settings == nil {
		settings = make(map[string]any)
	}
	settings["reasoningEffort"] = effort
	return settings
}

func cloneSettings(settings map[string]any) map[string]any {
	if len(settings) == 0 {
		return nil
	}
	out := make(map[string]any, len(settings))
	for key, value := range settings {
		out[key] = value
	}
	return out
}

func mergeRuntimeSelectionIntoSettings(settings map[string]any, providerID, provider, model string) map[string]any {
	if settings == nil {
		settings = make(map[string]any)
	}
	if strings.TrimSpace(providerID) != "" {
		settings["providerId"] = strings.TrimSpace(providerID)
	}
	if strings.TrimSpace(provider) != "" {
		settings["provider"] = strings.TrimSpace(provider)
	}
	if strings.TrimSpace(model) != "" {
		settings["model"] = strings.TrimSpace(model)
	}
	if len(settings) == 0 {
		return nil
	}
	return settings
}

func stringSetting(settings map[string]any, key string) string {
	value, ok := settings[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
