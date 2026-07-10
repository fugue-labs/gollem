package catalog

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	anthropicprovider "github.com/fugue-labs/gollem/provider/anthropic"
	openaiprovider "github.com/fugue-labs/gollem/provider/openai"
	vertexprovider "github.com/fugue-labs/gollem/provider/vertexai"
	vertexanthropicprovider "github.com/fugue-labs/gollem/provider/vertexai_anthropic"
)

const (
	ProviderOpenAI            = "openai"
	ProviderAnthropic         = "anthropic"
	ProviderVertexAI          = "vertexai"
	ProviderVertexAIAnthropic = "vertexai-anthropic"
)

var ErrProviderNotFound = errors.New("provider not found")

type EnvLookup func(string) (string, bool)

type Option func(*Catalog)

func WithEnvLookup(lookup EnvLookup) Option {
	return func(c *Catalog) {
		if lookup != nil {
			c.env = lookup
		}
	}
}

type Catalog struct {
	env       EnvLookup
	providers []Provider
}

func NewDefault(opts ...Option) *Catalog {
	c := &Catalog{
		env: os.LookupEnv,
	}
	for _, opt := range opts {
		opt(c)
	}
	c.providers = c.defaultProviders()
	return c
}

type Provider struct {
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	Package         string               `json:"package"`
	Description     string               `json:"description"`
	Configured      bool                 `json:"configured"`
	Hidden          bool                 `json:"hidden"`
	DefaultModelID  string               `json:"defaultModelId"`
	RequiredEnvVars []string             `json:"requiredEnvVars,omitempty"`
	OptionalEnvVars []string             `json:"optionalEnvVars,omitempty"`
	AuthModes       []string             `json:"authModes,omitempty"`
	Capabilities    ProviderCapabilities `json:"capabilities"`
	Models          []Model              `json:"models,omitempty"`
}

type ProviderCapabilities struct {
	ProviderID               string   `json:"providerId,omitempty"`
	Configured               bool     `json:"configured"`
	NamespaceTools           bool     `json:"namespaceTools"`
	ImageGeneration          bool     `json:"imageGeneration"`
	WebSearch                bool     `json:"webSearch"`
	ToolCalls                bool     `json:"toolCalls"`
	StructuredOutput         bool     `json:"structuredOutput"`
	Vision                   bool     `json:"vision"`
	Streaming                bool     `json:"streaming"`
	PromptCache              bool     `json:"promptCache"`
	ToolSearch               bool     `json:"toolSearch"`
	Reasoning                bool     `json:"reasoning"`
	ReasoningEfforts         []string `json:"reasoningEfforts,omitempty"`
	ReasoningSummaries       bool     `json:"reasoningSummaries,omitempty"`
	AdaptiveThinking         bool     `json:"adaptiveThinking,omitempty"`
	ManualThinking           bool     `json:"manualThinking,omitempty"`
	RequiresConfigurationEnv []string `json:"requiresConfigurationEnv,omitempty"`
}

type ModelCapabilities struct {
	ToolCalls        bool `json:"toolCalls"`
	StructuredOutput bool `json:"structuredOutput"`
	Vision           bool `json:"vision"`
	Streaming        bool `json:"streaming"`
	PromptCache      bool `json:"promptCache"`
	ToolSearch       bool `json:"toolSearch"`
	Reasoning        bool `json:"reasoning"`
}

type ReasoningEffortOption struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

type ModelServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ModelUpgradeInfo struct {
	Model             string  `json:"model"`
	UpgradeCopy       *string `json:"upgradeCopy"`
	ModelLink         *string `json:"modelLink"`
	MigrationMarkdown *string `json:"migrationMarkdown"`
}

type ModelAvailabilityNux struct {
	Message string `json:"message"`
}

