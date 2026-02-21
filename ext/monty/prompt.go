package monty

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fugue-labs/gollem/core"
)

// schemaTypeToPython maps JSON Schema types to Python type hints.
var schemaTypeToPython = map[string]string{
	"string":  "str",
	"integer": "int",
	"number":  "float",
	"boolean": "bool",
	"array":   "list",
	"object":  "dict",
}

// extractParamNames extracts ordered parameter names from a tool's schema.
// Required params come first (in struct declaration order), then optional
// params sorted alphabetically.
func extractParamNames(schema core.Schema) []string {
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return nil
	}

	var required []string
	switch r := schema["required"].(type) {
	case []string:
		required = r
	case []any:
		for _, v := range r {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}

	requiredSet := make(map[string]bool, len(required))
	for _, name := range required {
		requiredSet[name] = true
	}

	var optional []string
	for name := range props {
		if !requiredSet[name] {
			optional = append(optional, name)
		}
	}
	sort.Strings(optional)

	return append(required, optional...)
}

// generatePythonSignature generates a Python function signature from a tool definition.
func generatePythonSignature(name string, schema core.Schema) string {
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return fmt.Sprintf("def %s() -> Any", name)
	}

	requiredSet := make(map[string]bool)
	switch r := schema["required"].(type) {
	case []string:
		for _, s := range r {
			requiredSet[s] = true
		}
	case []any:
		for _, v := range r {
			if s, ok := v.(string); ok {
				requiredSet[s] = true
			}
		}
	}

	paramNames := extractParamNames(schema)
	var params []string
	for _, pName := range paramNames {
		propSchema, ok := props[pName].(map[string]any)
		if !ok {
			params = append(params, pName)
			continue
		}
		typeStr := "Any"
		if t, ok := propSchema["type"].(string); ok {
			if pyType, ok := schemaTypeToPython[t]; ok {
				typeStr = pyType
			}
		}
		if requiredSet[pName] {
			params = append(params, fmt.Sprintf("%s: %s", pName, typeStr))
		} else {
			params = append(params, fmt.Sprintf("%s: %s = None", pName, typeStr))
		}
	}

	return fmt.Sprintf("def %s(%s) -> Any", name, strings.Join(params, ", "))
}

// buildSystemPrompt generates a system prompt fragment describing available
// Python functions and their signatures.
func buildSystemPrompt(toolName string, tools map[string]*core.Tool, schemas map[string]core.Schema) string {
	var b strings.Builder

	b.WriteString("## Code Execution\n\n")
	fmt.Fprintf(&b, "You have an `%s` tool that runs Python code. ", toolName)
	b.WriteString("Write Python code that calls the available functions below. ")
	b.WriteString("The last expression in the code is the return value.\n\n")
	b.WriteString("### Available Functions\n\n")

	// Sort tool names for deterministic output.
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		tool := tools[name]
		schema := schemas[name]

		fmt.Fprintf(&b, "**%s**", name)
		if tool.Definition.Description != "" {
			b.WriteString(" - " + tool.Definition.Description)
		}
		b.WriteString("\n```python\n")
		b.WriteString(generatePythonSignature(name, schema))
		b.WriteString("\n```\n\n")
	}

	b.WriteString("### Usage Notes\n\n")
	b.WriteString("- Always use keyword arguments: `search(query=\"test\")` not `search(\"test\")`\n")
	b.WriteString("- Call multiple functions in one code block to avoid round trips\n")
	b.WriteString("- Use try/except for error handling\n")
	b.WriteString("- Functions return Python objects (dicts, lists, strings, numbers)\n")

	return b.String()
}
