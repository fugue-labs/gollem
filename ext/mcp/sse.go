package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fugue-labs/gollem/core"
)

// SSEClient communicates with an MCP server over HTTP Server-Sent Events
// (SSE) using JSON-RPC 2.0. The client connects to the SSE endpoint to
// receive responses, and posts JSON-RPC requests to the messages endpoint
// provided by the server.
type SSEClient struct {
	baseURL    string
	httpClient *http.Client

	mu          sync.Mutex
	nextID      atomic.Int64
	pending     map[int64]chan *jsonRPCResponse
	closed      bool
	closeOnce   sync.Once
	cancelSSE   context.CancelFunc
	messagesURL string // set after receiving the "endpoint" event
	ready       chan struct{}
}

// SSEOption configures the SSE client.
type SSEOption func(*sseConfig)

type sseConfig struct {
	httpClient *http.Client
}

// WithHTTPClient sets a custom HTTP client for the SSE transport.
func WithHTTPClient(client *http.Client) SSEOption {
	return func(c *sseConfig) {
		c.httpClient = client
	}
}

// NewSSEClient connects to an MCP server over SSE at the given URL.
// The URL should point to the SSE endpoint (e.g., "http://localhost:8080/sse").
// The server sends an "endpoint" event with the messages URL, then the client
// performs the MCP initialization handshake.
func NewSSEClient(ctx context.Context, url string, opts ...SSEOption) (*SSEClient, error) {
	cfg := &sseConfig{
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	sseCtx, cancel := context.WithCancel(context.Background())

	c := &SSEClient{
		baseURL:    url,
		httpClient: cfg.httpClient,
		pending:    make(map[int64]chan *jsonRPCResponse),
		cancelSSE:  cancel,
		ready:      make(chan struct{}),
	}

	// Connect to SSE stream.
	if err := c.connectSSE(sseCtx); err != nil {
		cancel()
		return nil, fmt.Errorf("mcp: failed to connect SSE: %w", err)
	}

	// Wait for the endpoint event.
	select {
	case <-c.ready:
	case <-ctx.Done():
		cancel()
		return nil, fmt.Errorf("mcp: timed out waiting for endpoint event: %w", ctx.Err())
	}

	// Perform initialization handshake.
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp: initialization failed: %w", err)
	}

	return c, nil
}

// connectSSE starts the SSE connection in the background.
func (c *SSEClient) connectSSE(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("mcp: SSE connection returned status %d", resp.StatusCode)
	}

	go c.readSSE(resp.Body)
	return nil
}

// readSSE reads SSE events from the response body and dispatches them.
func (c *SSEClient) readSSE(body io.ReadCloser) {
	defer body.Close()

	scanner := bufio.NewScanner(body)
	var eventType string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = end of event.
			if eventType != "" && len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				c.handleSSEEvent(eventType, data)
			}
			eventType = ""
			dataLines = nil
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}

	// Connection closed or error.
	c.mu.Lock()
	c.closed = true
	for _, ch := range c.pending {
		close(ch)
	}
	c.pending = make(map[int64]chan *jsonRPCResponse)
	c.mu.Unlock()
}

// handleSSEEvent processes a single SSE event.
func (c *SSEClient) handleSSEEvent(eventType, data string) {
	switch eventType {
	case "endpoint":
		// The server tells us where to POST messages.
		c.mu.Lock()
		c.messagesURL = c.resolveURL(strings.TrimSpace(data))
		c.mu.Unlock()
		select {
		case <-c.ready:
		default:
			close(c.ready)
		}

	case "message":
		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			return
		}

		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()

		if ok {
			ch <- &resp
		}
	}
}

// resolveURL resolves a potentially relative URL against the base URL.
func (c *SSEClient) resolveURL(endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}

	// Relative path: combine with base URL's scheme+host.
	base := c.baseURL
	// Find the scheme+host portion.
	idx := strings.Index(base, "://")
	if idx < 0 {
		return endpoint
	}
	// Find the end of the host portion.
	hostEnd := strings.Index(base[idx+3:], "/")
	if hostEnd < 0 {
		return base + endpoint
	}
	return base[:idx+3+hostEnd] + endpoint
}

// initialize performs the MCP initialization handshake.
func (c *SSEClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "gollem",
			"version": "1.0.0",
		},
	}

	_, err := c.call(ctx, "initialize", params)
	if err != nil {
		return err
	}

	// Send initialized notification.
	return c.notify(ctx, "notifications/initialized", nil)
}

// call sends a JSON-RPC request and waits for a response.
func (c *SSEClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	ch := make(chan *jsonRPCResponse, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("mcp: client is closed")
	}
	c.pending[id] = ch
	messagesURL := c.messagesURL
	c.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, messagesURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mcp: failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: failed to send request: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: server returned status %d", resp.StatusCode)
	}

	select {
	case rpcResp, ok := <-ch:
		if !ok {
			return nil, errors.New("mcp: connection closed")
		}
		if rpcResp.Error != nil {
			return nil, rpcResp.Error
		}
		return rpcResp.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// notify sends a JSON-RPC notification over HTTP (no response expected).
func (c *SSEClient) notify(ctx context.Context, method string, params any) error {
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	c.mu.Lock()
	messagesURL := c.messagesURL
	c.mu.Unlock()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, messagesURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("mcp: failed to send notification: %w", err)
	}
	resp.Body.Close()
	return nil
}

// ListTools discovers available tools from the MCP server.
func (c *SSEClient) ListTools(ctx context.Context) ([]Tool, error) {
	result, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/list failed: %w", err)
	}

	var listResult struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(result, &listResult); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse tools list: %w", err)
	}

	return listResult.Tools, nil
}

// CallTool invokes a tool on the MCP server.
func (c *SSEClient) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}

	result, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/call failed: %w", err)
	}

	var toolResult ToolResult
	if err := json.Unmarshal(result, &toolResult); err != nil {
		return nil, fmt.Errorf("mcp: failed to parse tool result: %w", err)
	}

	return &toolResult, nil
}

// Tools converts MCP tools into core.Tool instances that call back to the
// MCP server. This method has the same signature as Client.Tools for
// interchangeable usage.
func (c *SSEClient) Tools(ctx context.Context) ([]core.Tool, error) {
	tools, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}

	var result []core.Tool
	for _, mt := range tools {
		tool := convertSSETool(c, mt)
		result = append(result, tool)
	}
	return result, nil
}

// convertSSETool converts a single MCP tool definition to a core.Tool
// backed by the SSE client.
func convertSSETool(client *SSEClient, mt Tool) core.Tool {
	var schema core.Schema
	if mt.InputSchema != nil {
		if err := json.Unmarshal(mt.InputSchema, &schema); err != nil {
			schema = nil
		}
	}
	if schema == nil {
		schema = core.Schema{"type": "object"}
	}

	def := core.ToolDefinition{
		Name:             mt.Name,
		Description:      mt.Description,
		ParametersSchema: schema,
		Kind:             core.ToolKindFunction,
	}

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

		result, err := client.CallTool(ctx, mt.Name, args)
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

// Close shuts down the SSE connection and releases resources.
func (c *SSEClient) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()

		if c.cancelSSE != nil {
			c.cancelSSE()
		}
	})
	return nil
}
