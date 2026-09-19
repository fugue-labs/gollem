package appserver

import "github.com/fugue-labs/gollem/core"

// WithRuntimeSelectionDefaults declares the provider and model the runtime
// factory falls back to when a selection leaves them empty (for the CLI, the
// --provider and --model flags). Capability checks resolve empty fields the
// same way, so they judge the model that will actually run.
func WithRuntimeSelectionDefaults(defaults RuntimeModelSelection) Option {
	return func(s *Server) {
		s.selectionDefaults = defaults
	}
}

// resolveRuntimeSelection applies the configured defaults exactly as the
// CLI runtime factory does: provider falls back when both provider fields are
// empty, model falls back when the selection names none.
func (s *Server) resolveRuntimeSelection(selection RuntimeModelSelection) RuntimeModelSelection {
	if s == nil {
		return selection
	}
	if selection.ProviderID == "" && selection.Provider == "" {
		selection.ProviderID = s.selectionDefaults.ProviderID
	}
	if selection.Model == "" {
		selection.Model = s.selectionDefaults.Model
	}
	return selection
}

func (s *Server) validateRuntimeSelection(selection RuntimeModelSelection, settings core.ModelSettings) error {
	selection = s.resolveRuntimeSelection(selection)
	if s != nil && s.selectionValidator != nil {
		if err := s.selectionValidator(selection); err != nil {
			return err
		}
	}
	if err := validateRuntimeReasoningSelection(s.catalog, selection, settings); err != nil {
		return err
	}
	if err := validateRuntimeReasoningSummarySelection(s.catalog, selection, settings); err != nil {
		return err
	}
	if err := validateRuntimePromptCacheSelection(s.catalog, selection, settings); err != nil {
		return err
	}
	if err := validateRuntimeSamplingSelection(s.catalog, selection, settings); err != nil {
		return err
	}
	if err := validateRuntimeStopSequenceSelection(s.catalog, selection, settings); err != nil {
		return err
	}
	return validateRuntimeThinkingSelection(s.catalog, selection, settings)
}
