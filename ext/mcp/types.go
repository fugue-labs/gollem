package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ProtocolVersion = "2025-11-25"
	protocolVersion = ProtocolVersion
	clientName      = "gollem"
	clientVersion   = "1.0.0"
)

// EmptyCapability is used for presence-only MCP capabilities.
type EmptyCapability struct{}

// ClientCapabilities describes the protocol surfaces exposed by an MCP client.
type ClientCapabilities struct {
	Roots        *RootsCapability          `json:"roots,omitempty"`
	Sampling     *ClientSamplingCapability `json:"sampling,omitempty"`
	Elicitation  *ElicitationCapability    `json:"elicitation,omitempty"`
	Experimental map[string]map[string]any `json:"experimental,omitempty"`
}

// ClientSamplingCapability describes client-side sampling support.
type ClientSamplingCapability struct {
	Context *EmptyCapability `json:"context,omitempty"`
	Tools   *EmptyCapability `json:"tools,omitempty"`
}

// ElicitationCapability describes client-side elicitation support.
type ElicitationCapability struct {
	Form *EmptyCapability `json:"form,omitempty"`
	URL  *EmptyCapability `json:"url,omitempty"`
}

// RootsCapability describes client-side root listing support.
type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerCapabilities describes the protocol surfaces exposed by the server.
type ServerCapabilities struct {
	Tools        *ToolCapabilities     `json:"tools,omitempty"`
	Prompts      *PromptCapabilities   `json:"prompts,omitempty"`
	Resources    *ResourceCapabilities `json:"resources,omitempty"`
	Experimental map[string]any        `json:"experimental,omitempty"`
}

// ToolCapabilities describes the server's tool support.
type ToolCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptCapabilities describes the server's prompt support.
type PromptCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourceCapabilities describes the server's resource support.
type ResourceCapabilities struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerInfo identifies the connected MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ImplementationInfo identifies an MCP implementation.
type ImplementationInfo = ServerInfo

// InitializeParams is sent by the client during initialize.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ImplementationInfo `json:"clientInfo"`
}

// InitializeResult is returned by the MCP initialize handshake.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      *ServerInfo        `json:"serverInfo,omitempty"`
	Instructions    string             `json:"instructions,omitempty"`
	Meta            map[string]any     `json:"_meta,omitempty"`
}

// Root is a client-declared root URI visible to the server.
type Root struct {
	URI  string         `json:"uri"`
	Name string         `json:"name,omitempty"`
	Meta map[string]any `json:"_meta,omitempty"`
}

// ListRootsResult is returned from roots/list.
type ListRootsResult struct {
	Roots []Root         `json:"roots"`
	Meta  map[string]any `json:"_meta,omitempty"`
}

// Tool represents a tool definition from an MCP server.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Prompt represents a prompt definition from an MCP server.
type Prompt struct {
	Name         string           `json:"name"`
	OriginalName string           `json:"originalName,omitempty"`
	Description  string           `json:"description,omitempty"`
	Arguments    []PromptArgument `json:"arguments,omitempty"`
	Server       string           `json:"server,omitempty"`
}

// PromptArgument describes a prompt argument.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// ModelHint suggests a model family or name for client-side selection.
type ModelHint struct {
	Name string `json:"name,omitempty"`
}

// ModelPreferences guides the client when selecting a model for sampling.
type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	CostPriority         float64     `json:"costPriority,omitempty"`
	SpeedPriority        float64     `json:"speedPriority,omitempty"`
	IntelligencePriority float64     `json:"intelligencePriority,omitempty"`
}

// SamplingToolChoice controls tool use during sampling.
type SamplingToolChoice struct {
	Mode string `json:"mode,omitempty"`
}

// SamplingTool defines a tool the model may use during sampling.
type SamplingTool = Tool

// SamplingMessage is a single message in an MCP sampling request or response.
// The content field may be either a single content block or an array of blocks.
type SamplingMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Meta    map[string]any  `json:"_meta,omitempty"`
}

// CreateMessageParams is the request payload for sampling/createMessage.
type CreateMessageParams struct {
	Messages         []SamplingMessage   `json:"messages"`
	ModelPreferences *ModelPreferences   `json:"modelPreferences,omitempty"`
	SystemPrompt     string              `json:"systemPrompt,omitempty"`
	IncludeContext   string              `json:"includeContext,omitempty"`
	Temperature      *float64            `json:"temperature,omitempty"`
	MaxTokens        int                 `json:"maxTokens"`
	StopSequences    []string            `json:"stopSequences,omitempty"`
	Metadata         map[string]any      `json:"metadata,omitempty"`
	Tools            []SamplingTool      `json:"tools,omitempty"`
	ToolChoice       *SamplingToolChoice `json:"toolChoice,omitempty"`
	Meta             map[string]any      `json:"_meta,omitempty"`
}

// CreateMessageResult is returned by a client in response to sampling/createMessage.
type CreateMessageResult struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model"`
	StopReason string          `json:"stopReason,omitempty"`
	Meta       map[string]any  `json:"_meta,omitempty"`
}

// ElicitationParams is the request payload for elicitation/create.
// When Mode is omitted, clients should treat it as form mode.
type ElicitationParams struct {
	Mode            string          `json:"mode,omitempty"`
	Message         string          `json:"message"`
	RequestedSchema json.RawMessage `json:"requestedSchema,omitempty"`
	ElicitationID   string          `json:"elicitationId,omitempty"`
	URL             string          `json:"url,omitempty"`
	Meta            map[string]any  `json:"_meta,omitempty"`
}

// ElicitationResult is returned by a client in response to elicitation/create.
type ElicitationResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
	Meta    map[string]any `json:"_meta,omitempty"`
}