type Model struct {
	ID                        string                  `json:"id"`
	ProviderID                string                  `json:"providerId"`
	Model                     string                  `json:"model"`
	Upgrade                   *string                 `json:"upgrade"`
	UpgradeInfo               *ModelUpgradeInfo       `json:"upgradeInfo"`
	AvailabilityNux           *ModelAvailabilityNux   `json:"availabilityNux"`
	DisplayName               string                  `json:"displayName"`
	Description               string                  `json:"description"`
	Hidden                    bool                    `json:"hidden"`
	SupportedReasoningEfforts []ReasoningEffortOption `json:"supportedReasoningEfforts"`
	DefaultReasoningEffort    string                  `json:"defaultReasoningEffort"`
	InputModalities           []string                `json:"inputModalities"`
	SupportsPersonality       bool                    `json:"supportsPersonality"`
	AdditionalSpeedTiers      []string                `json:"additionalSpeedTiers"`
	ServiceTiers              []ModelServiceTier      `json:"serviceTiers"`
	DefaultServiceTier        *string                 `json:"defaultServiceTier"`
	IsDefault                 bool                    `json:"isDefault"`
	Capabilities              ModelCapabilities       `json:"capabilities"`
	MaxContextTokens          int                     `json:"maxContextTokens,omitempty"`
	MaxOutputTokens           int                     `json:"maxOutputTokens,omitempty"`
}

type ModelListParams struct {
	Cursor        *string  `json:"cursor,omitempty"`
	Limit         *uint32  `json:"limit,omitempty"`
	IncludeHidden *bool    `json:"includeHidden,omitempty"`
	ProviderID    string   `json:"providerId,omitempty"`
	ProviderIDs   []string `json:"providerIds,omitempty"`
}

type ModelListResponse struct {
	Data       []Model `json:"data"`
	NextCursor *string `json:"nextCursor"`
}

type ProviderListParams struct {
	IncludeHidden  *bool `json:"includeHidden,omitempty"`
	ConfiguredOnly bool  `json:"configuredOnly,omitempty"`
}

type ProviderListResponse struct {
	Data      []Provider `json:"data"`
	Providers []Provider `json:"providers"`
}

type CapabilitiesReadParams struct {
	ProviderID    string `json:"providerId,omitempty"`
	Provider      string `json:"provider,omitempty"`
	ModelProvider string `json:"modelProvider,omitempty"`
}

type ToolListParams struct {
	IncludeUnavailable bool   `json:"includeUnavailable,omitempty"`
	Category           string `json:"category,omitempty"`
	Source             string `json:"source,omitempty"`
}

type ToolServices struct {
	Filesystem   bool
	Process      bool
	Git          bool
	Cache        bool
	Config       bool
	MCP          bool
	Skills       bool
	Runtime      bool
	Interactions bool
	Memory       bool
}

type Tool struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	DisplayName        string   `json:"displayName"`
	Description        string   `json:"description"`
	Source             string   `json:"source"`
	Category           string   `json:"category"`
	Kind               string   `json:"kind"`
	Methods            []string `json:"methods"`
	Enabled            bool     `json:"enabled"`
	Available          bool     `json:"available"`
	Mutation           bool     `json:"mutation"`
	RequiresApproval   bool     `json:"requiresApproval"`
	ServiceConfigured  bool     `json:"serviceConfigured"`
	UnavailableReason  string   `json:"unavailableReason,omitempty"`
	CodexCompatible    bool     `json:"codexCompatible"`
	GollemExtension    bool     `json:"gollemExtension"`
	OutputNotification string   `json:"outputNotification,omitempty"`
}

type ToolListResponse struct {
	Data  []Tool `json:"data"`
	Tools []Tool `json:"tools"`
}

func (c *Catalog) ListProviders(params ProviderListParams) ProviderListResponse {
	c = ensureCatalog(c)
	includeHidden := boolValue(params.IncludeHidden)
	providers := make([]Provider, 0, len(c.providers))
	for _, provider := range c.providers {
		if provider.Hidden && !includeHidden {
			continue
		}
		if params.ConfiguredOnly && !provider.Configured {
			continue
		}
		providers = append(providers, cloneProvider(provider))
	}
	return ProviderListResponse{
		Data:      providers,
		Providers: cloneProviders(providers),
	}
}

