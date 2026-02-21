package monty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	montygo "github.com/fugue-labs/monty-go"

	"github.com/fugue-labs/gollem/core"
)

// contextKey is an unexported type for context keys in this package.
type contextKey int

const runContextKey contextKey = iota

// codeParams is the schema for the execute_code tool.
type codeParams struct {
	Code string `json:"code" jsonschema:"description=Python code to execute. Call the available functions directly. The last expression is the return value."`
}

// Option configures a CodeMode.
type Option func(*config)

type config struct {
	toolName      string
	limits        montygo.Limits
	capturePrints bool
}

// WithToolName sets the name of the code execution tool (default: "execute_code").
func WithToolName(name string) Option {
	return func(c *config) { c.toolName = name }
}

// WithLimits sets WASM resource limits for Python execution.
func WithLimits(l montygo.Limits) Option {
	return func(c *config) { c.limits = l }
}

// WithCapturePrints controls whether Python print() output is captured and
// included in the result. Default is true.
func WithCapturePrints(b bool) Option {
	return func(c *config) { c.capturePrints = b }
}

// CodeMode wraps a monty-go Runner and a set of gollem tools, presenting
// them to the LLM as a single code execution tool. The LLM writes Python
// that calls the wrapped tools as functions; monty-go executes the Python
// in a WASM sandbox, pausing at each function call so the corresponding
// gollem tool handler runs.
//
// CodeMode is safe for concurrent use; a mutex serializes calls to the
// underlying monty-go Runner (WASM instances are single-threaded).
type CodeMode struct {
	mu            sync.Mutex
	runner        *montygo.Runner
	tools         map[string]*core.Tool
	schemas       map[string]core.Schema
	funcDefs      []montygo.FuncDef
	toolName      string
	limits        montygo.Limits
	capturePrints bool
}

// New creates a CodeMode from a monty-go Runner and gollem tools. Each tool
// becomes a Python function that the LLM can call from code. Tools with
// RequiresApproval are excluded (code execution cannot pause for human
// approval mid-script).
func New(runner *montygo.Runner, tools []core.Tool, opts ...Option) *CodeMode {
	cfg := &config{
		toolName:      "execute_code",
		capturePrints: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	toolMap := make(map[string]*core.Tool, len(tools))
	schemas := make(map[string]core.Schema, len(tools))
	funcDefs := make([]montygo.FuncDef, 0, len(tools))

	for _, t := range tools {
		if t.RequiresApproval {
			continue
		}
		tc := t
		name := tc.Definition.Name
		toolMap[name] = &tc
		schema := tc.Definition.ParametersSchema
		schemas[name] = schema
		funcDefs = append(funcDefs, montygo.FuncDef{
			Name:   name,
			Params: extractParamNames(schema),
		})
	}

	return &CodeMode{
		runner:        runner,
		tools:         toolMap,
		schemas:       schemas,
		funcDefs:      funcDefs,
		toolName:      cfg.toolName,
		limits:        cfg.limits,
		capturePrints: cfg.capturePrints,
	}
}

// Tool returns the code execution tool for agent registration.
func (cm *CodeMode) Tool() core.Tool {
	return core.Tool{
		Definition: core.ToolDefinition{
			Name:             cm.toolName,
			Description:      "Execute Python code that calls the available functions. The last expression is the return value.",
			ParametersSchema: core.SchemaFor[codeParams](),
			Kind:             core.ToolKindFunction,
		},
		Handler: cm.handler,
	}
}

// SystemPrompt returns a system prompt fragment describing the available
// Python functions and their signatures. Append this to your agent's system
// prompt so the LLM knows what functions are available in code mode.
func (cm *CodeMode) SystemPrompt() string {
	return buildSystemPrompt(cm.toolName, cm.tools, cm.schemas)
}

func (cm *CodeMode) handler(ctx context.Context, rc *core.RunContext, argsJSON string) (any, error) {
	var params codeParams
	if err := json.Unmarshal([]byte(argsJSON), &params); err != nil {
		return nil, fmt.Errorf("failed to parse code tool arguments: %w", err)
	}
	if params.Code == "" {
		return nil, errors.New("code parameter is required")
	}

	// Serialize access to the WASM runner (single-threaded).
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Store RunContext in context for the external function callback.
	ctx = context.WithValue(ctx, runContextKey, rc)

	opts := []montygo.ExecuteOption{
		montygo.WithExternalFunc(cm.externalFunc, cm.funcDefs...),
	}

	if cm.limits != (montygo.Limits{}) {
		opts = append(opts, montygo.WithLimits(cm.limits))
	}

	if cm.capturePrints {
		var prints strings.Builder
		opts = append(opts, montygo.WithPrintFunc(func(s string) {
			prints.WriteString(s)
		}))
		result, err := cm.runner.Execute(ctx, params.Code, nil, opts...)
		if err != nil {
			return nil, err
		}
		if prints.Len() > 0 {
			return map[string]any{
				"result": result,
				"stdout": prints.String(),
			}, nil
		}
		return result, nil
	}

	return cm.runner.Execute(ctx, params.Code, nil, opts...)
}

func (cm *CodeMode) externalFunc(ctx context.Context, call *montygo.FunctionCall) (any, error) {
	tool, ok := cm.tools[call.Name]
	if !ok {
		return nil, fmt.Errorf("unknown function: %s", call.Name)
	}
	rc, _ := ctx.Value(runContextKey).(*core.RunContext)
	return tool.Handler(ctx, rc, call.ArgsJSON())
}
