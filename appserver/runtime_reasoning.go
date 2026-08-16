package appserver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fugue-labs/gollem/appserver/catalog"
	"github.com/fugue-labs/gollem/core"
)

func validateRuntimeReasoningSelection(
	catalogService *catalog.Catalog,
	selection RuntimeModelSelection,
	settings core.ModelSettings,
) error {
	if settings.ReasoningEffort == nil {
		return nil
	}
	effort := strings.TrimSpace(*settings.ReasoningEffort)
	if effort == "" {
		return errors.New("reasoning effort must not be empty")
	}
	selected, err := selectedRuntimeCatalogModel(catalogService, selection)
	if err != nil {
		return err
	}
	if !selected.Capabilities.Reasoning {
		return fmt.Errorf("model %q does not advertise reasoning", selected.ID)
	}
	for _, option := range selected.SupportedReasoningEfforts {
		if strings.TrimSpace(option.ReasoningEffort) == effort {
			return nil
		}
	}
	return fmt.Errorf(
		"model %q does not advertise reasoning effort %q",
		selected.ID,
		effort,
	)
}

func validateRuntimeThinkingSelection(
	catalogService *catalog.Catalog,
	selection RuntimeModelSelection,
	settings core.ModelSettings,
) error {
	adaptive := settings.AdaptiveThinking != nil && *settings.AdaptiveThinking
	if settings.ThinkingBudget == nil && !adaptive {
		return nil
	}
	if settings.ThinkingBudget != nil && adaptive {
		return errors.New("thinking budget and adaptive thinking are mutually exclusive")
	}
	selected, err := selectedRuntimeCatalogModel(catalogService, selection)
	if err != nil {
		return err
	}
	if settings.ThinkingBudget != nil && !selected.Capabilities.ManualThinking {
		return fmt.Errorf("model %q does not advertise manual thinking", selected.ID)
	}
	if adaptive && !selected.Capabilities.AdaptiveThinking {
		return fmt.Errorf("model %q does not advertise adaptive thinking", selected.ID)
	}
	return nil
}

func validateRuntimeReasoningSummarySelection(
	catalogService *catalog.Catalog,
	selection RuntimeModelSelection,
	settings core.ModelSettings,
) error {
	if settings.ReasoningSummary == nil {
		return nil
	}
	summary := strings.TrimSpace(*settings.ReasoningSummary)
	switch summary {
	case "auto", "concise", "detailed":
	default:
		return fmt.Errorf("reasoning summary %q is unavailable; choose auto, concise, or detailed", summary)
	}
	selected, err := selectedRuntimeCatalogModel(catalogService, selection)
	if err != nil {
		return err
	}
	if !selected.Capabilities.ReasoningSummaries {
		return fmt.Errorf("model %q does not advertise reasoning summaries", selected.ID)
	}
	return nil
}

func selectedRuntimeCatalogModel(catalogService *catalog.Catalog, selection RuntimeModelSelection) (*catalog.Model, error) {
	providerID := strings.TrimSpace(firstNonEmpty(selection.ProviderID, selection.Provider))
	modelName := strings.TrimSpace(selection.Model)
	if providerID == "" {
		return nil, errors.New("provider capability is unavailable")
	}
	includeHidden := true
	response, err := catalogService.ListModels(catalog.ModelListParams{
		ProviderID:    providerID,
		IncludeHidden: &includeHidden,
	})
	if err != nil {
		return nil, fmt.Errorf("read model capability: %w", err)
	}
	var selected *catalog.Model
	for index := range response.Data {
		model := &response.Data[index]
		if modelName == "" {
			if model.IsDefault {
				selected = model
				break
			}
			continue
		}
		if model.ID == modelName || model.Model == modelName {
			selected = model
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("model capability is unavailable for %q", modelName)
	}
	return selected, nil
}