func (c *Catalog) ListModels(params ModelListParams) (ModelListResponse, error) {
	c = ensureCatalog(c)
	includeHidden := boolValue(params.IncludeHidden)
	providerFilter := providerFilter(params.ProviderID, params.ProviderIDs)

	var models []Model
	for _, provider := range c.providers {
		if len(providerFilter) > 0 {
			if _, ok := providerFilter[provider.ID]; !ok {
				continue
			}
		}
		if provider.Hidden && !includeHidden {
			continue
		}
		for _, model := range provider.Models {
			if model.Hidden && !includeHidden {
				continue
			}
			models = append(models, cloneModel(model))
		}
	}
	markDefaultModel(models, c.providers)

	total := len(models)
	if total == 0 {
		return ModelListResponse{Data: []Model{}}, nil
	}
	limit := total
	if params.Limit != nil && *params.Limit > 0 {
		limit = min(int(*params.Limit), total)
	}
	start := 0
	if params.Cursor != nil && strings.TrimSpace(*params.Cursor) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(*params.Cursor))
		if err != nil || n < 0 {
			return ModelListResponse{}, fmt.Errorf("invalid cursor %q", *params.Cursor)
		}
		if n > total {
			return ModelListResponse{}, fmt.Errorf("cursor %d exceeds total models %d", n, total)
		}
		start = n
	}
	end := min(start+limit, total)
	var next *string
	if end < total {
		cursor := strconv.Itoa(end)
		next = &cursor
	}
	return ModelListResponse{Data: models[start:end], NextCursor: next}, nil
}

func (c *Catalog) ProviderCapabilities(providerID string) (ProviderCapabilities, error) {
	c = ensureCatalog(c)
	providerID = normalizeProviderID(providerID)
	if providerID != "" {
		for _, provider := range c.providers {
			if provider.ID == providerID {
				caps := cloneCapabilities(provider.Capabilities)
				caps.ProviderID = provider.ID
				caps.Configured = provider.Configured
				caps.RequiresConfigurationEnv = append([]string(nil), provider.RequiredEnvVars...)
				return caps, nil
			}
		}
		return ProviderCapabilities{}, fmt.Errorf("%w: %s", ErrProviderNotFound, providerID)
	}
	var aggregate ProviderCapabilities
	for _, provider := range c.providers {
		aggregate.NamespaceTools = aggregate.NamespaceTools || provider.Capabilities.NamespaceTools
		aggregate.ImageGeneration = aggregate.ImageGeneration || provider.Capabilities.ImageGeneration
		aggregate.WebSearch = aggregate.WebSearch || provider.Capabilities.WebSearch
		aggregate.ToolCalls = aggregate.ToolCalls || provider.Capabilities.ToolCalls
		aggregate.StructuredOutput = aggregate.StructuredOutput || provider.Capabilities.StructuredOutput
		aggregate.Vision = aggregate.Vision || provider.Capabilities.Vision
		aggregate.Streaming = aggregate.Streaming || provider.Capabilities.Streaming
		aggregate.PromptCache = aggregate.PromptCache || provider.Capabilities.PromptCache
		aggregate.ToolSearch = aggregate.ToolSearch || provider.Capabilities.ToolSearch
		aggregate.Reasoning = aggregate.Reasoning || provider.Capabilities.Reasoning
		aggregate.ReasoningSummaries = aggregate.ReasoningSummaries || provider.Capabilities.ReasoningSummaries
		aggregate.AdaptiveThinking = aggregate.AdaptiveThinking || provider.Capabilities.AdaptiveThinking
		aggregate.ManualThinking = aggregate.ManualThinking || provider.Capabilities.ManualThinking
		aggregate.Configured = aggregate.Configured || provider.Configured
		aggregate.ReasoningEfforts = appendUnique(aggregate.ReasoningEfforts, provider.Capabilities.ReasoningEfforts...)
	}
	return aggregate, nil
}

func ListTools(params ToolListParams, services ToolServices) ToolListResponse {
	tools := builtinTools(services)
	filtered := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if !params.IncludeUnavailable && !tool.Available {
			continue
		}
		if params.Category != "" && tool.Category != params.Category {
			continue
		}
		if params.Source != "" && tool.Source != params.Source {
			continue
		}
		filtered = append(filtered, cloneTool(tool))
	}
	return ToolListResponse{
		Data:  filtered,
		Tools: cloneTools(filtered),
	}
}

