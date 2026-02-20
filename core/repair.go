package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RepairFunc takes malformed raw output and the parse/validation error,
// and returns a repaired output of type T. Typically calls a model to fix the output.
type RepairFunc[T any] func(ctx context.Context, raw string, parseErr error) (T, error)

// WithOutputRepair sets a repair function that is called when output parsing fails.
// The repair function gets one chance to fix the output before the normal retry flow.
func WithOutputRepair[T any](repair RepairFunc[T]) AgentOption[T] {
	return func(a *Agent[T]) {
		a.repairFunc = any(repair)
	}
}

// ModelRepair creates a RepairFunc that sends the malformed output to a model
// with instructions to fix it. The model response is then parsed as T.
func ModelRepair[T any](model Model) RepairFunc[T] {
	return func(ctx context.Context, raw string, parseErr error) (T, error) {
		var zero T

		prompt := fmt.Sprintf(
			"The following output failed to parse with error: %s\n\nMalformed output:\n%s\n\nPlease fix the output to be valid JSON matching the expected schema. Return only the corrected JSON, nothing else.",
			parseErr.Error(), raw,
		)

		req := ModelRequest{
			Parts: []ModelRequestPart{
				UserPromptPart{Content: prompt, Timestamp: time.Now()},
			},
			Timestamp: time.Now(),
		}

		resp, err := model.Request(ctx, []ModelMessage{req}, nil, &ModelRequestParameters{
			AllowTextOutput: true,
		})
		if err != nil {
			return zero, fmt.Errorf("repair model request failed: %w", err)
		}

		text := resp.TextContent()
		if text == "" {
			return zero, errors.New("repair model returned empty response")
		}

		var result T
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			return zero, fmt.Errorf("repair model output still invalid: %w", err)
		}
		return result, nil
	}
}
