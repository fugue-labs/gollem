package modelutil

import (
	"context"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

type sessionlessTestModel struct{}

func (m *sessionlessTestModel) Request(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return &core.ModelResponse{}, nil
}
func (m *sessionlessTestModel) RequestStream(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return nil, nil
}
func (m *sessionlessTestModel) ModelName() string { return "sessionless" }

type clonableTestModel struct {
	id int
}

func (m *clonableTestModel) Request(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	return &core.ModelResponse{}, nil
}
func (m *clonableTestModel) RequestStream(_ context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	return nil, nil
}
func (m *clonableTestModel) ModelName() string { return "clonable" }
func (m *clonableTestModel) NewSession() core.Model {
	return &clonableTestModel{id: m.id + 1}
}

func TestNewSessionModel(t *testing.T) {
	var nilModel core.Model
	if got := NewSessionModel(nilModel); got != nil {
		t.Fatalf("expected nil for nil model, got %T", got)
	}

	base := &sessionlessTestModel{}
	if got := NewSessionModel(base); got != base {
		t.Fatalf("expected non-clonable model to be returned as-is")
	}

	clonable := &clonableTestModel{id: 10}
	got := NewSessionModel(clonable)
	clone, ok := got.(*clonableTestModel)
	if !ok {
		t.Fatalf("expected clonableTestModel clone, got %T", got)
	}
	if clone == clonable {
		t.Fatal("expected a distinct clone instance")
	}
	if clone.id != 11 {
		t.Fatalf("expected clone id 11, got %d", clone.id)
	}
}
