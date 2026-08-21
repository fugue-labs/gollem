package protocol

import (
	"encoding/json"
	"errors"
)

// CurrentTimeReadParams identifies the thread whose connected client owns the
// external clock used by the active turn.
type CurrentTimeReadParams struct {
	ThreadID string `json:"threadId"`
}

func (p *CurrentTimeReadParams) UnmarshalJSON(data []byte) error {
	if p == nil {
		return errors.New("decode current-time params into nil receiver")
	}
	const objectName = "current-time params"
	payload, err := decodeRustSerdeObject(data, objectName, "threadId")
	if err != nil {
		return err
	}
	threadID, err := decodeRequiredThreadItemValue[string](payload, objectName, "threadId")
	if err != nil {
		return err
	}
	*p = CurrentTimeReadParams{ThreadID: threadID}
	return nil
}

// CurrentTimeReadResponse returns the client clock as whole Unix seconds.
type CurrentTimeReadResponse struct {
	CurrentTimeAt int64 `json:"currentTimeAt"`
}

func (r *CurrentTimeReadResponse) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.New("decode current-time response into nil receiver")
	}
	const objectName = "current-time response"
	payload, err := decodeRustSerdeObject(data, objectName, "currentTimeAt")
	if err != nil {
		return err
	}
	currentTimeAt, err := decodeRequiredThreadItemValue[int64](payload, objectName, "currentTimeAt")
	if err != nil {
		return err
	}
	*r = CurrentTimeReadResponse{CurrentTimeAt: currentTimeAt}
	return nil
}

func currentTimeReadParamsSchema() Schema {
	return Schema{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title":   "CurrentTimeReadParams",
		"type":    "object",
		"properties": Schema{
			"threadId": Schema{"type": "string"},
		},
		"required": []string{"threadId"},
	}
}

func currentTimeReadResponseSchema() Schema {
	return Schema{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title":   "CurrentTimeReadResponse",
		"type":    "object",
		"properties": Schema{
			"currentTimeAt": Schema{
				"description": "Current time as whole Unix seconds.",
				"format":      "int64",
				"type":        "integer",
			},
		},
		"required": []string{"currentTimeAt"},
	}
}

var (
	_ json.Unmarshaler = (*CurrentTimeReadParams)(nil)
	_ json.Unmarshaler = (*CurrentTimeReadResponse)(nil)
)
