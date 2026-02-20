package core

import (
	"context"
	"testing"
)

func TestClassifierRouter(t *testing.T) {
	simpleModel := NewTestModel(TextResponse("simple answer"))
	simpleModel.name = "simple"
	complexModel := NewTestModel(TextResponse("complex answer"))
	complexModel.name = "complex"

	router := ClassifierRouter(
		map[string]Model{"simple": simpleModel, "complex": complexModel},
		func(ctx context.Context, prompt string) string {
			if len(prompt) > 20 {
				return "complex"
			}
			return "simple"
		},
	)

	model, err := router.Route(context.Background(), "short")
	if err != nil {
		t.Fatal(err)
	}
	if model.ModelName() != "simple" {
		t.Errorf("expected simple model, got %s", model.ModelName())
	}

	model, err = router.Route(context.Background(), "this is a much longer prompt that needs complex model")
	if err != nil {
		t.Fatal(err)
	}
	if model.ModelName() != "complex" {
		t.Errorf("expected complex model, got %s", model.ModelName())
	}
}

func TestThresholdRouter_Simple(t *testing.T) {
	simpleModel := NewTestModel(TextResponse("simple"))
	simpleModel.name = "simple"
	complexModel := NewTestModel(TextResponse("complex"))
	complexModel.name = "complex"

	router := ThresholdRouter(simpleModel, complexModel, 20)

	model, err := router.Route(context.Background(), "short")
	if err != nil {
		t.Fatal(err)
	}
	if model.ModelName() != "simple" {
		t.Errorf("expected simple, got %s", model.ModelName())
	}
}

func TestThresholdRouter_Complex(t *testing.T) {
	simpleModel := NewTestModel(TextResponse("simple"))
	simpleModel.name = "simple"
	complexModel := NewTestModel(TextResponse("complex"))
	complexModel.name = "complex"

	router := ThresholdRouter(simpleModel, complexModel, 10)

	model, err := router.Route(context.Background(), "this is a long prompt exceeding the threshold")
	if err != nil {
		t.Fatal(err)
	}
	if model.ModelName() != "complex" {
		t.Errorf("expected complex, got %s", model.ModelName())
	}
}

func TestRoundRobinRouter(t *testing.T) {
	m1 := NewTestModel(TextResponse("a"))
	m1.name = "m1"
	m2 := NewTestModel(TextResponse("b"))
	m2.name = "m2"
	m3 := NewTestModel(TextResponse("c"))
	m3.name = "m3"

	router := RoundRobinRouter(m1, m2, m3)

	expected := []string{"m1", "m2", "m3", "m1", "m2", "m3"}
	for i, exp := range expected {
		model, err := router.Route(context.Background(), "test")
		if err != nil {
			t.Fatal(err)
		}
		if model.ModelName() != exp {
			t.Errorf("call %d: expected %s, got %s", i, exp, model.ModelName())
		}
	}
}

func TestRouterModel_Request(t *testing.T) {
	simpleModel := NewTestModel(TextResponse("simple answer"))
	simpleModel.name = "simple"
	complexModel := NewTestModel(TextResponse("complex answer"))
	complexModel.name = "complex"

	router := ThresholdRouter(simpleModel, complexModel, 10)
	model := NewRouterModel(router)

	agent := NewAgent[string](model)

	result, err := agent.Run(context.Background(), "short")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "simple answer" {
		t.Errorf("expected 'simple answer', got %q", result.Output)
	}

	// Verify the simple model was called.
	if len(simpleModel.Calls()) == 0 {
		t.Error("expected simple model to be called")
	}
}

func TestRouterModel_Streaming(t *testing.T) {
	simpleModel := NewTestModel(TextResponse("streamed"))
	simpleModel.name = "simple"
	complexModel := NewTestModel(TextResponse("complex"))
	complexModel.name = "complex"

	router := ThresholdRouter(simpleModel, complexModel, 10)
	model := NewRouterModel(router)

	// Test that RequestStream delegates correctly.
	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "short"}}},
	}
	stream, err := model.RequestStream(context.Background(), messages, nil, &ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	resp := stream.Response()
	if resp.TextContent() != "streamed" {
		t.Errorf("expected 'streamed', got %q", resp.TextContent())
	}
}
