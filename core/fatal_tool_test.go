package core

import (
	"context"
	"errors"
	"testing"
)

func TestFatalToolErrorStopsRunBeforeNextModelRequest(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Agent[string]) error
	}{
		{
			name: "synchronous",
			run: func(agent *Agent[string]) error {
				_, err := agent.Run(context.Background(), "read the clock")
				return err
			},
		},
		{
			name: "streaming",
			run: func(agent *Agent[string]) error {
				stream, err := agent.RunStream(context.Background(), "read the clock")
				if err != nil {
					return err
				}
				_, err = stream.Result()
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := errors.New("client clock unavailable")
			model := NewTestModel(
				ToolCallResponse("current_time", `{}`),
				TextResponse("must not be requested"),
			)
			tool := FuncTool[struct{}]("current_time", "Read the client clock",
				func(context.Context, struct{}) (int64, error) {
					return 0, NewFatalToolError(cause)
				},
				WithToolSequential(true),
			)
			agent := NewAgent[string](model, WithTools[string](tool))

			err := tt.run(agent)
			if err == nil || !errors.Is(err, cause) {
				t.Fatalf("run error = %v, want wrapped fatal cause", err)
			}
			if calls := len(model.Calls()); calls != 1 {
				t.Fatalf("model calls = %d, want 1", calls)
			}
		})
	}
}
