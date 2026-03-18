package agui

import (
	"encoding/json"
	"errors"
	"fmt"
)

// EncodePayload marshals a typed AGUI payload into a reusable raw JSON blob.
func EncodePayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return nil, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal AGUI payload: %w", err)
	}
	return json.RawMessage(data), nil
}

// DecodePayload unmarshals a raw AGUI payload into the given destination.
func DecodePayload(data json.RawMessage, target any) error {
	if len(data) == 0 {
		return nil
	}
	if target == nil {
		return errors.New("decode AGUI payload: nil target")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode AGUI payload: %w", err)
	}
	return nil
}

// MustEncodePayload marshals a payload and panics if encoding fails.
// It is intended for tests and static protocol fixtures.
func MustEncodePayload(payload any) json.RawMessage {
	data, err := EncodePayload(payload)
	if err != nil {
		panic(err)
	}
	return data
}

// NewEvent constructs an event envelope with a typed payload.
func NewEvent(sequence int64, eventType EventType, session Session, turnNumber int, payload any) (Event, error) {
	raw, err := EncodePayload(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{
		Sequence:    sequence,
		Type:        eventType,
		SessionID:   session.SessionID,
		RunID:       session.RunID,
		ParentRunID: session.ParentRunID,
		TurnNumber:  turnNumber,
		Timestamp:   session.CreatedAt,
		Payload:     raw,
	}, nil
}

// SetPayload encodes and stores a typed payload on the event.
func (e *Event) SetPayload(payload any) error {
	if e == nil {
		return errors.New("set AGUI event payload: nil event")
	}
	raw, err := EncodePayload(payload)
	if err != nil {
		return err
	}
	e.Payload = raw
	return nil
}

// PayloadAs decodes the event payload into the supplied destination.
func (e Event) PayloadAs(target any) error {
	return DecodePayload(e.Payload, target)
}

// DecodeEventPayload decodes an event payload into a typed value.
func DecodeEventPayload[T any](event Event) (*T, error) {
	var payload T
	if err := event.PayloadAs(&payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// MarshalEvent serializes a single AGUI event.
func MarshalEvent(event Event) ([]byte, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal AGUI event: %w", err)
	}
	return data, nil
}

// UnmarshalEvent deserializes a single AGUI event.
func UnmarshalEvent(data []byte) (*Event, error) {
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("unmarshal AGUI event: %w", err)
	}
	return &event, nil
}

// MarshalEvents serializes a list of AGUI events.
func MarshalEvents(events []Event) ([]byte, error) {
	data, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("marshal AGUI events: %w", err)
	}
	return data, nil
}

// UnmarshalEvents deserializes a list of AGUI events.
func UnmarshalEvents(data []byte) ([]Event, error) {
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("unmarshal AGUI events: %w", err)
	}
	return events, nil
}

// NewClientCommand constructs a command envelope with a typed payload.
func NewClientCommand(commandType ClientCommandType, sessionID string, payload any) (ClientCommand, error) {
	raw, err := EncodePayload(payload)
	if err != nil {
		return ClientCommand{}, err
	}
	return ClientCommand{Type: commandType, SessionID: sessionID, Payload: raw}, nil
}

// SetPayload encodes and stores a typed payload on the command.
func (c *ClientCommand) SetPayload(payload any) error {
	if c == nil {
		return errors.New("set AGUI command payload: nil command")
	}
	raw, err := EncodePayload(payload)
	if err != nil {
		return err
	}
	c.Payload = raw
	return nil
}

// PayloadAs decodes the command payload into the supplied destination.
func (c ClientCommand) PayloadAs(target any) error {
	return DecodePayload(c.Payload, target)
}

// DecodeCommandPayload decodes a command payload into a typed value.
func DecodeCommandPayload[T any](command ClientCommand) (*T, error) {
	var payload T
	if err := command.PayloadAs(&payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// MarshalCommand serializes a single AGUI command.
func MarshalCommand(command ClientCommand) ([]byte, error) {
	data, err := json.Marshal(command)
	if err != nil {
		return nil, fmt.Errorf("marshal AGUI command: %w", err)
	}
	return data, nil
}

// UnmarshalCommand deserializes a single AGUI command.
func UnmarshalCommand(data []byte) (*ClientCommand, error) {
	var command ClientCommand
	if err := json.Unmarshal(data, &command); err != nil {
		return nil, fmt.Errorf("unmarshal AGUI command: %w", err)
	}
	return &command, nil
}

// MarshalSessionState serializes a session snapshot.
func MarshalSessionState(state SessionState) ([]byte, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("marshal AGUI session state: %w", err)
	}
	return data, nil
}

// UnmarshalSessionState deserializes a session snapshot.
func UnmarshalSessionState(data []byte) (*SessionState, error) {
	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal AGUI session state: %w", err)
	}
	return &state, nil
}

// MarshalJSON serializes an open-session request.
func (r OpenSessionRequest) MarshalJSON() ([]byte, error) {
	type alias OpenSessionRequest
	data, err := json.Marshal(alias(r))
	if err != nil {
		return nil, fmt.Errorf("marshal AGUI open session request: %w", err)
	}
	return data, nil
}

// UnmarshalJSON deserializes an open-session request.
func (r *OpenSessionRequest) UnmarshalJSON(data []byte) error {
	type alias OpenSessionRequest
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("unmarshal AGUI open session request: %w", err)
	}
	*r = OpenSessionRequest(decoded)
	return nil
}
