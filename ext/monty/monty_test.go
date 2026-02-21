package monty

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	montygo "github.com/fugue-labs/monty-go"

	"github.com/fugue-labs/gollem/core"
)

// newRunner creates a monty-go Runner for tests.
func newRunner(t *testing.T) *montygo.Runner {
	t.Helper()
	runner, err := montygo.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.Close() })
	return runner
}

type searchParams struct {
	Query string `json:"query"`
}

type calcParams struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
}

func searchHandler(_ context.Context, p searchParams) (any, error) {
	return map[string]any{"query": p.Query, "count": float64(3)}, nil
}

func calcHandler(_ context.Context, p calcParams) (any, error) {
	return p.A + p.B, nil
}

func TestNew(t *testing.T) {
	runner := newRunner(t)

	search := core.FuncTool[searchParams]("search", "Search things", searchHandler)
	calc := core.FuncTool[calcParams]("add", "Add numbers", calcHandler)

	cm := New(runner, []core.Tool{search, calc})

	if cm.toolName != "execute_code" {
		t.Errorf("expected toolName execute_code, got %s", cm.toolName)
	}
	if len(cm.tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(cm.tools))
	}
	if len(cm.funcDefs) != 2 {
		t.Errorf("expected 2 funcDefs, got %d", len(cm.funcDefs))
	}
}

func TestNewSkipsApprovalTools(t *testing.T) {
	runner := newRunner(t)

	search := core.FuncTool[searchParams]("search", "Search", searchHandler)
	dangerous := core.FuncTool[searchParams]("delete_all", "Delete everything", searchHandler, core.WithRequiresApproval())

	cm := New(runner, []core.Tool{search, dangerous})

	if len(cm.tools) != 1 {
		t.Errorf("expected 1 tool (approval tool excluded), got %d", len(cm.tools))
	}
	if _, ok := cm.tools["search"]; !ok {
		t.Error("search tool should be present")
	}
	if _, ok := cm.tools["delete_all"]; ok {
		t.Error("delete_all tool should be excluded")
	}
}

func TestToolDefinition(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil)
	tool := cm.Tool()

	if tool.Definition.Name != "execute_code" {
		t.Errorf("expected execute_code, got %s", tool.Definition.Name)
	}
	if tool.Definition.Kind != core.ToolKindFunction {
		t.Error("expected function kind")
	}
	if tool.Handler == nil {
		t.Error("handler should not be nil")
	}
}

func TestWithToolName(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil, WithToolName("run_python"))

	if cm.toolName != "run_python" {
		t.Errorf("expected run_python, got %s", cm.toolName)
	}
	tool := cm.Tool()
	if tool.Definition.Name != "run_python" {
		t.Errorf("expected tool name run_python, got %s", tool.Definition.Name)
	}
}

func TestSystemPrompt(t *testing.T) {
	runner := newRunner(t)

	search := core.FuncTool[searchParams]("search", "Search the knowledge base", searchHandler)
	cm := New(runner, []core.Tool{search})

	prompt := cm.SystemPrompt()

	if !strings.Contains(prompt, "execute_code") {
		t.Error("prompt should mention tool name")
	}
	if !strings.Contains(prompt, "search") {
		t.Error("prompt should mention search function")
	}
	if !strings.Contains(prompt, "def search") {
		t.Error("prompt should contain Python signature")
	}
	if !strings.Contains(prompt, "Search the knowledge base") {
		t.Error("prompt should contain tool description")
	}
}

