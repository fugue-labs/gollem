package appserver

import "github.com/fugue-labs/gollem/core"

func (s *Server) validateRuntimeSelection(selection RuntimeModelSelection, settings core.ModelSettings) error {
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
