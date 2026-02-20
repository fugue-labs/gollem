package core

import (
	"context"
	"encoding/json"
	"testing"
)

type SearchParams struct {
	Query string `json:"query" jsonschema:"description=Search query,required"`
	Limit int    `json:"limit" jsonschema:"description=Max results"`
}

type SearchResult struct {
	Items []string `json:"items"`
	Total int      `json:"total"`
}

func TestFuncToolBasic(t *testing.T) {
	tool := FuncTool[SearchParams]("search", "Search the web",
		func(_ context.Context, _ SearchParams) (*SearchResult, error) {
			return &SearchResult{
				Items: []string{"result1", "result2"},
				Total: 2,
			}, nil
		},
	)

	if tool.Definition.Name != "search" {
		t.Errorf("name = %q, want 'search'", tool.Definition.Name)
	}
	if tool.Definition.Description != "Search the web" {
		t.Errorf("description = %q, want 'Search the web'", tool.Definition.Description)
	}
	if tool.Definition.Kind != ToolKindFunction {
		t.Errorf("kind = %q, want 'function'", tool.Definition.Kind)
	}

	// Check schema was generated.
	props, ok := tool.Definition.ParametersSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in schema")
	}
	if _, ok := props["query"]; !ok {
		t.Error("expected 'query' in schema properties")
	}
	if _, ok := props["limit"]; !ok {
		t.Error("expected 'limit' in schema properties")
	}

	// Execute the tool.
	ctx := context.Background()
	rc := &RunContext{}
	result, err := tool.Handler(ctx, rc, `{"query":"test","limit":5}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sr, ok := result.(*SearchResult)
	if !ok {
		t.Fatalf("expected *SearchResult, got %T", result)
	}
	if sr.Total != 2 {
		t.Errorf("total = %d, want 2", sr.Total)
	}
}

func TestFuncToolWithRunContext(t *testing.T) {
	var gotDeps any

	tool := FuncTool[SearchParams]("search", "Search",
		func(_ context.Context, rc *RunContext, params SearchParams) (string, error) {
			gotDeps = rc.Deps
			return "result for: " + params.Query, nil
		},
	)

	ctx := context.Background()
	rc := &RunContext{Deps: "my-deps"}
	result, err := tool.Handler(ctx, rc, `{"query":"hello"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "result for: hello" {
		t.Errorf("result = %q, want 'result for: hello'", result)
	}
	if gotDeps != "my-deps" {
		t.Errorf("deps = %v, want 'my-deps'", gotDeps)
	}
}

func TestFuncToolWithEmptyArgs(t *testing.T) {
	type NoParams struct{}

	tool := FuncTool[NoParams]("ping", "Ping",
		func(_ context.Context, _ NoParams) (string, error) {
			return "pong", nil
		},
	)

	ctx := context.Background()
	result, err := tool.Handler(ctx, &RunContext{}, `{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "pong" {
		t.Errorf("result = %q, want 'pong'", result)
	}
}

func TestFuncToolError(t *testing.T) {
	tool := FuncTool[SearchParams]("search", "Search",
		func(_ context.Context, _ SearchParams) (string, error) {
			return "", NewModelRetryError("try again with different query")
		},
	)

	ctx := context.Background()
	_, err := tool.Handler(ctx, &RunContext{}, `{"query":"bad"}`)
	if err == nil {
		t.Fatal("expected error")
	}
	retryErr, ok := err.(*ModelRetryError)
	if !ok {
		t.Fatalf("expected *ModelRetryError, got %T", err)
	}
	if retryErr.Message != "try again with different query" {
		t.Errorf("message = %q, want 'try again with different query'", retryErr.Message)
	}
}

func TestFuncToolInvalidJSON(t *testing.T) {
	tool := FuncTool[SearchParams]("search", "Search",
		func(_ context.Context, params SearchParams) (string, error) {
			return params.Query, nil
		},
	)

	ctx := context.Background()
	_, err := tool.Handler(ctx, &RunContext{}, `{invalid}`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFuncToolOptions(t *testing.T) {
	tool := FuncTool[SearchParams]("search", "Search",
		func(_ context.Context, _ SearchParams) (string, error) {
			return "", nil
		},
		WithToolSequential(true),
		WithToolMaxRetries(5),
	)

	if !tool.Definition.Sequential {
		t.Error("expected sequential=true")
	}
	if tool.MaxRetries == nil || *tool.MaxRetries != 5 {
		t.Errorf("maxRetries = %v, want 5", tool.MaxRetries)
	}
}

func TestFuncToolResultSerialization(t *testing.T) {
	// Test that complex return types are properly serializable.
	type ComplexResult struct {
		Data map[string][]int `json:"data"`
	}

	tool := FuncTool[SearchParams]("search", "Search",
		func(_ context.Context, _ SearchParams) (*ComplexResult, error) {
			return &ComplexResult{
				Data: map[string][]int{"a": {1, 2, 3}},
			}, nil
		},
	)

	ctx := context.Background()
	result, err := tool.Handler(ctx, &RunContext{}, `{"query":"test"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it can be JSON serialized.
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	if len(b) == 0 {
		t.Error("expected non-empty JSON")
	}
}

func TestFuncToolPanicsOnBadSignature(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for non-function argument")
		}
	}()
	FuncTool[SearchParams]("bad", "bad", "not a function")
}
