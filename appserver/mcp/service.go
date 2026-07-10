package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	extmcp "github.com/fugue-labs/gollem/ext/mcp"
)

var (
	ErrServerNameRequired  = errors.New("mcp server name is required")
	ErrServerAlreadyExists = errors.New("mcp server already exists")
	ErrServerNotFound      = errors.New("mcp server not found")
	ErrToolNameRequired    = errors.New("mcp tool name is required")
	ErrResourceURIRequired = errors.New("mcp resource uri is required")
)

type Option func(*Service)

// Source is an MCP source that can list and call tools. Sources that also
// implement ext/mcp.ResourceSource expose resource read support.
type Source interface {
	extmcp.ToolSource
}

type Service struct {
	mu          sync.RWMutex
	manager     *extmcp.Manager
	servers     map[string]registeredServer
	reloadCount int
	reloadedAt  time.Time
}

type registeredServer struct {
	name      string
	source    Source
	createdAt time.Time
}

type StatusListParams struct {
	ServerID   string   `json:"serverId,omitempty"`
	ServerName string   `json:"serverName,omitempty"`
	Name       string   `json:"name,omitempty"`
	Servers    []string `json:"servers,omitempty"`
}

type StatusListResponse struct {
	Servers []ServerStatus `json:"servers"`
	Data    []ServerStatus `json:"data"`
}

type ServerStatus struct {
	ID                    string             `json:"id"`
	Name                  string             `json:"name"`
	Status                string             `json:"status"`
	Connected             bool               `json:"connected"`
	Enabled               bool               `json:"enabled"`
	Capabilities          ServerCapabilities `json:"capabilities"`
	ToolCount             int                `json:"toolCount,omitempty"`
	ResourceCount         int                `json:"resourceCount,omitempty"`
	ResourceTemplateCount int                `json:"resourceTemplateCount,omitempty"`
	LastError             string             `json:"lastError,omitempty"`
	LastReloadedAt        *time.Time         `json:"lastReloadedAt,omitempty"`
	RegisteredAt          time.Time          `json:"registeredAt"`
}

type ServerCapabilities struct {
	Tools             bool `json:"tools"`
	Resources         bool `json:"resources"`
	ResourceTemplates bool `json:"resourceTemplates"`
	Prompts           bool `json:"prompts"`
	Notifications     bool `json:"notifications"`
}

type ResourceReadParams struct {
	ServerID     string `json:"serverId,omitempty"`
	ServerName   string `json:"serverName,omitempty"`
	Name         string `json:"name,omitempty"`
	MCPServerID  string `json:"mcpServerId,omitempty"`
	MCPServer    string `json:"mcpServer,omitempty"`
	URI          string `json:"uri,omitempty"`
	ResourceURI  string `json:"resourceUri,omitempty"`
	ResourceURI2 string `json:"resourceURI,omitempty"`
}

type ResourceReadResponse struct {
	ServerID   string                     `json:"serverId"`
	ServerName string                     `json:"serverName"`
	URI        string                     `json:"uri"`
	Result     *extmcp.ReadResourceResult `json:"result"`
	Contents   []extmcp.ResourceContents  `json:"contents"`
	Text       string                     `json:"text,omitempty"`
}

type ToolCallParams struct {
	ServerID    string         `json:"serverId,omitempty"`
	ServerName  string         `json:"serverName,omitempty"`
	MCPServerID string         `json:"mcpServerId,omitempty"`
	MCPServer   string         `json:"mcpServer,omitempty"`
	Name        string         `json:"name,omitempty"`
	ToolName    string         `json:"toolName,omitempty"`
	Tool        string         `json:"tool,omitempty"`
	Arguments   map[string]any `json:"arguments,omitempty"`
	Args        map[string]any `json:"args,omitempty"`
	Input       map[string]any `json:"input,omitempty"`
}