// PromptMessage is a single message returned by prompts/get.
type PromptMessage struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}

// PromptResult is the result of prompts/get.
type PromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
	Meta        map[string]any  `json:"_meta,omitempty"`
}

// Resource describes a resource exposed by an MCP server.
type Resource struct {
	URI         string         `json:"uri"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	MIMEType    string         `json:"mimeType,omitempty"`
	Size        int64          `json:"size,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Server      string         `json:"server,omitempty"`
}

// ResourceTemplate describes a templated resource URI.
type ResourceTemplate struct {
	URITemplate  string         `json:"uriTemplate"`
	Name         string         `json:"name,omitempty"`
	OriginalName string         `json:"originalName,omitempty"`
	Description  string         `json:"description,omitempty"`
	MIMEType     string         `json:"mimeType,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	Server       string         `json:"server,omitempty"`
}

// ResourceContents is a single entry returned by resources/read.
type ResourceContents struct {
	URI      string          `json:"uri"`
	MIMEType string          `json:"mimeType,omitempty"`
	Text     string          `json:"text,omitempty"`
	Blob     string          `json:"blob,omitempty"`
	Data     any             `json:"data,omitempty"`
	Meta     map[string]any  `json:"_meta,omitempty"`
	Raw      json.RawMessage `json:"-"`
}

// ReadResourceResult is the result of resources/read.
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
	Meta     map[string]any     `json:"_meta,omitempty"`
}

// ToolResult represents the result of an MCP tool call.
type ToolResult struct {
	Content           []Content      `json:"content"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
	Meta              map[string]any `json:"_meta,omitempty"`
}

// Content represents a content block in an MCP response.
type Content struct {
	Type              string            `json:"type"`
	Text              string            `json:"text,omitempty"`
	MIMEType          string            `json:"mimeType,omitempty"`
	URI               string            `json:"uri,omitempty"`
	Blob              string            `json:"blob,omitempty"`
	Data              any               `json:"data,omitempty"`
	ID                string            `json:"id,omitempty"`
	Name              string            `json:"name,omitempty"`
	Input             json.RawMessage   `json:"input,omitempty"`
	ToolUseID         string            `json:"toolUseId,omitempty"`
	Resource          *ResourceContents `json:"resource,omitempty"`
	Content           []Content         `json:"content,omitempty"`
	StructuredContent any               `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError,omitempty"`
	Annotations       map[string]any    `json:"annotations,omitempty"`
	Meta              map[string]any    `json:"_meta,omitempty"`
	Raw               json.RawMessage   `json:"-"`
}

func (c *Content) UnmarshalJSON(data []byte) error {
	type contentWire Content
	var decoded contentWire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = Content(decoded)
	c.Raw = append(c.Raw[:0], data...)
	return nil
}

func (c *ResourceContents) UnmarshalJSON(data []byte) error {
	type resourceContentsWire ResourceContents
	var decoded resourceContentsWire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = ResourceContents(decoded)
	c.Raw = append(c.Raw[:0], data...)
	return nil
}

// TextContent returns the concatenated textual content from the tool result.
// If no text blocks are present, it falls back to structured content JSON.
func (r *ToolResult) TextContent() string {
	return joinContentText(r.Content, r.StructuredContent)
}

// TextContent returns the concatenated textual content from the resource.
func (r *ReadResourceResult) TextContent() string {
	if r == nil {
		return ""
	}
	parts := make([]string, 0, len(r.Contents))
	for _, content := range r.Contents {
		if text := content.textContent(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// TextContent returns the concatenated textual content from the prompt result.
func (r *PromptResult) TextContent() string {
	if r == nil {
		return ""
	}
	parts := make([]string, 0, len(r.Messages))
	for _, message := range r.Messages {
		text := message.Content.textContent()
		if text == "" {
			continue
		}
		if message.Role != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", message.Role, text))
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

// ParseSamplingContent parses a sampling content field that may contain either
// a single block or an array of blocks.
func ParseSamplingContent(raw json.RawMessage) ([]Content, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var blocks []Content
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
		return blocks, nil
	}

	var block Content
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, err
	}
	return []Content{block}, nil
}

// MarshalSamplingContent marshals a single sampling content block.
func MarshalSamplingContent(block Content) json.RawMessage {
	data, _ := json.Marshal(block)
	return data
}

// MarshalSamplingContentArray marshals multiple sampling content blocks.
func MarshalSamplingContentArray(blocks []Content) json.RawMessage {
	data, _ := json.Marshal(blocks)
	return data
}

func joinContentText(content []Content, structured any) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if text := block.textContent(); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		if fallback := stringifyContentFallback(structured); fallback != "" {
			parts = append(parts, fallback)
		}
	}
	return strings.Join(parts, "\n")
}

func (c Content) textContent() string {
	switch {
	case c.Text != "":
		return c.Text
	case c.Resource != nil:
		return c.Resource.textContent()
	case c.Data != nil:
		return stringifyContentFallback(c.Data)
	case c.Blob != "":
		return c.Blob
	case c.URI != "":
		return c.URI
	default:
		return ""
	}
}

func (c ResourceContents) textContent() string {
	switch {
	case c.Text != "":
		return c.Text
	case c.Data != nil:
		return stringifyContentFallback(c.Data)
	case c.Blob != "":
		return c.Blob
	case c.URI != "":
		return c.URI
	default:
		return ""
	}
}

func stringifyContentFallback(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.RawMessage:
		return string(v)
	default:
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return ""
		}
		return string(data)
	}
}

type listToolsResult struct {
	Tools []Tool `json:"tools"`
}

type listResourcesResult struct {
	Resources []Resource `json:"resources"`
}

type listResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
}

type listPromptsResult struct {
	Prompts []Prompt `json:"prompts"`
}