func (c *Catalog) defaultProviders() []Provider {
	openaiConfigured := envConfigured(c.env, "OPENAI_API_KEY") || envConfigured(c.env, "CHATGPT_ACCESS_TOKEN")
	anthropicConfigured := envConfigured(c.env, "ANTHROPIC_API_KEY")
	vertexConfigured := envConfigured(c.env, "GOOGLE_CLOUD_PROJECT") || envConfigured(c.env, "GOOGLE_APPLICATION_CREDENTIALS")

	return []Provider{
		{
			ID:              ProviderOpenAI,
			Name:            "OpenAI",
			Package:         "github.com/fugue-labs/gollem/provider/openai",
			Description:     "OpenAI Responses and Chat Completions models through Gollem's provider-neutral core.Model interface.",
			Configured:      openaiConfigured,
			DefaultModelID:  modelID(ProviderOpenAI, openaiprovider.GPT4o),
			RequiredEnvVars: []string{"OPENAI_API_KEY"},
			OptionalEnvVars: []string{"OPENAI_BASE_URL", "OPENAI_SERVICE_TIER", "CHATGPT_ACCESS_TOKEN"},
			AuthModes:       []string{"api-key", "chatgpt-subscription"},
			Capabilities: ProviderCapabilities{
				NamespaceTools:     true,
				ToolCalls:          true,
				StructuredOutput:   true,
				Vision:             true,
				Streaming:          true,
				PromptCache:        true,
				ToolSearch:         true,
				Reasoning:          true,
				ReasoningEfforts:   []string{"minimal", "low", "medium", "high"},
				ReasoningSummaries: true,
			},
			Models: []Model{
				model(ProviderOpenAI, openaiprovider.GPT4o, "GPT-4o", "General-purpose multimodal OpenAI model.", false, textAndImage(), false, capabilities(true, true, true, true, true, false, false), []string{"low", "medium", "high"}, "medium"),
				model(ProviderOpenAI, openaiprovider.GPT4oMini, "GPT-4o mini", "Smaller OpenAI multimodal model for lower-latency turns.", false, textAndImage(), false, capabilities(true, true, true, true, true, false, false), []string{"low", "medium", "high"}, "medium"),
				model(ProviderOpenAI, openaiprovider.GPT5, "GPT-5", "OpenAI reasoning model with strong coding and tool-use support.", false, textAndImage(), false, capabilities(true, true, true, true, true, true, false), []string{"minimal", "low", "medium", "high"}, "medium"),
				model(ProviderOpenAI, openaiprovider.GPT5Mini, "GPT-5 mini", "Lower-latency GPT-5 family model.", false, textAndImage(), false, capabilities(true, true, true, true, true, true, false), []string{"minimal", "low", "medium", "high"}, "medium"),
				model(ProviderOpenAI, openaiprovider.GPT5Nano, "GPT-5 nano", "Small GPT-5 family model for fast utility work.", true, textAndImage(), false, capabilities(true, true, true, true, true, true, false), []string{"minimal", "low", "medium", "high"}, "low"),
				model(ProviderOpenAI, openaiprovider.GPT5Codex, "GPT-5 Codex", "OpenAI coding-specialized model exposed through Gollem's neutral model controls.", false, textAndImage(), false, capabilities(true, true, true, true, true, true, true), []string{"minimal", "low", "medium", "high"}, "medium"),
			},
		},
		{
			ID:              ProviderAnthropic,
			Name:            "Anthropic",
			Package:         "github.com/fugue-labs/gollem/provider/anthropic",
			Description:     "Anthropic Messages API models through Gollem's provider-neutral core.Model interface.",
			Configured:      anthropicConfigured,
			DefaultModelID:  modelID(ProviderAnthropic, anthropicprovider.ClaudeSonnet46),
			RequiredEnvVars: []string{"ANTHROPIC_API_KEY"},
			AuthModes:       []string{"api-key"},
			Capabilities: ProviderCapabilities{
				ToolCalls:          true,
				StructuredOutput:   true,
				Vision:             true,
				Streaming:          true,
				PromptCache:        true,
				ToolSearch:         true,
				Reasoning:          true,
				ReasoningEfforts:   []string{"low", "medium", "high", "xhigh", "max"},
				ReasoningSummaries: true,
				AdaptiveThinking:   true,
				ManualThinking:     true,
			},
			Models: []Model{
				model(ProviderAnthropic, anthropicprovider.ClaudeSonnet46, "Claude Sonnet 4.6", "Anthropic balanced coding and agentic workflow model.", false, textAndImage(), false, capabilities(true, true, true, true, true, true, true), []string{"low", "medium", "high", "max"}, "medium"),
				model(ProviderAnthropic, anthropicprovider.ClaudeOpus46, "Claude Opus 4.6", "Anthropic high-capability reasoning model.", false, textAndImage(), false, capabilities(true, true, true, true, true, true, true), []string{"low", "medium", "high", "max"}, "medium"),
				model(ProviderAnthropic, anthropicprovider.ClaudeOpus47, "Claude Opus 4.7", "Anthropic flagship adaptive-thinking model.", false, textAndImage(), false, capabilities(true, true, true, true, true, true, true), []string{"low", "medium", "high", "xhigh", "max"}, "high"),
				model(ProviderAnthropic, anthropicprovider.ClaudeOpus48, "Claude Opus 4.8", "Anthropic flagship adaptive-thinking model.", false, textAndImage(), false, capabilities(true, true, true, true, true, true, true), []string{"low", "medium", "high", "xhigh", "max"}, "high"),
				model(ProviderAnthropic, anthropicprovider.ClaudeFable5, "Claude Fable 5", "Anthropic Fable tier model for high-end reasoning workflows.", true, textAndImage(), false, capabilities(true, true, true, true, true, true, true), []string{"low", "medium", "high", "xhigh", "max"}, "high"),
				model(ProviderAnthropic, anthropicprovider.ClaudeHaiku45, "Claude Haiku 4.5", "Anthropic lower-latency utility model.", false, textAndImage(), false, capabilities(true, true, true, true, true, false, false), []string{"low", "medium", "high"}, "medium"),
			},
		},
		{
			ID:              ProviderVertexAI,
			Name:            "Vertex AI Gemini",
			Package:         "github.com/fugue-labs/gollem/provider/vertexai",
			Description:     "Google Gemini models through Vertex AI and Gollem's provider-neutral core.Model interface.",
			Configured:      vertexConfigured,
			Hidden:          true,
			DefaultModelID:  modelID(ProviderVertexAI, vertexprovider.Gemini25Flash),
			RequiredEnvVars: []string{"GOOGLE_CLOUD_PROJECT"},
			OptionalEnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS", "VERTEXAI_CACHED_CONTENT"},
			AuthModes:       []string{"application-default-credentials", "service-account"},
			Capabilities: ProviderCapabilities{
				ToolCalls:        true,
				StructuredOutput: true,
				Vision:           true,
				Streaming:        true,
				PromptCache:      true,
			},
			Models: []Model{
				model(ProviderVertexAI, vertexprovider.Gemini25Flash, "Gemini 2.5 Flash", "Google Gemini fast multimodal model through Vertex AI.", true, textAndImage(), false, capabilities(true, true, true, true, true, false, false), nil, "medium"),
				model(ProviderVertexAI, vertexprovider.Gemini25Pro, "Gemini 2.5 Pro", "Google Gemini Pro multimodal model through Vertex AI.", true, textAndImage(), false, capabilities(true, true, true, true, true, false, false), nil, "medium"),
				model(ProviderVertexAI, vertexprovider.Gemini31ProPreview, "Gemini 3.1 Pro Preview", "Google Gemini preview model through Vertex AI.", true, textAndImage(), false, capabilities(true, true, true, true, true, false, false), nil, "medium"),
				model(ProviderVertexAI, vertexprovider.Gemini3FlashPreview, "Gemini 3 Flash Preview", "Google Gemini preview flash model through Vertex AI.", true, textAndImage(), false, capabilities(true, true, true, true, true, false, false), nil, "medium"),
				model(ProviderVertexAI, vertexprovider.Gemini20Flash, "Gemini 2.0 Flash", "Google Gemini 2.0 Flash model through Vertex AI.", true, textAndImage(), false, capabilities(true, true, true, true, true, false, false), nil, "medium"),
			},
		},
		{
			ID:              ProviderVertexAIAnthropic,
			Name:            "Vertex AI Anthropic",
			Package:         "github.com/fugue-labs/gollem/provider/vertexai_anthropic",
			Description:     "Anthropic Claude models accessed through Google Vertex AI rawPredict.",
			Configured:      vertexConfigured,
			Hidden:          true,
			DefaultModelID:  modelID(ProviderVertexAIAnthropic, vertexanthropicprovider.ClaudeSonnet46),
			RequiredEnvVars: []string{"GOOGLE_CLOUD_PROJECT"},
			OptionalEnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS"},
			AuthModes:       []string{"application-default-credentials", "service-account"},
			Capabilities: ProviderCapabilities{
				ToolCalls:          true,
				StructuredOutput:   true,
				Vision:             true,
				Streaming:          true,
				PromptCache:        true,
				Reasoning:          true,
				ReasoningEfforts:   []string{"low", "medium", "high", "xhigh", "max"},
				ReasoningSummaries: true,
				AdaptiveThinking:   true,
				ManualThinking:     true,
			},
			Models: []Model{
				model(ProviderVertexAIAnthropic, vertexanthropicprovider.ClaudeSonnet46, "Claude Sonnet 4.6 on Vertex AI", "Anthropic Sonnet through Vertex AI.", true, textAndImage(), false, capabilities(true, true, true, true, true, true, false), []string{"low", "medium", "high", "max"}, "medium"),
				model(ProviderVertexAIAnthropic, vertexanthropicprovider.ClaudeOpus46, "Claude Opus 4.6 on Vertex AI", "Anthropic Opus through Vertex AI.", true, textAndImage(), false, capabilities(true, true, true, true, true, true, false), []string{"low", "medium", "high", "max"}, "medium"),
				model(ProviderVertexAIAnthropic, vertexanthropicprovider.ClaudeOpus47, "Claude Opus 4.7 on Vertex AI", "Anthropic Opus through Vertex AI.", true, textAndImage(), false, capabilities(true, true, true, true, true, true, false), []string{"low", "medium", "high", "xhigh", "max"}, "high"),
				model(ProviderVertexAIAnthropic, vertexanthropicprovider.ClaudeHaiku45, "Claude Haiku 4.5 on Vertex AI", "Anthropic Haiku through Vertex AI.", true, textAndImage(), false, capabilities(true, true, true, true, true, false, false), []string{"low", "medium", "high"}, "medium"),
			},
		},
	}
}