func TestExecuteSimple(t *testing.T) {
	runner := newRunner(t)

	search := core.FuncTool[searchParams]("search", "Search", searchHandler)
	cm := New(runner, []core.Tool{search})
	tool := cm.Tool()

	args, _ := json.Marshal(codeParams{Code: `search(query="hello")`})
	result, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err != nil {
		t.Fatal(err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T: %v", result, result)
	}
	if m["query"] != "hello" {
		t.Errorf("expected query=hello, got %v", m["query"])
	}
}

func TestExecuteMultipleTools(t *testing.T) {
	runner := newRunner(t)

	search := core.FuncTool[searchParams]("search", "Search", searchHandler)
	calc := core.FuncTool[calcParams]("add", "Add", calcHandler)
	cm := New(runner, []core.Tool{search, calc})
	tool := cm.Tool()

	code := `
r = search(query="test")
total = add(a=r["count"], b=10)
total`
	args, _ := json.Marshal(codeParams{Code: code})
	result, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err != nil {
		t.Fatal(err)
	}

	if result != float64(13) {
		t.Errorf("expected 13, got %v (%T)", result, result)
	}
}

func TestExecuteWithPrintCapture(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil)
	tool := cm.Tool()

	args, _ := json.Marshal(codeParams{Code: "print(\"hello world\")\n42"})
	result, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err != nil {
		t.Fatal(err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map with stdout, got %T: %v", result, result)
	}
	if m["result"] != float64(42) {
		t.Errorf("expected result=42, got %v", m["result"])
	}
	stdout, _ := m["stdout"].(string)
	if stdout != "hello world\n" {
		t.Errorf("expected stdout='hello world\\n', got %q", stdout)
	}
}

func TestExecuteNoPrintOutput(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil)
	tool := cm.Tool()

	args, _ := json.Marshal(codeParams{Code: "42"})
	result, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err != nil {
		t.Fatal(err)
	}

	// No print output — result should be returned directly, not wrapped.
	if result != float64(42) {
		t.Errorf("expected 42, got %v (%T)", result, result)
	}
}

func TestCapturePrintsDisabled(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil, WithCapturePrints(false))
	tool := cm.Tool()

	args, _ := json.Marshal(codeParams{Code: "print(\"ignored\")\n42"})
	result, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err != nil {
		t.Fatal(err)
	}

	// With prints disabled, result is returned directly.
	if result != float64(42) {
		t.Errorf("expected 42, got %v (%T)", result, result)
	}
}

func TestEmptyCode(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil)
	tool := cm.Tool()

	args, _ := json.Marshal(codeParams{Code: ""})
	_, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err == nil {
		t.Error("expected error for empty code")
	}
}

func TestInvalidArgs(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil)
	tool := cm.Tool()

	_, err := tool.Handler(context.Background(), &core.RunContext{}, "not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestUnknownFunction(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil)
	tool := cm.Tool()

	args, _ := json.Marshal(codeParams{Code: `nonexistent()`})
	_, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err == nil {
		t.Error("expected error for unknown function")
	}
}

func TestRunContextPropagation(t *testing.T) {
	runner := newRunner(t)

	var captured *core.RunContext
	rcTool := core.FuncTool[searchParams]("check_rc", "Check RunContext",
		func(ctx context.Context, rc *core.RunContext, p searchParams) (any, error) {
			captured = rc
			return "ok", nil
		},
	)

	cm := New(runner, []core.Tool{rcTool})
	tool := cm.Tool()

	rc := &core.RunContext{Prompt: "test prompt", RunStep: 5}
	args, _ := json.Marshal(codeParams{Code: `check_rc(query="test")`})
	_, err := tool.Handler(context.Background(), rc, string(args))
	if err != nil {
		t.Fatal(err)
	}

	if captured == nil {
		t.Fatal("RunContext was not propagated")
	}
	if captured.Prompt != "test prompt" {
		t.Errorf("expected prompt 'test prompt', got %q", captured.Prompt)
	}
	if captured.RunStep != 5 {
		t.Errorf("expected RunStep 5, got %d", captured.RunStep)
	}
}

func TestToolChaining(t *testing.T) {
	runner := newRunner(t)

	upper := core.FuncTool[searchParams]("upper", "Uppercase",
		func(_ context.Context, p searchParams) (any, error) {
			return strings.ToUpper(p.Query), nil
		},
	)
	repeat := core.FuncTool[struct {
		Text  string `json:"text"`
		Times int    `json:"times"`
	}]("repeat", "Repeat text",
		func(_ context.Context, p struct {
			Text  string
			Times int
		}) (any, error) {
			return strings.Repeat(p.Text, p.Times), nil
		},
	)

	cm := New(runner, []core.Tool{upper, repeat})
	tool := cm.Tool()

	code := `
result = upper(query="hello")
repeat(text=result, times=3)`
	args, _ := json.Marshal(codeParams{Code: code})
	result, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err != nil {
		t.Fatal(err)
	}
	if result != "HELLOHELLOHELLO" {
		t.Errorf("expected HELLOHELLOHELLO, got %v", result)
	}
}

