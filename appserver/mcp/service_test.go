package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	extmcp "github.com/fugue-labs/gollem/ext/mcp"
)

type mockSource struct {
	tools             []extmcp.Tool
	toolResults       map[string]*extmcp.ToolResult
	resources         []extmcp.Resource
	resourceTemplates []extmcp.ResourceTemplate
	resourceResults   map[string]*extmcp.ReadResourceResult
	listErr           error
	callErr           error
	resourceErr       error
}

func (m *mockSource) ListTools(context.Context) ([]extmcp.Tool, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return append([]extmcp.Tool(nil), m.tools...), nil
}

func (m *mockSource) CallTool(_ context.Context, name string, _ map[string]any) (*extmcp.ToolResult, error) {
	if m.callErr != nil {
		return nil, m.callErr
	}
	result, ok := m.toolResults[name]
	if !ok {
		return nil, errors.New("tool not found")
	}
	return result, nil
}

func (m *mockSource) ListResources(context.Context) ([]extmcp.Resource, error) {
	if m.resourceErr != nil {
		return nil, m.resourceErr
	}
	return append([]extmcp.Resource(nil), m.resources...), nil
}

func (m *mockSource) ReadResource(_ context.Context, uri string) (*extmcp.ReadResourceResult, error) {
	if m.resourceErr != nil {
		return nil, m.resourceErr
	}
	result, ok := m.resourceResults[uri]
	if !ok {
		return nil, errors.New("resource not found")
	}
	return result, nil
}

func (m *mockSource) ListResourceTemplates(context.Context) ([]extmcp.ResourceTemplate, error) {
	if m.resourceErr != nil {
		return nil, m.resourceErr
	}
	return append([]extmcp.ResourceTemplate(nil), m.resourceTemplates...), nil
}

func TestServiceListsStatusReadsResourceAndCallsTool(t *testing.T) {
	ctx := context.Background()
	src := &mockSource{
		tools: []extmcp.Tool{{
			Name:        "echo",
			Description: "Echo text",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		toolResults: map[string]*extmcp.ToolResult{
			"echo": {
				Content: []extmcp.Content{{Type: "text", Text: "pong"}},
			},
		},
		resources: []extmcp.Resource{{
			URI:  "file:///workspace/README.md",
			Name: "README",
		}},
		resourceTemplates: []extmcp.ResourceTemplate{{
			URITemplate: "file:///workspace/{path}",
			Name:        "workspace_file",
		}},
		resourceResults: map[string]*extmcp.ReadResourceResult{
			"file:///workspace/README.md": {
				Contents: []extmcp.ResourceContents{{
					URI:  "file:///workspace/README.md",
					Text: "# Gollem\n",
				}},
			},
		},
	}

	svc := NewService()
	if err := svc.AddServer("repo", src); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	statuses := svc.ListStatuses(ctx, StatusListParams{})
	if len(statuses.Servers) != 1 {
		t.Fatalf("statuses = %#v", statuses)
	}
	status := statuses.Servers[0]
	if status.Name != "repo" || status.Status != "ready" || status.ToolCount != 1 || status.ResourceCount != 1 || !status.Capabilities.Resources {
		t.Fatalf("status = %#v", status)
	}

	resource, err := svc.ReadResource(ctx, ResourceReadParams{
		ServerName: "repo",
		URI:        "file:///workspace/README.md",
	})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if resource.Text != "# Gollem\n" || len(resource.Contents) != 1 {
		t.Fatalf("resource = %#v", resource)
	}

	tool, err := svc.CallTool(ctx, ToolCallParams{
		Name:      "repo__echo",
		Arguments: map[string]any{"text": "ping"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if tool.ServerName != "repo" || tool.ToolName != "echo" || tool.Text != "pong" {
		t.Fatalf("tool result = %#v", tool)
	}
}

func TestServiceValidationAndReload(t *testing.T) {
	ctx := context.Background()
	svc := NewService()

	if _, err := svc.ReadResource(ctx, ResourceReadParams{URI: "file:///missing"}); !errors.Is(err, ErrServerNameRequired) {
		t.Fatalf("ReadResource err = %v, want ErrServerNameRequired", err)
	}
	if _, err := svc.CallTool(ctx, ToolCallParams{ServerName: "repo"}); !errors.Is(err, ErrToolNameRequired) {
		t.Fatalf("CallTool err = %v, want ErrToolNameRequired", err)
	}

	reload := svc.Reload()
	if reload.Reloaded || reload.Status != "no-op" || reload.ServerCount != 0 {
		t.Fatalf("empty reload = %#v", reload)
	}
}

func TestServiceListToolsIsDeterministicAndKeepsHealthyServers(t *testing.T) {
	svc := NewService()
	if err := svc.AddServer("zeta", &mockSource{tools: []extmcp.Tool{{Name: "second", InputSchema: json.RawMessage(`{"type":"object"}`)}}}); err != nil {
		t.Fatalf("AddServer zeta: %v", err)
	}
	if err := svc.AddServer("alpha", &mockSource{tools: []extmcp.Tool{{Name: "first", Description: "First tool", InputSchema: json.RawMessage(`{"type":"object"}`)}}}); err != nil {
		t.Fatalf("AddServer alpha: %v", err)
	}
	if err := svc.AddServer("broken", &mockSource{listErr: errors.New("offline")}); err != nil {
		t.Fatalf("AddServer broken: %v", err)
	}

	listed, err := svc.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(listed.Tools) != 2 || listed.Tools[0].QualifiedName != "alpha__first" || listed.Tools[1].QualifiedName != "zeta__second" {
		t.Fatalf("tools = %#v", listed.Tools)
	}
	if len(listed.Errors) != 1 || listed.Errors[0].ServerID != "broken" || listed.Errors[0].Message != "offline" {
		t.Fatalf("errors = %#v", listed.Errors)
	}
}