func builtinTools(services ToolServices) []Tool {
	return []Tool{
		tool("fs", "Filesystem", "Read, write, copy, remove, inspect, and watch files under the configured workspace root.", "workspace", []string{"fs/readFile", "fs/writeFile", "fs/createDirectory", "fs/readDirectory", "fs/getMetadata", "fs/remove", "fs/copy", "fs/watch", "fs/unwatch"}, services.Filesystem, true, true, "fs/changed", true, false),
		tool("process", "Process", "Spawn, stream, write to, resize, terminate, kill, and clean workspace-scoped processes.", "runtime", []string{"command/exec", "command/exec/write", "command/exec/resize", "command/exec/terminate", "process/spawn", "process/writeStdin", "process/resizePty", "process/kill", "thread/shellCommand", "thread/backgroundTerminals/list", "thread/backgroundTerminals/terminate", "thread/backgroundTerminals/clean"}, services.Process, true, true, "process/outputDelta", true, false),
		tool("git", "Git", "Read repository status and diffs, commit changes, and manage scoped worktrees.", "source-control", []string{"git/status", "git/diff", "git/commit", "git/worktree/list", "git/worktree/create"}, services.Git, true, true, "", false, true),
		tool("thread-store", "Thread store", "List, search, read, unsubscribe, compact, rollback, archive, unarchive, delete, fork, inject history, rename, and configure durable Gollem app-server threads.", "conversation", []string{"thread/list", "thread/search", "thread/loaded/list", "thread/unsubscribe", "thread/read", "thread/fork", "thread/compact/start", "thread/rollback", "thread/archive", "thread/unarchive", "thread/delete", "thread/settings/update", "thread/goal/get", "thread/goal/set", "thread/goal/clear", "thread/metadata/update", "thread/memoryMode/set", "thread/name/set", "thread/turns/list", "thread/items/list", "thread/inject_items"}, true, false, false, "", true, false),
		tool("turn-runtime", "Turn runtime", "Start, resume, interrupt, steer, and retry provider-neutral Gollem app-server turns.", "runtime", []string{"thread/start", "thread/resume", "turn/start", "turn/interrupt", "turn/steer", "turn/retry"}, services.Runtime, true, false, "turn/started", true, true),
		tool("interactions", "Interactions", "Request user input, dynamic tool calls, and MCP elicitation from the connected Slang client.", "runtime", []string{"item/tool/requestUserInput", "item/tool/call", "mcpServer/elicitation/request"}, services.Interactions, false, false, "serverRequest/resolved", true, false),
		tool("provider-catalog", "Provider catalog", "List provider, model, capability, and app-server tool metadata for Slang controls.", "configuration", []string{"provider/list", "model/list", "modelProvider/capabilities/read", "provider/capabilities/read", "tool/list"}, true, false, false, "", true, true),
		tool("config", "Configuration", "Read and update app-server config, environment metadata, permission profiles, collaboration modes, and feature flags.", "configuration", []string{"config/read", "config/value/write", "config/batchWrite", "configRequirements/read", "config/mcpServer/reload", "environment/info", "environment/add", "permissionProfile/list", "collaborationMode/list", "experimentalFeature/list", "experimentalFeature/enablement/set"}, services.Config, true, false, "", true, false),
		tool("mcp", "MCP servers", "List MCP server startup status, read MCP resources, and call MCP tools through registered Gollem MCP clients.", "runtime", []string{"mcpServerStatus/list", "mcpServer/resource/read", "mcpServer/tool/call"}, services.MCP, true, true, "", true, false),
		tool("skills", "Skills and plugins", "List workspace-scoped skills and read plugin manifests and skill content from configured roots.", "configuration", []string{"skills/list", "plugin/list", "plugin/installed", "plugin/read", "plugin/skill/read"}, services.Skills, false, false, "", true, false),
		tool("cache", "Cache", "Read deterministic cache stats and run provider normalization benchmark fixtures.", "runtime", []string{"cache/stats", "cache/benchmark"}, services.Cache, false, false, "", false, true),
		tool("memory", "Memory", "Reset local Gollem memory artifacts without changing persisted thread memory modes.", "conversation", []string{"memory/reset"}, services.Memory, true, true, "", true, false),
	}
}

