package appserver

import (
	"fmt"

	"github.com/fugue-labs/gollem/appserver/catalog"
	"github.com/fugue-labs/gollem/core"
)

// validateRuntimeStopSequenceSelection prevents unsupported providers from
// silently accepting a durable request setting they cannot apply.
func validateRuntimeStopSequenceSelection(
	catalogService *catalog.Catalog,
	selection RuntimeModelSelection,
	settings core.ModelSettings,
) error {
	if len(settings.StopSequences) == 0 {
		return nil
	}
	selected, err := selectedRuntimeCatalogModel(catalogService, selection)
	if err != nil {
		return err
	}
	if !selected.Capabilities.StopSequences {
		return fmt.Errorf("model %q does not advertise stop sequences", selected.ID)
	}
	return nil
}
