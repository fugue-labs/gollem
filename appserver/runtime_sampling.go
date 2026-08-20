package appserver

import (
	"fmt"

	"github.com/fugue-labs/gollem/appserver/catalog"
	"github.com/fugue-labs/gollem/core"
)

// validateRuntimeSamplingSelection prevents durable settings from recording
// sampling controls a selected model would silently discard.
func validateRuntimeSamplingSelection(
	catalogService *catalog.Catalog,
	selection RuntimeModelSelection,
	settings core.ModelSettings,
) error {
	if settings.Temperature == nil && settings.TopP == nil {
		return nil
	}
	selected, err := selectedRuntimeCatalogModel(catalogService, selection)
	if err != nil {
		return err
	}
	if !selected.Capabilities.Sampling {
		return fmt.Errorf("model %q does not advertise sampling", selected.ID)
	}
	manualThinking := settings.ThinkingBudget != nil && *settings.ThinkingBudget > 0
	adaptiveThinking := settings.AdaptiveThinking != nil && *settings.AdaptiveThinking
	if (manualThinking || adaptiveThinking) &&
		(selected.Capabilities.ManualThinking || selected.Capabilities.AdaptiveThinking) {
		return fmt.Errorf("model %q does not advertise sampling with thinking", selected.ID)
	}
	return nil
}