func tool(id, displayName, description, category string, methods []string, configured, mutation, approval bool, outputNotification string, codexCompatible, extension bool) Tool {
	reason := ""
	if !configured {
		reason = "service is not configured in this app-server instance"
	}
	return Tool{
		ID:                 id,
		Name:               id,
		DisplayName:        displayName,
		Description:        description,
		Source:             "builtin",
		Category:           category,
		Kind:               "app-server",
		Methods:            append([]string(nil), methods...),
		Enabled:            configured,
		Available:          configured,
		Mutation:           mutation,
		RequiresApproval:   approval,
		ServiceConfigured:  configured,
		UnavailableReason:  reason,
		CodexCompatible:    codexCompatible,
		GollemExtension:    extension,
		OutputNotification: outputNotification,
	}
}

func model(providerID, name, displayName, description string, hidden bool, modalities []string, personality bool, caps ModelCapabilities, efforts []string, defaultEffort string) Model {
	return Model{
		ID:                        modelID(providerID, name),
		ProviderID:                providerID,
		Model:                     name,
		DisplayName:               displayName,
		Description:               description,
		Hidden:                    hidden,
		SupportedReasoningEfforts: effortOptions(efforts),
		DefaultReasoningEffort:    defaultEffort,
		InputModalities:           append([]string(nil), modalities...),
		SupportsPersonality:       personality,
		AdditionalSpeedTiers:      []string{},
		ServiceTiers:              []ModelServiceTier{},
		Capabilities:              caps,
	}
}