func TestToolErrorPropagation(t *testing.T) {
	runner := newRunner(t)

	failing := core.FuncTool[searchParams]("fail", "Always fails",
		func(_ context.Context, p searchParams) (any, error) {
			return nil, fmt.Errorf("tool error: %s", p.Query)
		},
	)

	cm := New(runner, []core.Tool{failing})
	tool := cm.Tool()

	args, _ := json.Marshal(codeParams{Code: `fail(query="boom")`})
	_, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err == nil {
		t.Error("expected error from failing tool")
	}
}

func TestNoTools(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil)
	tool := cm.Tool()

	// Pure Python computation, no external functions.
	args, _ := json.Marshal(codeParams{Code: "[x * 2 for x in range(5)]"})
	result, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err != nil {
		t.Fatal(err)
	}

	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T: %v", result, result)
	}
	expected := []float64{0, 2, 4, 6, 8}
	if len(list) != len(expected) {
		t.Fatalf("expected %d elements, got %d", len(expected), len(list))
	}
	for i, v := range list {
		if v != expected[i] {
			t.Errorf("element %d: expected %v, got %v", i, expected[i], v)
		}
	}
}

func TestExtractParamNames(t *testing.T) {
	schema := core.Schema{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
			"beta":  map[string]any{"type": "boolean"},
		},
		"required": []string{"query"},
	}

	names := extractParamNames(schema)

	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d: %v", len(names), names)
	}
	if names[0] != "query" {
		t.Errorf("expected first param 'query', got %s", names[0])
	}
	// Optional params should be sorted alphabetically.
	if names[1] != "beta" || names[2] != "limit" {
		t.Errorf("expected optional params [beta, limit], got %v", names[1:])
	}
}

func TestExtractParamNamesNoProperties(t *testing.T) {
	schema := core.Schema{"type": "object"}
	names := extractParamNames(schema)
	if names != nil {
		t.Errorf("expected nil, got %v", names)
	}
}

func TestGeneratePythonSignature(t *testing.T) {
	schema := core.Schema{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
		},
		"required": []string{"query"},
	}

	sig := generatePythonSignature("search", schema)
	expected := "def search(query: str, limit: int = None) -> Any"
	if sig != expected {
		t.Errorf("expected %q, got %q", expected, sig)
	}
}

func TestGeneratePythonSignatureNoParams(t *testing.T) {
	schema := core.Schema{"type": "object"}
	sig := generatePythonSignature("noop", schema)
	if sig != "def noop() -> Any" {
		t.Errorf("expected 'def noop() -> Any', got %q", sig)
	}
}

func TestWithLimits(t *testing.T) {
	runner := newRunner(t)

	cm := New(runner, nil, WithLimits(montygo.Limits{
		MaxRecursionDepth: 2,
	}))
	tool := cm.Tool()

	// Deep recursion should exceed the limit.
	code := `
def f(n):
    if n == 0:
        return 0
    return f(n - 1)
f(100)`
	args, _ := json.Marshal(codeParams{Code: code})
	_, err := tool.Handler(context.Background(), &core.RunContext{}, string(args))
	if err == nil {
		t.Error("expected error from recursion limit")
	}
}

func TestContextCancellation(t *testing.T) {
	runner := newRunner(t)

	// Tool that blocks until context is cancelled.
	slow := core.FuncTool[searchParams]("slow", "Slow tool",
		func(ctx context.Context, p searchParams) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)

	cm := New(runner, []core.Tool{slow})
	tool := cm.Tool()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the tool's context is already done.
	cancel()

	args, _ := json.Marshal(codeParams{Code: `slow(query="test")`})
	_, err := tool.Handler(ctx, &core.RunContext{}, string(args))
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}
