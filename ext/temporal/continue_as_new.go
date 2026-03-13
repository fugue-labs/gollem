package temporal

import (
	"fmt"

	"go.temporal.io/sdk/workflow"
)

// ContinueAsNewConfig controls when a durable Temporal agent run should roll
// over into a fresh workflow execution while preserving run state.
type ContinueAsNewConfig struct {
	MaxTurns         int  `json:"max_turns,omitempty"`
	MaxMessages      int  `json:"max_messages,omitempty"`
	MaxHistoryLength int  `json:"max_history_length,omitempty"`
	MaxHistorySize   int  `json:"max_history_size,omitempty"`
	OnSuggested      bool `json:"on_suggested,omitempty"`
}

func (c ContinueAsNewConfig) enabled() bool {
	return c.MaxTurns > 0 || c.MaxMessages > 0 || c.MaxHistoryLength > 0 || c.MaxHistorySize > 0 || c.OnSuggested
}

func (c ContinueAsNewConfig) reason(ctx workflow.Context, state *workflowRunState) string {
	if !c.enabled() {
		return ""
	}
	turnsThisRun := state.RunStep - state.ContinueAsNewBaseRunStep
	messagesThisRun := len(state.Messages) - state.ContinueAsNewBaseMessageCount
	if c.MaxTurns > 0 && turnsThisRun >= c.MaxTurns {
		return fmt.Sprintf("max_turns=%d", c.MaxTurns)
	}
	if c.MaxMessages > 0 && messagesThisRun >= c.MaxMessages {
		return fmt.Sprintf("max_messages=%d", c.MaxMessages)
	}
	info := workflow.GetInfo(ctx)
	if c.MaxHistoryLength > 0 && info.GetCurrentHistoryLength() >= c.MaxHistoryLength {
		return fmt.Sprintf("max_history_length=%d", c.MaxHistoryLength)
	}
	if c.MaxHistorySize > 0 && info.GetCurrentHistorySize() >= c.MaxHistorySize {
		return fmt.Sprintf("max_history_size=%d", c.MaxHistorySize)
	}
	if c.OnSuggested && info.GetContinueAsNewSuggested() {
		return "server_suggested"
	}
	return ""
}
