package modelutil

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

type profiledModel struct {
	*core.TestModel
	profile ModelProfile
}

func (m *profiledModel) Profile() ModelProfile {
	return m.profile
}

func TestGetProfile_Profiled(t *testing.T) {
	m := &profiledModel{
		TestModel: core.NewTestModel(core.TextResponse("ok")),
		profile: ModelProfile{
			SupportsToolCalls: true,
			SupportsVision:    false,
			MaxContextTokens:  128000,
		},
	}
	p := GetProfile(m)
	if !p.SupportsToolCalls {
		t.Error("expected SupportsToolCalls=true")
	}
	if p.SupportsVision {
		t.Error("expected SupportsVision=false")
	}
	if p.MaxContextTokens != 128000 {
		t.Errorf("expected MaxContextTokens=128000, got %d", p.MaxContextTokens)
	}
}

func TestGetProfile_Default(t *testing.T) {
	m := core.NewTestModel(core.TextResponse("ok"))
	p := GetProfile(m)
	if !p.SupportsToolCalls {
		t.Error("expected default SupportsToolCalls=true")
	}
	if !p.SupportsStructuredOutput {
		t.Error("expected default SupportsStructuredOutput=true")
	}
	if !p.SupportsVision {
		t.Error("expected default SupportsVision=true")
	}
	if !p.SupportsStreaming {
		t.Error("expected default SupportsStreaming=true")
	}
}

func TestCapabilityRouter_Match(t *testing.T) {
	noVision := &profiledModel{
		TestModel: core.NewTestModel(core.TextResponse("no-vision")),
		profile:   ModelProfile{SupportsToolCalls: true, SupportsVision: false},
	}
	withVision := &profiledModel{
		TestModel: core.NewTestModel(core.TextResponse("with-vision")),
		profile:   ModelProfile{SupportsToolCalls: true, SupportsVision: true},
	}

	router := NewCapabilityRouter(
		[]core.Model{noVision, withVision},
		ModelProfile{SupportsVision: true},
	)

	model, err := router.Route(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if model != withVision {
		t.Error("expected withVision model to be selected")
	}
}

func TestCapabilityRouter_NoMatch(t *testing.T) {
	m := &profiledModel{
		TestModel: core.NewTestModel(core.TextResponse("basic")),
		profile:   ModelProfile{SupportsVision: false},
	}

	router := NewCapabilityRouter(
		[]core.Model{m},
		ModelProfile{SupportsVision: true},
	)

	_, err := router.Route(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error when no model matches")
	}
}

func TestModelProfile_JSON(t *testing.T) {
	p := ModelProfile{
		SupportsToolCalls:        true,
		SupportsStructuredOutput: true,
		SupportsVision:           false,
		SupportsStreaming:        true,
		MaxContextTokens:         200000,
		ProviderName:             "anthropic",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}

	var p2 ModelProfile
	if err := json.Unmarshal(data, &p2); err != nil {
		t.Fatal(err)
	}

	if p2.SupportsToolCalls != p.SupportsToolCalls ||
		p2.SupportsVision != p.SupportsVision ||
		p2.MaxContextTokens != p.MaxContextTokens ||
		p2.ProviderName != p.ProviderName {
		t.Errorf("JSON round-trip mismatch: %+v vs %+v", p, p2)
	}
}
