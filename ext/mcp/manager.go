package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/fugue-labs/gollem/core"
)

// ToolSource is the interface for any MCP client that can provide tools.
// Both Client (stdio) and SSEClient implement this interface.
type ToolSource interface {
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error)
}

// Ensure both client types implement ToolSource.
var (
	_ ToolSource = (*Client)(nil)
	_ ToolSource = (*SSEClient)(nil)
)

// ServerConfig configures an MCP server within the manager.
type ServerConfig struct {
	// Name is the unique name for this server (used as tool prefix).
	Name string

	// Source is the MCP client connected to the server.
	Source ToolSource
}

// Manager aggregates tools from multiple MCP servers, applying prefix-based
// namespacing to avoid tool name collisions. Tool names from server "foo"
// become "foo__tool_name" when exposed to the agent.
type Manager struct {
	mu      sync.Mutex
	servers map[string]ToolSource
	sep     string
}

// ManagerOption configures the manager.
type ManagerOption func(*managerConfig)

type managerConfig struct {
	sep string
}

// WithSeparator sets the separator used between server name and tool name.
// Default is "__" (double underscore).
func WithSeparator(sep string) ManagerOption {
	return func(c *managerConfig) {
		c.sep = sep
	}
}

// NewManager creates a new multi-server MCP manager.
func NewManager(opts ...ManagerOption) *Manager {
	cfg := &managerConfig{sep: "__"}
	for _, opt := range opts {
		opt(cfg)
	}

	return &Manager{
		servers: make(map[string]ToolSource),
		sep:     cfg.sep,
	}
}

// AddServer registers an MCP server with the manager under the given name.
// The name is used as a prefix for tool names to avoid collisions.
func (m *Manager) AddServer(name string, source ToolSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.servers[name]; exists {
		return fmt.Errorf("mcp: server %q already registered", name)
	}

	m.servers[name] = source
	return nil
}

// RemoveServer unregisters an MCP server. If the source implements io.Closer,
// it is also closed.
func (m *Manager) RemoveServer(name string) error {
	m.mu.Lock()
	source, exists := m.servers[name]
	if exists {
		delete(m.servers, name)
	}
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("mcp: server %q not found", name)
	}

	if closer, ok := source.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Tools discovers tools from all registered servers and returns them as
// core.Tool instances with namespaced names (e.g., "server__tool_name").
// Tools are discovered concurrently for efficiency.
func (m *Manager) Tools(ctx context.Context) ([]core.Tool, error) {
	m.mu.Lock()
	servers := make(map[string]ToolSource, len(m.servers))
	for name, source := range m.servers {
		servers[name] = source
	}
	m.mu.Unlock()

	type result struct {
		name  string
		tools []Tool
		err   error
	}

	ch := make(chan result, len(servers))
	var wg sync.WaitGroup

	for name, source := range servers {
		wg.Add(1)
		go func(name string, source ToolSource) {
			defer wg.Done()
			tools, err := source.ListTools(ctx)
			ch <- result{name: name, tools: tools, err: err}
		}(name, source)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var allTools []core.Tool
	var errs []error

	for r := range ch {
		if r.err != nil {
			errs = append(errs, fmt.Errorf("server %q: %w", r.name, r.err))
			continue
		}

		for _, mt := range r.tools {
			tool := m.convertManagedTool(r.name, servers[r.name], mt)
			allTools = append(allTools, tool)
		}
	}

	if len(errs) > 0 {
		// If all servers failed, return an error.
		if len(allTools) == 0 {
			return nil, fmt.Errorf("mcp: all servers failed: %v", errs)
		}
		// Partial failure: return tools we got with no error.
		// Callers can log warnings themselves if needed.
	}

	return allTools, nil
}

// convertManagedTool creates a core.Tool from an MCP tool definition with
// a namespaced name.
func (m *Manager) convertManagedTool(serverName string, source ToolSource, mt Tool) core.Tool {
	var schema core.Schema
	if mt.InputSchema != nil {
		if err := json.Unmarshal(mt.InputSchema, &schema); err != nil {
			schema = nil
		}
	}
	if schema == nil {
		schema = core.Schema{"type": "object"}
	}

	prefixedName := serverName + m.sep + mt.Name

	def := core.ToolDefinition{
		Name:             prefixedName,
		Description:      fmt.Sprintf("[%s] %s", serverName, mt.Description),
		ParametersSchema: schema,
		Kind:             core.ToolKindFunction,
	}

	originalName := mt.Name

	handler := func(ctx context.Context, _ *core.RunContext, argsJSON string) (any, error) {
		var args map[string]any
		if argsJSON != "" && argsJSON != "{}" {
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return nil, err
			}
		}
		if args == nil {
			args = make(map[string]any)
		}

		result, err := source.CallTool(ctx, originalName, args)
		if err != nil {
			return nil, err
		}

		if result.IsError {
			return nil, &core.ModelRetryError{Message: result.TextContent()}
		}

		return result.TextContent(), nil
	}

	return core.Tool{
		Definition: def,
		Handler:    handler,
	}
}

// ServerNames returns the names of all registered servers.
func (m *Manager) ServerNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	return names
}

// ParseToolName splits a namespaced tool name into server name and tool name.
// Returns the server name, tool name, and true if the name was successfully parsed.
func (m *Manager) ParseToolName(namespacedName string) (serverName, toolName string, ok bool) {
	idx := strings.Index(namespacedName, m.sep)
	if idx < 0 {
		return "", namespacedName, false
	}
	return namespacedName[:idx], namespacedName[idx+len(m.sep):], true
}

// Close closes all registered servers that implement io.Closer.
func (m *Manager) Close() error {
	m.mu.Lock()
	servers := make(map[string]ToolSource, len(m.servers))
	for name, source := range m.servers {
		servers[name] = source
	}
	m.servers = make(map[string]ToolSource)
	m.mu.Unlock()

	var errs []error
	for name, source := range servers {
		if closer, ok := source.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, fmt.Errorf("server %q: %w", name, err))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("mcp: errors closing servers: %v", errs)
	}
	return nil
}
