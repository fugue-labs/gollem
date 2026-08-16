package appserver

import (
	"fmt"

	"github.com/fugue-labs/gollem/appserver/catalog"
	"github.com/fugue-labs/gollem/core"
)

// validateRuntimePromptCacheSelection prevents durable runtime settings from
// claiming cache control for a model whose catalog profile cannot honor it.
func validateRuntimePromptCacheSelection(
	catalogService *catalog.Catalog,
	selection RuntimeModelSelection,
	settings core.ModelSettings,
) error {
	if settings.PromptCacheEnabled == nil {
		return nil
	}
	selected, err := selectedRuntimeCatalogModel(catalogService, selection)
	if err != nil {
		return err
	}
	if !selected.Capabilities.PromptCache {
		return fmt.Errorf("model %q does not advertise prompt caching", selected.ID)
	}
	return nil
}
