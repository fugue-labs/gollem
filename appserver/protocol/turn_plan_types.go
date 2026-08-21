package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

type TurnPlanStepStatus string

const (
	TurnPlanStepStatusPending    TurnPlanStepStatus = "pending"
	TurnPlanStepStatusInProgress TurnPlanStepStatus = "inProgress"
	TurnPlanStepStatusCompleted  TurnPlanStepStatus = "completed"
)

func (s TurnPlanStepStatus) MarshalJSON() ([]byte, error) {
	return marshalThreadTurnLeafEnum(s, "turn plan step status", TurnPlanStepStatus.valid)
}

func (s *TurnPlanStepStatus) UnmarshalJSON(data []byte) error {
	return unmarshalThreadTurnLeafEnum(data, s, "turn plan step status", TurnPlanStepStatus.valid)
}

func (s TurnPlanStepStatus) valid() bool {
	switch s {
	case TurnPlanStepStatusPending, TurnPlanStepStatusInProgress, TurnPlanStepStatusCompleted:
		return true
	default:
		return false
	}
}

type TurnPlanStep struct {
	Step   string             `json:"step"`
	Status TurnPlanStepStatus `json:"status"`
}

func (s *TurnPlanStep) UnmarshalJSON(data []byte) error {
	if s == nil {
		return errors.New("decode turn plan step into nil receiver")
	}
	payload, err := decodeExactThreadItemObject(data, "turn plan step", "step", "status")
	if err != nil {
		return err
	}
	step, err := decodeRequiredThreadItemValue[string](payload, "turn plan step", "step")
	if err != nil {
		return err
	}
	status, err := decodeRequiredThreadItemValue[TurnPlanStepStatus](payload, "turn plan step", "status")
	if err != nil {
		return err
	}
	*s = TurnPlanStep{Step: step, Status: status}
	return nil
}

// TurnPlanUpdatedNotification is the exact fixed public turn-plan snapshot.
type TurnPlanUpdatedNotification struct {
	ThreadID    string         `json:"threadId"`
	TurnID      string         `json:"turnId"`
	Explanation *string        `json:"explanation"`
	Plan        []TurnPlanStep `json:"plan" jsonschema:"nonnullable=true"`
}

func (n TurnPlanUpdatedNotification) MarshalJSON() ([]byte, error) {
	if n.Plan == nil {
		return nil, errors.New("turn-plan notification plan cannot be null")
	}
	type wire TurnPlanUpdatedNotification
	encoded, err := json.Marshal(wire(n))
	if err != nil {
		return nil, fmt.Errorf("encode turn-plan notification: %w", err)
	}
	return encoded, nil
}

func (n *TurnPlanUpdatedNotification) UnmarshalJSON(data []byte) error {
	if n == nil {
		return errors.New("decode turn-plan notification into nil receiver")
	}
	payload, err := decodeExactThreadItemObject(
		data,
		"turn-plan notification",
		"threadId",
		"turnId",
		"explanation",
		"plan",
	)
	if err != nil {
		return err
	}
	threadID, err := decodeRequiredThreadItemValue[string](payload, "turn-plan notification", "threadId")
	if err != nil {
		return err
	}
	turnID, err := decodeRequiredThreadItemValue[string](payload, "turn-plan notification", "turnId")
	if err != nil {
		return err
	}
	plan, err := decodeRequiredThreadItemArray[TurnPlanStep](payload, "turn-plan notification", "plan")
	if err != nil {
		return err
	}
	explanation, err := decodeOptionalNullableTurnPlanExplanation(payload)
	if err != nil {
		return err
	}
	*n = TurnPlanUpdatedNotification{
		ThreadID:    threadID,
		TurnID:      turnID,
		Explanation: explanation,
		Plan:        plan,
	}
	return nil
}

func decodeOptionalNullableTurnPlanExplanation(payload map[string]json.RawMessage) (*string, error) {
	raw, ok := payload["explanation"]
	if !ok || isJSONNull(raw) {
		return nil, nil
	}
	var explanation string
	if err := json.Unmarshal(raw, &explanation); err != nil {
		return nil, fmt.Errorf("decode turn-plan notification explanation: %w", err)
	}
	return &explanation, nil
}

var (
	_ json.Marshaler   = TurnPlanStepStatus("")
	_ json.Unmarshaler = (*TurnPlanStepStatus)(nil)
	_ json.Unmarshaler = (*TurnPlanStep)(nil)
	_ json.Marshaler   = TurnPlanUpdatedNotification{}
	_ json.Unmarshaler = (*TurnPlanUpdatedNotification)(nil)
)
