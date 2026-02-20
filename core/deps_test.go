package core

import (
	"context"
	"testing"
)

type testDeps struct {
	DBConn string
	APIKey string
}

func TestGetDeps_TypeSafe(t *testing.T) {
	rc := &RunContext{
		Deps: &testDeps{DBConn: "postgres://localhost", APIKey: "secret"},
	}
	deps := GetDeps[*testDeps](rc)
	if deps.DBConn != "postgres://localhost" {
		t.Errorf("expected postgres://localhost, got %q", deps.DBConn)
	}
	if deps.APIKey != "secret" {
		t.Errorf("expected secret, got %q", deps.APIKey)
	}
}

func TestGetDeps_WrongType(t *testing.T) {
	rc := &RunContext{Deps: "not a struct"}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on type mismatch")
		}
	}()
	GetDeps[*testDeps](rc)
}

func TestTryGetDeps_Success(t *testing.T) {
	rc := &RunContext{Deps: &testDeps{DBConn: "test"}}
	deps, ok := TryGetDeps[*testDeps](rc)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if deps.DBConn != "test" {
		t.Errorf("expected test, got %q", deps.DBConn)
	}
}

func TestTryGetDeps_Missing(t *testing.T) {
	rc := &RunContext{}
	_, ok := TryGetDeps[*testDeps](rc)
	if ok {
		t.Fatal("expected ok=false when deps is nil")
	}
}

func TestWithDeps_InTool(t *testing.T) {
	type Params struct {
		N int `json:"n"`
	}

	var captured *testDeps
	tool := FuncTool[Params]("check_deps", "check deps", func(ctx context.Context, rc *RunContext, p Params) (string, error) {
		captured = GetDeps[*testDeps](rc)
		return "ok", nil
	})

	model := NewTestModel(
		ToolCallResponse("check_deps", `{"n":1}`),
		TextResponse("done"),
	)

	deps := &testDeps{DBConn: "from-agent", APIKey: "key123"}
	agent := NewAgent[string](model,
		WithTools[string](tool),
		WithDeps[string](deps),
	)

	_, err := agent.Run(context.Background(), "test deps")
	if err != nil {
		t.Fatal(err)
	}
	if captured == nil {
		t.Fatal("expected deps to be captured in tool")
	}
	if captured.DBConn != "from-agent" {
		t.Errorf("expected from-agent, got %q", captured.DBConn)
	}
}

func TestGetDeps_StructType(t *testing.T) {
	type SimpleDeps struct {
		Value int
	}
	rc := &RunContext{Deps: SimpleDeps{Value: 42}}
	deps := GetDeps[SimpleDeps](rc)
	if deps.Value != 42 {
		t.Errorf("expected 42, got %d", deps.Value)
	}
}

func TestGetDeps_NilPanics(t *testing.T) {
	rc := &RunContext{}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on nil deps")
		}
	}()
	GetDeps[*testDeps](rc)
}

func TestTryGetDeps_NilRunContext(t *testing.T) {
	_, ok := TryGetDeps[*testDeps](nil)
	if ok {
		t.Fatal("expected ok=false for nil RunContext")
	}
}