func capabilities(toolCalls, structured, vision, streaming, promptCache, reasoning, toolSearch bool) ModelCapabilities {
	return ModelCapabilities{
		ToolCalls:        toolCalls,
		StructuredOutput: structured,
		Vision:           vision,
		Streaming:        streaming,
		PromptCache:      promptCache,
		Reasoning:        reasoning,
		ToolSearch:       toolSearch,
	}
}

func effortOptions(efforts []string) []ReasoningEffortOption {
	if len(efforts) == 0 {
		efforts = []string{"low", "medium", "high"}
	}
	out := make([]ReasoningEffortOption, 0, len(efforts))
	for _, effort := range efforts {
		out = append(out, ReasoningEffortOption{
			ReasoningEffort: effort,
			Description:     effortDescription(effort),
		})
	}
	return out
}

func effortDescription(effort string) string {
	switch effort {
	case "minimal":
		return "Fastest response with minimal reasoning."
	case "low":
		return "Lower-latency reasoning."
	case "medium":
		return "Balanced reasoning and latency."
	case "high":
		return "Deeper reasoning for complex work."
	case "xhigh":
		return "Extended high-depth reasoning when supported."
	case "max":
		return "Maximum provider-supported reasoning effort."
	default:
		return "Provider-supported reasoning effort."
	}
}

