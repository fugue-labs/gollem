package modelutil

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestFallbackModel_PrimarySucceeds(t *testing.T) {
	primary := core.NewTestModel(core.TextResponse("primary response"))
	fallback := core.NewTestModel(core.TextResponse("fallback response"))

	m := NewFallbackModel(primary, fallback)

	resp, err := m.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TextContent() != "primary response" {
		t.Errorf("expected primary response, got %q", resp.TextContent())
	}
}

func TestFallbackModel_PrimaryFails(t *testing.T) {
	primary := &failingModel{err: errors.New("primary unavailable")}
	fallback := core.NewTestModel(core.TextResponse("fallback response"))

	m := NewFallbackModel(primary, fallback)

	resp, err := m.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TextContent() != "fallback response" {
		t.Errorf("expected fallback response, got %q", resp.TextContent())
	}
}

func TestFallbackModel_AllFail(t *testing.T) {
	m := NewFallbackModel(
		&failingModel{err: errors.New("error 1")},
		&failingModel{err: errors.New("error 2")},
	)

	_, err := m.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err == nil {
		t.Fatal("expected error when all models fail")
	}
	// The last error should be wrapped in the returned error.
	if !errors.Is(err, errors.Unwrap(err)) {
		// Just verify the error message is meaningful.
		if err.Error() == "" {
			t.Error("expected non-empty error message")
		}
	}
	// Verify the error message mentions all models failed.
	expected := "all models failed, last error: error 2"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestFallbackModel_StreamFallback(t *testing.T) {
	primary := &failingModel{err: errors.New("stream unavailable")}
	fallback := core.NewTestModel(core.TextResponse("streamed fallback"))

	m := NewFallbackModel(primary, fallback)

	stream, err := m.RequestStream(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer stream.Close()

	// Consume the stream until EOF.
	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
	}

	// After consuming the stream, Response() should have the final data.
	resp := stream.Response()
	if resp.TextContent() != "streamed fallback" {
		t.Errorf("expected 'streamed fallback', got %q", resp.TextContent())
	}
}

func TestFallbackModel_StreamAllFail(t *testing.T) {
	m := NewFallbackModel(
		&failingModel{err: errors.New("stream error 1")},
		&failingModel{err: errors.New("stream error 2")},
	)

	_, err := m.RequestStream(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err == nil {
		t.Fatal("expected error when all models fail for streaming")
	}
	expected := "all models failed, last error: stream error 2"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestFallbackModel_ModelName(t *testing.T) {
	primary := core.NewTestModel(core.TextResponse(""))
	fallback := core.NewTestModel(core.TextResponse(""))

	m := NewFallbackModel(primary, fallback)
	name := m.ModelName()
	if name != "test-model+fallback" {
		t.Errorf("expected 'test-model+fallback', got %q", name)
	}
}

func TestFallbackModel_ModelNameEmpty(t *testing.T) {
	m := &FallbackModel{models: nil}
	name := m.ModelName()
	if name != "fallback" {
		t.Errorf("expected 'fallback', got %q", name)
	}
}

func TestFallbackModel_ThreeModels(t *testing.T) {
	m := NewFallbackModel(
		&failingModel{err: errors.New("fail 1")},
		&failingModel{err: errors.New("fail 2")},
		core.NewTestModel(core.TextResponse("third model works")),
	)

	resp, err := m.Request(context.Background(), nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TextContent() != "third model works" {
		t.Errorf("expected 'third model works', got %q", resp.TextContent())
	}
}

func TestFallbackModel_InterfaceCompliance(t *testing.T) {
	// Verify FallbackModel satisfies the Model interface.
	var _ core.Model = (*FallbackModel)(nil)
}

// Regression: FallbackModel did not check ctx.Err() between models, wasting
// time trying remaining models after context cancellation.
func TestFallbackModel_RespectsContextCancellation(t *testing.T) {
	callCount := 0
	slow := &countingFailModel{callFn: func() error {
		callCount++
		return errors.New("fail")
	}}

	m := NewFallbackModel(slow, slow, slow)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before requesting — all models should be skipped.
	cancel()

	_, err := m.Request(ctx, nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if callCount > 0 {
		t.Errorf("expected 0 model calls after context cancellation, got %d", callCount)
	}
}

func TestFallbackModel_StreamRespectsContextCancellation(t *testing.T) {
	callCount := 0
	slow := &countingFailModel{callFn: func() error {
		callCount++
		return errors.New("fail")
	}}

	m := NewFallbackModel(slow, slow, slow)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.RequestStream(ctx, nil, nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if callCount > 0 {
		t.Errorf("expected 0 model calls after context cancellation, got %d", callCount)
	}
}

type countingFailModel struct {
	callFn func() error
}

func (c *countingFailModel) Request(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return nil, c.callFn()
}

func (c *countingFailModel) RequestStream(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return nil, c.callFn()
}

func (c *countingFailModel) ModelName() string { return "counting-fail-model" }

// failingModel is a model that always returns an error.
type failingModel struct {
	err error
}

func (f *failingModel) Request(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return nil, f.err
}

func (f *failingModel) RequestStream(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return nil, f.err
}

func (f *failingModel) ModelName() string {
	return "failing-model"
}
