package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	appmcp "github.com/fugue-labs/gollem/appserver/mcp"
	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/core"
)

const (
	runtimeMCPToolNamespace        = "mcp"
	runtimeMCPResultMaxBytes       = 64 * 1024
	runtimeMCPCallTextMaxBytes     = 32 * 1024
	runtimeMCPStructuredMaxBytes   = 16 * 1024
	runtimeMCPMetadataMaxBytes     = 2048
	runtimeMCPIdentifierMaxBytes   = 2048
	runtimeMCPResourceURIMaxBytes  = 16 * 1024
	runtimeMCPMaxTools             = 128
	runtimeMCPMaxServers           = 64
	runtimeMCPReadTimeout          = 30 * time.Second
	runtimeMCPCallTimeout          = 5 * time.Minute
	runtimeMCPTruncationMarker     = "\n[MCP output truncated]\n"
	runtimeMCPNonTextOmittedMarker = "[MCP returned non-text content; artifact handles are not implemented]"
)

type runtimeMCPServerListParams struct {
	Servers []string `json:"servers,omitempty"`
}

type runtimeMCPResourceParams struct {
	Server string `json:"server"`
	URI    string `json:"uri"`
}

type runtimeMCPCallParams struct {
	Server    string         `json:"server"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type runtimeMCPServerSummary struct {
	ID                    string                    `json:"id"`
	Status                string                    `json:"status"`
	Connected             bool                      `json:"connected"`
	Enabled               bool                      `json:"enabled"`
	Capabilities          appmcp.ServerCapabilities `json:"capabilities"`
	ToolCount             int                       `json:"toolCount,omitempty"`
	ResourceCount         int                       `json:"resourceCount,omitempty"`
	ResourceTemplateCount int                       `json:"resourceTemplateCount,omitempty"`
	LastError             string                    `json:"lastError,omitempty"`
	LastErrorTruncated    bool                      `json:"lastErrorTruncated,omitempty"`
}

type runtimeMCPServerListResult struct {
	Servers   []runtimeMCPServerSummary `json:"servers"`
	Total     int                       `json:"total"`
	Truncated bool                      `json:"truncated,omitempty"`
}

type runtimeMCPToolSummary struct {
	Server               string `json:"server"`
	Name                 string `json:"name"`
	QualifiedName        string `json:"qualifiedName"`
	Description          string `json:"description,omitempty"`
	DescriptionTruncated bool   `json:"descriptionTruncated,omitempty"`
	InputSchema          any    `json:"inputSchema"`
}

type runtimeMCPToolListResult struct {
	Tools     []runtimeMCPToolSummary `json:"tools"`
	Errors    []appmcp.ToolListError  `json:"errors,omitempty"`
	Total     int                     `json:"total"`
	Truncated bool                    `json:"truncated,omitempty"`
}

type runtimeMCPResourceResult struct {
	Server                string `json:"server"`
	URI                   string `json:"uri"`
	Text                  string `json:"text,omitempty"`
	Bytes                 int    `json:"bytes"`
	SHA256                string `json:"sha256,omitempty"`
	Truncated             bool   `json:"truncated,omitempty"`
	NonTextContentOmitted bool   `json:"nonTextContentOmitted,omitempty"`
}

type runtimeMCPContentPayload = protocol.MCPContent
type runtimeMCPToolCallResultPayload = protocol.MCPToolCallResult

type runtimeMCPCallResult struct {
	Server            string                           `json:"server"`
	Tool              string                           `json:"tool"`
	Text              string                           `json:"text,omitempty"`
	StructuredContent any                              `json:"structuredContent,omitempty"`
	IsError           bool                             `json:"isError,omitempty"`
	Bytes             int                              `json:"bytes"`
	SHA256            string                           `json:"sha256,omitempty"`
	Truncated         bool                             `json:"truncated,omitempty"`
	NonTextOmitted    bool                             `json:"nonTextContentOmitted,omitempty"`
	ItemResult        *runtimeMCPToolCallResultPayload `json:"-"`
}

type runtimeMCPToolStartedEvent struct {
	RunID      string
	ToolCallID string
	ToolName   string
	Server     string
	MCPTool    string
	Arguments  map[string]any
	StartedAt  time.Time
	ItemID     *string
}

type runtimeMCPToolProgressEvent struct {
	RunID      string
	ToolCallID string
	ToolName   string
	Message    string
}

type runtimeMCPToolCompletedEvent struct {
	RunID       string
	ToolCallID  string
	ToolName    string
	Result      *runtimeMCPCallResult
	Error       string
	CompletedAt time.Time
}

// MCPRuntimeTools adapts the app-server MCP registry into bounded,
// provider-neutral model tools. Tool calls retain app-server approval and emit
// dedicated MCP item lifecycle events.
func MCPRuntimeTools(service *appmcp.Service, approvals *ApprovalService) []core.Tool {
	if service == nil {
		return nil
	}
	tools := []core.Tool{
		core.FuncTool[runtimeMCPServerListParams](
			"mcp_list_servers",
			"List bounded status and capability metadata for registered MCP servers.",
			func(ctx context.Context, params runtimeMCPServerListParams) (runtimeMCPServerListResult, error) {
				if len(params.Servers) > runtimeMCPMaxServers {
					return runtimeMCPServerListResult{}, fmt.Errorf("servers exceeds %d entries", runtimeMCPMaxServers)
				}
				for _, name := range params.Servers {
					if len(name) > runtimeMCPIdentifierMaxBytes {
						return runtimeMCPServerListResult{}, fmt.Errorf("server name exceeds %d bytes", runtimeMCPIdentifierMaxBytes)
					}
				}
				statuses := service.ListStatuses(ctx, appmcp.StatusListParams{Servers: append([]string(nil), params.Servers...)})
				total := len(statuses.Servers)
				if len(statuses.Servers) > runtimeMCPMaxServers {
					statuses.Servers = statuses.Servers[:runtimeMCPMaxServers]
				}
				result := runtimeMCPServerListResult{Total: total, Truncated: total > len(statuses.Servers)}
				for _, status := range statuses.Servers {
					lastError, truncated := boundedRuntimeMCPText(status.LastError, runtimeMCPMetadataMaxBytes)
					serverID, _ := boundedRuntimeMCPText(status.ID, runtimeMCPIdentifierMaxBytes)
					result.Servers = append(result.Servers, runtimeMCPServerSummary{
						ID:                    serverID,
						Status:                status.Status,
						Connected:             status.Connected,
						Enabled:               status.Enabled,
						Capabilities:          status.Capabilities,
						ToolCount:             status.ToolCount,
						ResourceCount:         status.ResourceCount,
						ResourceTemplateCount: status.ResourceTemplateCount,
						LastError:             lastError,
						LastErrorTruncated:    truncated,
					})
				}
				return result, nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(runtimeMCPReadTimeout),
		),
		core.FuncTool[struct{}](
			"mcp_list_tools",
			"List bounded tool names, descriptions, and input schemas from registered MCP servers.",
			func(ctx context.Context, _ struct{}) (runtimeMCPToolListResult, error) {
				listed, err := service.ListTools(ctx)
				if err != nil {
					return runtimeMCPToolListResult{}, err
				}
				total := len(listed.Tools)
				if len(listed.Tools) > runtimeMCPMaxTools {
					listed.Tools = listed.Tools[:runtimeMCPMaxTools]
				}
				result := runtimeMCPToolListResult{
					Errors:    boundedRuntimeMCPListErrors(listed.Errors),
					Total:     total,
					Truncated: total > len(listed.Tools),
				}
				for _, tool := range listed.Tools {
					description, truncated := boundedRuntimeMCPText(tool.Description, runtimeMCPMetadataMaxBytes)
					serverName, _ := boundedRuntimeMCPText(tool.ServerName, runtimeMCPIdentifierMaxBytes)
					name, _ := boundedRuntimeMCPText(tool.Name, runtimeMCPIdentifierMaxBytes)
					qualifiedName, _ := boundedRuntimeMCPText(tool.QualifiedName, runtimeMCPIdentifierMaxBytes*2)
					result.Tools = append(result.Tools, runtimeMCPToolSummary{
						Server:               serverName,
						Name:                 name,
						QualifiedName:        qualifiedName,
						Description:          description,
						DescriptionTruncated: truncated,
						InputSchema:          boundedRuntimeMCPRawJSON(tool.InputSchema, runtimeMCPStructuredMaxBytes, "MCP input schema exceeds model payload limit"),
					})
				}
				return result, nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(runtimeMCPReadTimeout),
		),
		core.FuncTool[runtimeMCPResourceParams](
			"mcp_read_resource",
			"Read bounded textual content from one resource on a registered MCP server.",
			func(ctx context.Context, params runtimeMCPResourceParams) (runtimeMCPResourceResult, error) {
				serverName := strings.TrimSpace(params.Server)
				uri := strings.TrimSpace(params.URI)
				if serverName == "" {
					return runtimeMCPResourceResult{}, errors.New("server is required")
				}
				if uri == "" {
					return runtimeMCPResourceResult{}, errors.New("uri is required")
				}
				if len(serverName) > runtimeMCPIdentifierMaxBytes || len(uri) > runtimeMCPResourceURIMaxBytes {
					return runtimeMCPResourceResult{}, fmt.Errorf("server must be at most %d bytes and uri at most %d bytes", runtimeMCPIdentifierMaxBytes, runtimeMCPResourceURIMaxBytes)
				}
				response, err := service.ReadResource(ctx, appmcp.ResourceReadParams{ServerName: serverName, URI: uri})
				if err != nil {
					return runtimeMCPResourceResult{}, err
				}
				text, truncated := boundedRuntimeMCPText(response.Text, runtimeMCPResultMaxBytes)
				result := runtimeMCPResourceResult{
					Server:                response.ServerName,
					URI:                   response.URI,
					Text:                  text,
					Bytes:                 len(response.Text),
					Truncated:             truncated,
					NonTextContentOmitted: response.Text == "" && len(response.Contents) > 0,
				}
				if response.Text != "" {
					result.SHA256 = runtimeSHA256([]byte(response.Text))
				}
				return result, nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(runtimeMCPReadTimeout),
		),
		core.FuncTool[runtimeMCPCallParams](
			"mcp_call_tool",
			"Call one registered MCP tool after app-server approval. Arguments, results, and persisted lifecycle data are bounded.",
			func(ctx context.Context, rc *core.RunContext, params runtimeMCPCallParams) (runtimeMCPCallResult, error) {
				if len(params.Server) > runtimeMCPIdentifierMaxBytes || len(params.Tool) > runtimeMCPIdentifierMaxBytes {
					return runtimeMCPCallResult{}, fmt.Errorf("server and tool must each be at most %d bytes", runtimeMCPIdentifierMaxBytes)
				}
				target, err := service.ResolveToolCall(appmcp.ToolCallParams{
					ServerName: strings.TrimSpace(params.Server),
					ToolName:   strings.TrimSpace(params.Tool),
					Arguments:  cloneStringAnyMap(params.Arguments),
				})
				if err != nil {
					return runtimeMCPCallResult{}, err
				}
				if err := validateRuntimeMCPArguments(target.Arguments); err != nil {
					return runtimeMCPCallResult{}, err
				}
				startedAt := time.Now().UTC()
				itemID := ""
				publishRuntimeMCPToolStarted(rc, runtimeMCPToolStartedEvent{
					RunID:      runtimeRunID(rc),
					ToolCallID: runtimeToolCallID(ctx, rc),
					ToolName:   runtimeToolName(rc, "mcp_call_tool"),
					Server:     target.ServerName,
					MCPTool:    target.ToolName,
					Arguments:  cloneStringAnyMap(target.Arguments),
					StartedAt:  startedAt,
					ItemID:     &itemID,
				})
				approvalCtx := withRuntimeApprovalItemID(ctx, itemID)
				publishRuntimeMCPToolProgress(rc, "Waiting for MCP tool approval")
				if approvals == nil {
					err = errors.New("MCP approval service is not configured")
				} else {
					err = approvals.MCPToolApproval(approvalCtx, target.ServerName, target.ToolName, target.Arguments)
				}
				if err != nil {
					publishRuntimeMCPToolCompleted(rc, nil, err)
					return runtimeMCPCallResult{}, err
				}

				publishRuntimeMCPToolProgress(rc, "Calling MCP tool")
				response, callErr := service.CallResolvedTool(approvalCtx, target)
				result := newRuntimeMCPCallResult(response)
				if callErr == nil && response.IsError {
					message := strings.TrimSpace(result.Text)
					if message == "" {
						message = "MCP tool returned an error result"
					}
					callErr = errors.New(message)
				}
				publishRuntimeMCPToolCompleted(rc, &result, callErr)
				if callErr != nil {
					return runtimeMCPCallResult{}, callErr
				}
				return result, nil
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(runtimeMCPCallTimeout),
		),
	}
	for i := range tools {
		tools[i].Definition.Namespace = runtimeMCPToolNamespace
	}
	return tools
}

func validateRuntimeMCPArguments(args map[string]any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("encode MCP arguments: %w", err)
	}
	if len(raw) > runtimeToolPayloadMaxBytes {
		return fmt.Errorf("MCP arguments exceed %d bytes", runtimeToolPayloadMaxBytes)
	}
	return nil
}

func newRuntimeMCPCallResult(response appmcp.ToolCallResponse) runtimeMCPCallResult {
	raw, _ := json.Marshal(response.Result)
	text := response.Text
	nonTextOmitted := text == "" && len(response.Content) > 0
	if nonTextOmitted {
		text = runtimeMCPNonTextOmittedMarker
	}
	boundedText, textTruncated := boundedRuntimeMCPText(text, runtimeMCPCallTextMaxBytes)
	structured, structuredTruncated := boundedRuntimeMCPValue(response.StructuredContent, runtimeMCPStructuredMaxBytes, "MCP structured content exceeds model payload limit")
	truncated := textTruncated || structuredTruncated || len(raw) > runtimeMCPResultMaxBytes || nonTextOmitted
	meta := map[string]any{
		"gollem": map[string]any{
			"bytes":                 len(raw),
			"sha256":                runtimeSHA256(raw),
			"truncated":             truncated,
			"nonTextContentOmitted": nonTextOmitted,
		},
	}
	content := make([]runtimeMCPContentPayload, 0, 1)
	if boundedText != "" {
		content = append(content, runtimeMCPContentPayload{Type: "text", Text: boundedText})
	}
	itemResult := &runtimeMCPToolCallResultPayload{Content: content, StructuredContent: structured, Meta: meta}
	return runtimeMCPCallResult{
		Server:            response.ServerName,
		Tool:              response.ToolName,
		Text:              boundedText,
		StructuredContent: structured,
		IsError:           response.IsError,
		Bytes:             len(raw),
		SHA256:            runtimeSHA256(raw),
		Truncated:         truncated,
		NonTextOmitted:    nonTextOmitted,
		ItemResult:        itemResult,
	}
}

func boundedRuntimeMCPListErrors(errorsIn []appmcp.ToolListError) []appmcp.ToolListError {
	if len(errorsIn) > runtimeMCPMaxServers {
		errorsIn = errorsIn[:runtimeMCPMaxServers]
	}
	out := make([]appmcp.ToolListError, 0, len(errorsIn))
	for _, item := range errorsIn {
		message, _ := boundedRuntimeMCPText(item.Message, runtimeMCPMetadataMaxBytes)
		serverID, _ := boundedRuntimeMCPText(item.ServerID, runtimeMCPIdentifierMaxBytes)
		out = append(out, appmcp.ToolListError{ServerID: serverID, Message: message})
	}
	return out
}

func boundedRuntimeMCPRawJSON(raw json.RawMessage, limit int, reason string) any {
	if len(raw) == 0 {
		return map[string]any{"type": "object"}
	}
	if len(raw) > limit {
		return runtimeToolPayloadSummary{Omitted: true, Reason: reason, Bytes: len(raw), SHA256: runtimeSHA256(raw)}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return runtimeToolPayloadSummary{Omitted: true, Reason: "MCP JSON payload is invalid", Bytes: len(raw), SHA256: runtimeSHA256(raw)}
	}
	return value
}

func boundedRuntimeMCPValue(value any, limit int, reason string) (any, bool) {
	if value == nil {
		return nil, false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return runtimeToolPayloadSummary{Omitted: true, Reason: "MCP value could not be encoded"}, true
	}
	return boundedRuntimeMCPRawJSON(raw, limit, reason), len(raw) > limit
}

func boundedRuntimeMCPText(value string, limit int) (string, bool) {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	marker := runtimeMCPTruncationMarker
	if len(marker) >= limit {
		return boundedRuntimeMCPPrefix(marker, limit), true
	}
	remaining := limit - len(marker)
	headLimit := remaining / 2
	tailLimit := remaining - headLimit
	return boundedRuntimeMCPPrefix(value, headLimit) + marker + boundedRuntimeMCPSuffix(value, tailLimit), true
}

func boundedRuntimeMCPPrefix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func boundedRuntimeMCPSuffix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	start := len(value) - limit
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func publishRuntimeMCPToolStarted(rc *core.RunContext, event runtimeMCPToolStartedEvent) {
	if rc == nil || rc.EventBus == nil {
		return
	}
	core.Publish(rc.EventBus, event)
}

func publishRuntimeMCPToolProgress(rc *core.RunContext, message string) {
	if rc == nil || rc.EventBus == nil {
		return
	}
	core.Publish(rc.EventBus, runtimeMCPToolProgressEvent{
		RunID:      runtimeRunID(rc),
		ToolCallID: rc.ToolCallID,
		ToolName:   runtimeToolName(rc, "mcp_call_tool"),
		Message:    message,
	})
}

func publishRuntimeMCPToolCompleted(rc *core.RunContext, result *runtimeMCPCallResult, err error) {
	if rc == nil || rc.EventBus == nil {
		return
	}
	core.Publish(rc.EventBus, runtimeMCPToolCompletedEvent{
		RunID:       runtimeRunID(rc),
		ToolCallID:  rc.ToolCallID,
		ToolName:    runtimeToolName(rc, "mcp_call_tool"),
		Result:      result,
		Error:       runtimeErrorText(err),
		CompletedAt: time.Now().UTC(),
	})
}