func modelID(providerID, name string) string {
	return providerID + "/" + name
}

func textAndImage() []string {
	return []string{"text", "image"}
}

func ensureCatalog(c *Catalog) *Catalog {
	if c != nil {
		return c
	}
	return NewDefault()
}

func envConfigured(lookup EnvLookup, names ...string) bool {
	for _, name := range names {
		if value, ok := lookup(name); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func providerFilter(providerID string, providerIDs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(providerIDs)+1)
	if providerID != "" {
		out[normalizeProviderID(providerID)] = struct{}{}
	}
	for _, id := range providerIDs {
		if id != "" {
			out[normalizeProviderID(id)] = struct{}{}
		}
	}
	return out
}

func normalizeProviderID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "vertex", "gemini", "google", "google-vertex":
		return ProviderVertexAI
	case "vertex-anthropic", "vertexaianthropic", "vertex_ai_anthropic":
		return ProviderVertexAIAnthropic
	default:
		return id
	}
}

func markDefaultModel(models []Model, providers []Provider) {
	defaultID := ""
	for _, provider := range providers {
		if provider.Configured {
			defaultID = provider.DefaultModelID
			break
		}
	}
	if defaultID == "" {
		for _, provider := range providers {
			if provider.ID == ProviderOpenAI {
				defaultID = provider.DefaultModelID
				break
			}
		}
	}
	for i := range models {
		models[i].IsDefault = models[i].ID == defaultID
	}
	if !slices.ContainsFunc(models, func(model Model) bool { return model.IsDefault }) && len(models) > 0 {
		models[0].IsDefault = true
	}
}

func cloneProvider(provider Provider) Provider {
	provider.RequiredEnvVars = append([]string(nil), provider.RequiredEnvVars...)
	provider.OptionalEnvVars = append([]string(nil), provider.OptionalEnvVars...)
	provider.AuthModes = append([]string(nil), provider.AuthModes...)
	provider.Capabilities = cloneCapabilities(provider.Capabilities)
	provider.Models = cloneModels(provider.Models)
	return provider
}

func cloneProviders(providers []Provider) []Provider {
	out := make([]Provider, len(providers))
	for i, provider := range providers {
		out[i] = cloneProvider(provider)
	}
	return out
}

func cloneModel(model Model) Model {
	model.SupportedReasoningEfforts = append([]ReasoningEffortOption(nil), model.SupportedReasoningEfforts...)
	model.InputModalities = append([]string(nil), model.InputModalities...)
	model.AdditionalSpeedTiers = append([]string(nil), model.AdditionalSpeedTiers...)
	model.ServiceTiers = append([]ModelServiceTier(nil), model.ServiceTiers...)
	return model
}

func cloneModels(models []Model) []Model {
	out := make([]Model, len(models))
	for i, model := range models {
		out[i] = cloneModel(model)
	}
	return out
}

func cloneCapabilities(caps ProviderCapabilities) ProviderCapabilities {
	caps.ReasoningEfforts = append([]string(nil), caps.ReasoningEfforts...)
	caps.RequiresConfigurationEnv = append([]string(nil), caps.RequiresConfigurationEnv...)
	return caps
}

func cloneTool(tool Tool) Tool {
	tool.Methods = append([]string(nil), tool.Methods...)
	return tool
}

func cloneTools(tools []Tool) []Tool {
	out := make([]Tool, len(tools))
	for i, tool := range tools {
		out[i] = cloneTool(tool)
	}
	return out
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		if addition == "" || slices.Contains(values, addition) {
			continue
		}
		values = append(values, addition)
	}
	return values
}
