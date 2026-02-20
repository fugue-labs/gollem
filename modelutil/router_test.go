package modelutil

import (
	"context"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestClassifierRouter(t *testing.T) {
	simpleModel := core.NewTestModel(core.TextResponse("simple answer"))
	simpleModel.SetName("simple")
	complexModel := core.NewTestModel(core.TextResponse("complex answer"))
	complexModel.SetName("complex")

	router := ClassifierRouter(
		map[string]core.Model{"simple": simpleModel, "complex": complexModel},
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
	simpleModel := core.NewTestModel(core.TextResponse("simple"))
	simpleModel.SetName("simple")
	complexModel := core.NewTestModel(core.TextResponse("complex"))
	complexModel.SetName("complex")

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
	simpleModel := core.NewTestModel(core.TextResponse("simple"))
	simpleModel.SetName("simple")
	complexModel := core.NewTestModel(core.TextResponse("complex"))
	complexModel.SetName("complex")

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
	m1 := core.NewTestModel(core.TextResponse("a"))
	m1.SetName("m1")
	m2 := core.NewTestModel(core.TextResponse("b"))
	m2.SetName("m2")
	m3 := core.NewTestModel(core.TextResponse("c"))
	m3.SetName("m3")

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
	simpleModel := core.NewTestModel(core.TextResponse("simple answer"))
	simpleModel.SetName("simple")
	complexModel := core.NewTestModel(core.TextResponse("complex answer"))
	complexModel.SetName("complex")

	router := ThresholdRouter(simpleModel, complexModel, 10)
	model := NewRouterModel(router)

	agent := core.NewAgent[string](model)

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
	simpleModel := core.NewTestModel(core.TextResponse("streamed"))
	simpleModel.SetName("simple")
	complexModel := core.NewTestModel(core.TextResponse("complex"))
	complexModel.SetName("complex")

	router := ThresholdRouter(simpleModel, complexModel, 10)
	model := NewRouterModel(router)

	// Test that RequestStream delegates correctly.
	messages := []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "short"}}},
	}
	stream, err := model.RequestStream(context.Background(), messages, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	resp := stream.Response()
	if resp.TextContent() != "streamed" {
		t.Errorf("expected 'streamed', got %q", resp.TextContent())
	}
}
