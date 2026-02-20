package mcp

import (
	"encoding/json"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestJSONRPCRequestSerialization(t *testing.T) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
		Params:  nil,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	json.Unmarshal(data, &parsed)

	if parsed["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %v", parsed["jsonrpc"])
	}
	if parsed["id"].(float64) != 1 {
		t.Errorf("expected id 1, got %v", parsed["id"])
	}
	if parsed["method"] != "tools/list" {
		t.Errorf("expected method tools/list, got %v", parsed["method"])
	}
}

func TestJSONRPCRequestWithParams(t *testing.T) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "get_weather",
			"arguments": map[string]any{"city": "NYC"},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	json.Unmarshal(data, &parsed)

	params := parsed["params"].(map[string]any)
	if params["name"] != "get_weather" {
		t.Errorf("expected name get_weather, got %v", params["name"])
	}
	args := params["arguments"].(map[string]any)
	if args["city"] != "NYC" {
		t.Errorf("expected city NYC, got %v", args["city"])
	}
}

func TestJSONRPCResponseParsing(t *testing.T) {
	data := `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"test_tool","description":"A test tool","inputSchema":{"type":"object"}}]}}`

	var resp jsonRPCResponse
	err := json.Unmarshal([]byte(data), &resp)
	if err != nil {
		t.Fatal(err)
	}

	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %s", resp.JSONRPC)
	}
	if resp.ID != 1 {
		t.Errorf("expected id 1, got %d", resp.ID)
	}
	if resp.Error != nil {
		t.Errorf("expected no error, got %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected result")
	}
}

func TestJSONRPCErrorParsing(t *testing.T) {
	data := `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`

	var resp jsonRPCResponse
	err := json.Unmarshal([]byte(data), &resp)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected code -32601, got %d", resp.Error.Code)
	}
	if resp.Error.Message != "Method not found" {
		t.Errorf("expected 'Method not found', got %s", resp.Error.Message)
	}
	if resp.Error.Error() != "JSON-RPC error -32601: Method not found" {
		t.Errorf("unexpected error string: %s", resp.Error.Error())
	}
}

func TestToolParsing(t *testing.T) {
	data := `{"name":"get_weather","description":"Get weather for a location","inputSchema":{"type":"object","properties":{"city":{"type":"string"}}}}`

	var tool Tool
	err := json.Unmarshal([]byte(data), &tool)
	if err != nil {
		t.Fatal(err)
	}

	if tool.Name != "get_weather" {
		t.Errorf("expected name get_weather, got %s", tool.Name)
	}
	if tool.Description != "Get weather for a location" {
		t.Errorf("unexpected description: %s", tool.Description)
	}
	if tool.InputSchema == nil {
		t.Fatal("expected inputSchema")
	}
}

func TestToolResultTextContent(t *testing.T) {
	result := &ToolResult{
		Content: []Content{
			{Type: "text", Text: "Hello"},
			{Type: "text", Text: "World"},
		},
	}
	if result.TextContent() != "Hello\nWorld" {
		t.Errorf("expected 'Hello\\nWorld', got '%s'", result.TextContent())
	}
}

func TestToolResultEmpty(t *testing.T) {
	result := &ToolResult{Content: []Content{}}
	if result.TextContent() != "" {
		t.Errorf("expected empty string, got '%s'", result.TextContent())
	}
}

func TestConvertTool(t *testing.T) {
	mcpTool := Tool{
		Name:        "get_weather",
		Description: "Get weather for a city",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
	}

	// We pass nil for client since we won't invoke the handler.
	tool := convertTool(nil, mcpTool)

	if tool.Definition.Name != "get_weather" {
		t.Errorf("expected name get_weather, got %s", tool.Definition.Name)
	}
	if tool.Definition.Description != "Get weather for a city" {
		t.Errorf("unexpected description: %s", tool.Definition.Description)
	}
	if tool.Definition.Kind != core.ToolKindFunction {
		t.Errorf("expected ToolKindFunction, got %s", tool.Definition.Kind)
	}
	if tool.Handler == nil {
		t.Error("expected handler to be set")
	}

	// Check schema was parsed.
	schema := tool.Definition.ParametersSchema
	if schema["type"] != "object" {
		t.Errorf("expected type object, got %v", schema["type"])
	}
}

func TestConvertToolNilSchema(t *testing.T) {
	mcpTool := Tool{
		Name: "simple_tool",
	}

	tool := convertTool(nil, mcpTool)
	schema := tool.Definition.ParametersSchema
	if schema["type"] != "object" {
		t.Errorf("expected default type object, got %v", schema["type"])
	}
}

func TestToolsListResultParsing(t *testing.T) {
	data := `{"tools":[{"name":"tool1","description":"First tool","inputSchema":{"type":"object"}},{"name":"tool2","description":"Second tool","inputSchema":{"type":"object"}}]}`

	var result struct {
		Tools []Tool `json:"tools"`
	}
	err := json.Unmarshal([]byte(data), &result)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "tool1" {
		t.Errorf("expected tool1, got %s", result.Tools[0].Name)
	}
	if result.Tools[1].Name != "tool2" {
		t.Errorf("expected tool2, got %s", result.Tools[1].Name)
	}
}

func TestInitializeParamsSerialization(t *testing.T) {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "gollem",
			"version": "1.0.0",
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	json.Unmarshal(data, &parsed)

	if parsed["protocolVersion"] != "2024-11-05" {
		t.Errorf("unexpected protocol version: %v", parsed["protocolVersion"])
	}
	clientInfo := parsed["clientInfo"].(map[string]any)
	if clientInfo["name"] != "gollem" {
		t.Errorf("expected client name gollem, got %v", clientInfo["name"])
	}
}
