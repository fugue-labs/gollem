package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fugue-labs/gollem/core"
)

// memoryToolParams defines the JSON arguments for the memory tool.
type memoryToolParams struct {
	Operation string `json:"operation" jsonschema:"description=The operation to perform,enum=save|get|search|delete"`
	Key       string `json:"key,omitempty" jsonschema:"description=Document key (required for save/get/delete)"`
	Value     string `json:"value,omitempty" jsonschema:"description=JSON string of the value to save (required for save)"`
	Query     string `json:"query,omitempty" jsonschema:"description=Search query string (required for search)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=Maximum number of search results (default 10)"`
}

// MemoryTool creates a tool that gives agents direct access to a memory store.
// The tool supports four operations: save, get, search, and delete.
// An optional namespace scopes all operations to a specific prefix.
func MemoryTool(store Store, namespace ...string) core.Tool {
	handler := func(ctx context.Context, _ *core.RunContext, argsJSON string) (any, error) {
		var params memoryToolParams
		if err := json.Unmarshal([]byte(argsJSON), &params); err != nil {
			return nil, fmt.Errorf("failed to parse memory tool arguments: %w", err)
		}

		switch params.Operation {
		case "save":
			if params.Key == "" {
				return nil, errors.New("key is required for save operation")
			}
			if params.Value == "" {
				return nil, errors.New("value is required for save operation")
			}
			var value any
			if err := json.Unmarshal([]byte(params.Value), &value); err != nil {
				// If not valid JSON, wrap as a string value.
				value = map[string]any{"content": params.Value}
			}
			if err := store.Put(ctx, namespace, params.Key, value); err != nil {
				return nil, err
			}
			return map[string]any{"status": "saved", "key": params.Key}, nil

		case "get":
			if params.Key == "" {
				return nil, errors.New("key is required for get operation")
			}
			doc, err := store.Get(ctx, namespace, params.Key)
			if err != nil {
				return nil, err
			}
			if doc == nil {
				return map[string]any{"status": "not_found", "key": params.Key}, nil
			}
			return doc, nil

		case "search":
			if params.Query == "" {
				return nil, errors.New("query is required for search operation")
			}
			limit := params.Limit
			if limit <= 0 {
				limit = 10
			}
			docs, err := store.Search(ctx, namespace, params.Query, limit)
			if err != nil {
				return nil, err
			}
			return map[string]any{"results": docs, "count": len(docs)}, nil

		case "delete":
			if params.Key == "" {
				return nil, errors.New("key is required for delete operation")
			}
			if err := store.Delete(ctx, namespace, params.Key); err != nil {
				return nil, err
			}
			return map[string]any{"status": "deleted", "key": params.Key}, nil

		default:
			return nil, fmt.Errorf("unknown operation %q, must be one of: save, get, search, delete", params.Operation)
		}
	}

	return core.Tool{
		Definition: core.ToolDefinition{
			Name:        "memory",
			Description: "Store and retrieve information from the persistent memory store. Supports save, get, search, and delete operations.",
			ParametersSchema: core.SchemaFor[memoryToolParams](),
			Kind:             core.ToolKindFunction,
		},
		Handler: handler,
	}
}