type ToolCallResponse struct {
	ServerID          string             `json:"serverId"`
	ServerName        string             `json:"serverName"`
	ToolName          string             `json:"toolName"`
	Result            *extmcp.ToolResult `json:"result"`
	Content           []extmcp.Content   `json:"content"`
	StructuredContent any                `json:"structuredContent,omitempty"`
	IsError           bool               `json:"isError,omitempty"`
	Text              string             `json:"text,omitempty"`
}

type ToolCallTarget struct {
	ServerName string         `json:"serverName"`
	ToolName   string         `json:"toolName"`
	Arguments  map[string]any `json:"arguments"`
}

type ToolListResponse struct {
	Tools  []ToolDescriptor `json:"tools"`
	Errors []ToolListError  `json:"errors,omitempty"`
}

type ToolDescriptor struct {
	ServerID      string          `json:"serverId"`
	ServerName    string          `json:"serverName"`
	Name          string          `json:"name"`
	QualifiedName string          `json:"qualifiedName"`
	Description   string          `json:"description,omitempty"`
	InputSchema   json.RawMessage `json:"inputSchema,omitempty"`
}

type ToolListError struct {
	ServerID string `json:"serverId"`
	Message  string `json:"message"`
}

type ReloadResponse struct {
	Reloaded    bool       `json:"reloaded"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason,omitempty"`
	Count       int        `json:"count"`
	ServerCount int        `json:"serverCount"`
	ReloadedAt  *time.Time `json:"reloadedAt,omitempty"`
}

