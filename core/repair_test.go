package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type RepairTestOutput struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestOutputRepair_FixesMalformed(t *testing.T) {
	repairCalled := false
	repair := func(ctx context.Context, raw string, parseErr error) (RepairTestOutput, error) {
		repairCalled = true
		// Fix the malformed JSON.
		return RepairTestOutput{Name: "fixed", Value: 42}, nil
	}

	// Model returns malformed JSON via tool call.
	model := NewTestModel(
		ToolCallResponse("final_result", `{"name": "broken", value: bad}`), // malformed
		TextResponse("done"),
	)
	agent := NewAgent[RepairTestOutput](model,
		WithOutputRepair(repair),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if !repairCalled {
		t.Error("repair function was not called")
	}
	if result.Output.Name != "fixed" || result.Output.Value != 42 {
		t.Errorf("unexpected output: %+v", result.Output)
	}
}

func TestOutputRepair_RepairFails(t *testing.T) {
	repair := func(ctx context.Context, raw string, parseErr error) (RepairTestOutput, error) {
		return RepairTestOutput{}, errors.New("repair also failed")
	}

	// Model returns malformed JSON, then valid JSON after retry.
	validJSON, _ := json.Marshal(RepairTestOutput{Name: "retried", Value: 99})
	model := NewTestModel(
		ToolCallResponse("final_result", `{bad json}`),
		ToolCallResponse("final_result", string(validJSON)),
	)
	agent := NewAgent[RepairTestOutput](model,
		WithOutputRepair(repair),
		WithMaxRetries[RepairTestOutput](2),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output.Name != "retried" {
		t.Errorf("expected retried output, got %+v", result.Output)
	}
}

func TestOutputRepair_NotNeeded(t *testing.T) {
	repairCalled := false
	repair := func(ctx context.Context, raw string, parseErr error) (RepairTestOutput, error) {
		repairCalled = true
		return RepairTestOutput{}, nil
	}

	validJSON, _ := json.Marshal(RepairTestOutput{Name: "valid", Value: 1})
	model := NewTestModel(
		ToolCallResponse("final_result", string(validJSON)),
	)
	agent := NewAgent[RepairTestOutput](model,
		WithOutputRepair(repair),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if repairCalled {
		t.Error("repair should not be called when output is valid")
	}
	if result.Output.Name != "valid" {
		t.Errorf("expected valid output, got %+v", result.Output)
	}
}

func TestModelRepair(t *testing.T) {
	// The repair model returns corrected JSON.
	fixedJSON, _ := json.Marshal(RepairTestOutput{Name: "model-fixed", Value: 7})
	repairModel := NewTestModel(TextResponse(string(fixedJSON)))

	repair := ModelRepair[RepairTestOutput](repairModel)

	model := NewTestModel(
		ToolCallResponse("final_result", `{broken json!!!}`),
	)
	agent := NewAgent[RepairTestOutput](model,
		WithOutputRepair(repair),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output.Name != "model-fixed" {
		t.Errorf("expected model-fixed, got %+v", result.Output)
	}
	// Repair model should have been called.
	if len(repairModel.Calls()) == 0 {
		t.Error("repair model was not called")
	}
}

func TestOutputRepair_WithValidator(t *testing.T) {
	repair := func(ctx context.Context, raw string, parseErr error) (RepairTestOutput, error) {
		return RepairTestOutput{Name: "repaired", Value: 100}, nil
	}

	validator := func(ctx context.Context, rc *RunContext, output RepairTestOutput) (RepairTestOutput, error) {
		if output.Value < 0 {
			return output, NewModelRetryError("value must be non-negative")
		}
		return output, nil
	}

	model := NewTestModel(
		ToolCallResponse("final_result", `{broken}`),
	)
	agent := NewAgent[RepairTestOutput](model,
		WithOutputRepair(repair),
		WithOutputValidator(validator),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output.Name != "repaired" || result.Output.Value != 100 {
		t.Errorf("unexpected output: %+v", result.Output)
	}
}