func NewService(opts ...Option) *Service {
	s := &Service{
		manager: extmcp.NewManager(),
		servers: make(map[string]registeredServer),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.manager == nil {
		s.manager = extmcp.NewManager()
	}
	if s.servers == nil {
		s.servers = make(map[string]registeredServer)
	}
	return s
}

func (s *Service) AddServer(name string, source Source) error {
	s = ensureService(s)
	name = normalizeName(name)
	if name == "" {
		return ErrServerNameRequired
	}
	if source == nil {
		return errors.New("mcp source is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[name]; ok {
		return fmt.Errorf("%w: %s", ErrServerAlreadyExists, name)
	}
	if err := s.manager.AddServer(name, source); err != nil {
		return err
	}
	s.servers[name] = registeredServer{
		name:      name,
		source:    source,
		createdAt: time.Now().UTC(),
	}
	return nil
}

func (s *Service) RemoveServer(name string) error {
	s = ensureService(s)
	name = normalizeName(name)
	if name == "" {
		return ErrServerNameRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[name]; !ok {
		return fmt.Errorf("%w: %s", ErrServerNotFound, name)
	}
	if err := s.manager.RemoveServer(name); err != nil {
		return err
	}
	delete(s.servers, name)
	return nil
}

func (s *Service) ListStatuses(ctx context.Context, params StatusListParams) StatusListResponse {
	s = ensureService(s)
	servers := s.snapshotServers(params)
	statuses := make([]ServerStatus, 0, len(servers))
	for _, server := range servers {
		statuses = append(statuses, s.statusForServer(ctx, server))
	}
	return StatusListResponse{
		Servers: statuses,
		Data:    cloneStatuses(statuses),
	}
}

// ListTools returns deterministic server-qualified MCP tool descriptors. A
// broken server is reported alongside tools from healthy servers so one source
// cannot hide the rest of the registry.
func (s *Service) ListTools(ctx context.Context) (ToolListResponse, error) {
	s = ensureService(s)
	if ctx == nil {
		ctx = context.Background()
	}
	servers := s.snapshotServers(StatusListParams{})
	response := ToolListResponse{Tools: make([]ToolDescriptor, 0)}
	for _, server := range servers {
		tools, err := server.source.ListTools(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ToolListResponse{}, ctx.Err()
			}
			response.Errors = append(response.Errors, ToolListError{ServerID: server.name, Message: err.Error()})
			continue
		}
		for _, tool := range tools {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			response.Tools = append(response.Tools, ToolDescriptor{
				ServerID:      server.name,
				ServerName:    server.name,
				Name:          name,
				QualifiedName: server.name + "__" + name,
				Description:   tool.Description,
				InputSchema:   append(json.RawMessage(nil), tool.InputSchema...),
			})
		}
	}
	slices.SortFunc(response.Tools, func(a, b ToolDescriptor) int {
		if byServer := strings.Compare(a.ServerName, b.ServerName); byServer != 0 {
			return byServer
		}
		return strings.Compare(a.Name, b.Name)
	})
	return response, nil
}

func (s *Service) ReadResource(ctx context.Context, params ResourceReadParams) (ResourceReadResponse, error) {
	s = ensureService(s)
	serverName := normalizeName(firstNonEmpty(params.ServerID, params.ServerName, params.MCPServerID, params.MCPServer, params.Name))
	if serverName == "" {
		return ResourceReadResponse{}, ErrServerNameRequired
	}
	uri := strings.TrimSpace(firstNonEmpty(params.URI, params.ResourceURI, params.ResourceURI2))
	if uri == "" {
		return ResourceReadResponse{}, ErrResourceURIRequired
	}
	if _, err := s.sourceForServer(serverName); err != nil {
		return ResourceReadResponse{}, err
	}
	result, err := s.manager.ReadResource(ctx, serverName, uri)
	if err != nil {
		return ResourceReadResponse{}, err
	}
	return ResourceReadResponse{
		ServerID:   serverName,
		ServerName: serverName,
		URI:        uri,
		Result:     result,
		Contents:   append([]extmcp.ResourceContents(nil), result.Contents...),
		Text:       result.TextContent(),
	}, nil
}

func (s *Service) CallTool(ctx context.Context, params ToolCallParams) (ToolCallResponse, error) {
	s = ensureService(s)
	target, err := s.ResolveToolCall(params)
	if err != nil {
		return ToolCallResponse{}, err
	}
	return s.CallResolvedTool(ctx, target)
}

func (s *Service) ResolveToolCall(params ToolCallParams) (ToolCallTarget, error) {
	s = ensureService(s)
	serverName := normalizeName(firstNonEmpty(params.ServerID, params.ServerName, params.MCPServerID, params.MCPServer))
	toolName := strings.TrimSpace(firstNonEmpty(params.ToolName, params.Tool, params.Name))
	if serverName == "" && toolName != "" {
		if parsedServer, parsedTool, ok := s.manager.ParseToolName(toolName); ok {
			serverName = normalizeName(parsedServer)
			toolName = parsedTool
		}
	}
	if serverName == "" {
		return ToolCallTarget{}, ErrServerNameRequired
	}
	if toolName == "" {
		return ToolCallTarget{}, ErrToolNameRequired
	}
	args := params.Arguments
	if args == nil {
		args = params.Args
	}
	if args == nil {
		args = params.Input
	}
	if args == nil {
		args = map[string]any{}
	}
	if _, err := s.sourceForServer(serverName); err != nil {
		return ToolCallTarget{}, err
	}
	return ToolCallTarget{
		ServerName: serverName,
		ToolName:   toolName,
		Arguments:  args,
	}, nil
}

func (s *Service) CallResolvedTool(ctx context.Context, target ToolCallTarget) (ToolCallResponse, error) {
	s = ensureService(s)
	serverName := normalizeName(target.ServerName)
	toolName := strings.TrimSpace(target.ToolName)
	if serverName == "" {
		return ToolCallResponse{}, ErrServerNameRequired
	}
	if toolName == "" {
		return ToolCallResponse{}, ErrToolNameRequired
	}
	source, err := s.sourceForServer(serverName)
	if err != nil {
		return ToolCallResponse{}, err
	}
	args := target.Arguments
	if args == nil {
		args = map[string]any{}
	}
	result, err := source.CallTool(ctx, toolName, args)
	if err != nil {
		return ToolCallResponse{}, err
	}
	if result == nil {
		return ToolCallResponse{}, fmt.Errorf("mcp tool %q on server %q returned no result", toolName, serverName)
	}
	return ToolCallResponse{
		ServerID:          serverName,
		ServerName:        serverName,
		ToolName:          toolName,
		Result:            result,
		Content:           append([]extmcp.Content(nil), result.Content...),
		StructuredContent: result.StructuredContent,
		IsError:           result.IsError,
		Text:              result.TextContent(),
	}, nil
}

func (s *Service) Reload() ReloadResponse {
	s = ensureService(s)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadCount++
	serverCount := len(s.servers)
	if serverCount == 0 {
		return ReloadResponse{
			Reloaded:    false,
			Status:      "no-op",
			Reason:      "no MCP servers are registered in this Gollem app-server instance",
			Count:       s.reloadCount,
			ServerCount: serverCount,
		}
	}
	now := time.Now().UTC()
	s.reloadedAt = now
	return ReloadResponse{
		Reloaded:    true,
		Status:      "reloaded",
		Count:       s.reloadCount,
		ServerCount: serverCount,
		ReloadedAt:  &now,
	}
}

func (s *Service) snapshotServers(params StatusListParams) []registeredServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	filters := statusFilters(params)
	servers := make([]registeredServer, 0, len(s.servers))
	for name, server := range s.servers {
		if len(filters) > 0 {
			if _, ok := filters[name]; !ok {
				continue
			}
		}
		servers = append(servers, server)
	}
	slices.SortFunc(servers, func(a, b registeredServer) int {
		return strings.Compare(a.name, b.name)
	})
	return servers
}

func (s *Service) statusForServer(ctx context.Context, server registeredServer) ServerStatus {
	status := ServerStatus{
		ID:           server.name,
		Name:         server.name,
		Status:       "ready",
		Connected:    true,
		Enabled:      true,
		Capabilities: capabilitiesForSource(server.source),
		RegisteredAt: server.createdAt,
	}
	s.mu.RLock()
	if !s.reloadedAt.IsZero() {
		reloadedAt := s.reloadedAt
		status.LastReloadedAt = &reloadedAt
	}
	s.mu.RUnlock()

	tools, err := server.source.ListTools(ctx)
	if err != nil {
		status.Status = "error"
		status.Connected = false
		status.LastError = err.Error()
		return status
	}
	status.ToolCount = len(tools)

	if resourceSource, ok := server.source.(extmcp.ResourceSource); ok {
		resources, err := resourceSource.ListResources(ctx)
		if err != nil {
			status.Status = "error"
			status.Connected = false
			status.LastError = err.Error()
			return status
		}
		status.ResourceCount = len(resources)
		templates, err := resourceSource.ListResourceTemplates(ctx)
		if err != nil {
			status.Status = "error"
			status.Connected = false
			status.LastError = err.Error()
			return status
		}
		status.ResourceTemplateCount = len(templates)
	}
	return status
}

func (s *Service) sourceForServer(name string) (Source, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	server, ok := s.servers[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServerNotFound, name)
	}
	return server.source, nil
}

func capabilitiesForSource(source Source) ServerCapabilities {
	caps := ServerCapabilities{Tools: true}
	if _, ok := source.(extmcp.ResourceSource); ok {
		caps.Resources = true
		caps.ResourceTemplates = true
	}
	if _, ok := source.(extmcp.PromptSource); ok {
		caps.Prompts = true
	}
	if _, ok := source.(extmcp.NotificationSource); ok {
		caps.Notifications = true
	}
	return caps
}

func ensureService(s *Service) *Service {
	if s != nil {
		return s
	}
	return NewService()
}

func statusFilters(params StatusListParams) map[string]struct{} {
	values := append([]string(nil), params.Servers...)
	values = append(values, params.ServerID, params.ServerName, params.Name)
	filters := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeName(value)
		if value != "" {
			filters[value] = struct{}{}
		}
	}
	return filters
}

func normalizeName(name string) string {
	return strings.TrimSpace(name)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cloneStatuses(statuses []ServerStatus) []ServerStatus {
	out := make([]ServerStatus, len(statuses))
	copy(out, statuses)
	return out
}
