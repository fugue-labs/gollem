# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **Adaptive thinking for Anthropic providers.** New
  `ModelSettings.AdaptiveThinking *bool` emits
  `{thinking: {type: "adaptive"}}` from both the `anthropic` and
  `vertexai_anthropic` providers — the model decides when and how much
  to think. Gated per model (Claude 4.6 generation and newer; clear
  build-time error on older models), mutually exclusive with the
  legacy `ThinkingBudget` manual mode, and temperature is omitted on
  the wire as the API requires. Response and stream paths already
  parsed `thinking` blocks; this closes the request side.
- **Opus 4.8 and Fable model gating.** `claude-opus-4-8` and
  `claude-fable-5` (new `ClaudeOpus48` / `ClaudeFable5` constants) are
  recognized as post-4.7 flagships: adaptive-only thinking (manual
  budgets rejected with a pointer to `AdaptiveThinking`), and the full
  effort range including `xhigh` and `max` now passes
  per-model effort gating.

### Phase 14: Ten Innovations from Pydantic-AI, LangChain 1.0, OpenAI Agents SDK, AutoGen & CrewAI

#### Innovation 1: Typed Dependency Access
- `GetDeps[D]` extracts typed dependencies from `RunContext` without manual type assertions
- `TryGetDeps[D]` safe variant returning `(D, bool)`
- `WithDeps` agent option for setting dependencies at agent level
- Agent-level deps merge with run-level `WithRunDeps` (run-level takes precedence)

#### Innovation 2: Model Capability Profiles
- `ModelProfile` struct describes model capabilities (tool calls, vision, streaming, context window)
- `Profiled` optional interface for models to self-declare capabilities
- `GetProfile` returns profile or default (full capabilities) for non-Profiled models
- `NewCapabilityRouter` selects first model matching required capabilities

#### Innovation 3: Usage Quotas with Auto-Termination
- `UsageQuota` with hard limits on requests, total/input/output tokens
- `QuotaExceededError` returned when quota is breached
- `WithUsageQuota` agent option; checked before each model request
- Zero values mean unlimited (opt-in enforcement)

#### Innovation 4: Message Interceptor
- `MessageInterceptor` intercepts outgoing model requests (allow/drop/modify)
- `ResponseInterceptor` intercepts incoming model responses
- `RedactPII` built-in interceptor for regex-based PII redaction
- `AuditLog` built-in interceptor for message logging
- `WithMessageInterceptor` / `WithResponseInterceptor` agent options

#### Innovation 5: Tool Choice Control
- `ToolChoice` with modes: auto, required, none, force (specific tool)
- `ToolChoiceAuto`, `ToolChoiceRequired`, `ToolChoiceNone`, `ToolChoiceForce` constructors
- `WithToolChoice` agent option; `WithToolChoiceAutoReset` prevents infinite loops
- `ToolChoice` field added to `ModelSettings` for provider pass-through

#### Innovation 6: Cost Tracker
- `CostTracker` with per-model `ModelPricing` (input/output/cached token costs)
- `Record` accumulates costs; `TotalCost` and `CostBreakdown` for reporting
- `RunCost` on `RunResult` for per-run cost visibility
- `WithCostTracker` agent option; thread-safe for concurrent recording

#### Innovation 7: Composable Pipeline
- `Pipeline` chains `PipelineStep` functions sequentially
- `AgentStep` wraps `Agent[string]` as a pipeline step
- `TransformStep` for pure string transformations
- `ParallelSteps` runs steps concurrently and joins results
- `ConditionalStep` branches based on a predicate
- `Then` appends steps immutably (returns new pipeline)

#### Innovation 8: Auto Context Window Management
- `AutoContextConfig` with token threshold, keep-last-N, optional summary model
- `WithAutoContext` agent option for transparent overflow handling
- Simple word-based token estimation (no external deps)
- Summarizes old messages via model call when threshold exceeded

#### Innovation 9: Streaming Text Options
- `StreamText` with `StreamTextOptions` (delta mode, debounce window)
- `StreamTextDelta` for raw incremental chunks
- `StreamTextAccumulated` for growing accumulated text
- `StreamTextDebounced` for grouped event delivery
- Returns `iter.Seq2[string, error]` consistent with existing streaming API

#### Innovation 10: Agent Middleware
- `AgentMiddleware` wraps model calls with cross-cutting concerns
- `WithAgentMiddleware` agent option; middleware compose in order (first = outermost)
- `LoggingMiddleware` logs request/response summaries
- `MaxTokensMiddleware` enforces token limits via ModelSettings
- `TimingMiddleware` records request durations
- Middleware can modify inputs, outputs, or skip the model call entirely

### Phase 13: Ten Innovations from LangGraph, Pydantic-AI, OpenAI Agents SDK & AutoGen

#### Innovation 1: Rate Limiter Model Wrapper
- `RateLimitedModel` wraps any `Model` with token-bucket rate limiting
- `NewRateLimitedModel` with configurable requests-per-second and burst
- Requests exceeding rate are delayed, not rejected
- Context cancellation stops waiting requests

#### Innovation 2: Response Cache Model Wrapper
- `CacheStore` interface with `MemoryCache` implementation
- `NewMemoryCacheWithTTL` for TTL-based expiration
- `CachedModel` wraps `Model` to cache `Request()` responses by SHA-256 hash
- Streaming requests bypass cache

#### Innovation 3: Tool Timeout / Deadline
- `WithToolTimeout` per-tool execution deadline via `context.WithTimeout`
- `WithDefaultToolTimeout` agent-level default for tools without explicit timeout
- Per-tool timeout takes precedence over agent default

#### Innovation 4: Composable Run Conditions
- `RunCondition` predicate checked after each model response
- `Or()` and `And()` combinators for composing conditions
- Built-in conditions: `MaxRunDuration`, `TextContains`, `ToolCallCount`, `ResponseContains`
- `WithRunCondition` agent option
- `RunConditionError` error type when condition triggers

#### Innovation 5: Handoff Context Filters
- `HandoffFilter` transforms messages at agent handoff boundaries
- Built-in filters: `StripSystemPrompts`, `KeepLastN`, `SummarizeHistory`
- `ChainFilters` for composing multiple filters in sequence
- `ChainRunWithFilter` for filtered agent chaining
- `Handoff.AddStepWithFilter` for filtered handoff pipeline steps

#### Innovation 6: Trace Exporter Interface
- `TraceExporter` interface for pluggable trace export
- `JSONFileExporter` writes JSON trace files to a directory
- `ConsoleExporter` prints human-readable trace summaries
- `MultiExporter` fans out to multiple exporters
- `WithTraceExporter` agent option (implicitly enables tracing)

#### Innovation 7: Agent Test Override
- `Override()` creates independent agent with replaced model
- `WithTestModel()` convenience returns agent + `TestModel` pair
- Original agent is never modified

#### Innovation 8: Retry with Exponential Backoff
- `RetryModel` wraps `Model` with configurable retry for transient failures
- `RetryConfig` with `MaxRetries`, `InitialBackoff`, `MaxBackoff`, `BackoffFactor`, `Jitter`
- `DefaultRetryConfig` targets HTTP 429/500/502/503
- Both `Request` and `RequestStream` retry

#### Innovation 9: Conversation State Snapshot
- `RunSnapshot` captures full run state (messages, usage, step)
- `MarshalSnapshot` / `UnmarshalSnapshot` for JSON round-trip
- `Branch()` creates independent copies for alternate-path exploration
- `WithSnapshot` `RunOption` to resume from saved state
- Hook-friendly capture via `Snapshot(rc)`

#### Innovation 10: Typed Event Bus for Agent Coordination
- `EventBus` with typed `Subscribe` / `Publish` / `PublishAsync` using generics
- Type-safe: subscribers only receive matching event types
- `Unsubscribe` via returned function from `Subscribe`
- Built-in events: `RunStartedEvent`, `RunCompletedEvent`, `ToolCalledEvent`
- `WithEventBus` agent option; bus accessible via `RunContext.EventBus`
- Thread-safe under concurrent access

### Phase 12: Ten Innovations from Pydantic-AI & LangChain/LangGraph

#### Innovation 1: Agent Lifecycle Hooks
- `Hook` struct with 6 event callbacks: `OnRunStart`, `OnRunEnd`, `OnModelRequest`, `OnModelResponse`, `OnToolStart`, `OnToolEnd`
- `WithHooks` agent option for registering multiple hooks in order
- All hooks fire at correct points in the agent run loop

#### Innovation 2: Prompt Templates
- `PromptTemplate` with Go `text/template` syntax (`{{.VarName}}`)
- `NewPromptTemplate`, `MustTemplate`, `Format`, `Partial`, `Variables` API
- `WithSystemPromptTemplate` agent option for template-based system prompts
- `TemplateVars` interface for custom deps types

#### Innovation 3: Input Guardrails
- `InputGuardrailFunc` validates/transforms prompts before the agent loop
- `TurnGuardrailFunc` validates messages before each model request
- `GuardrailError` distinct error type with guardrail name
- Built-in guardrails: `MaxPromptLength`, `ContentFilter`, `MaxTurns`

#### Innovation 4: Batch Execution
- `RunBatch` executes multiple prompts concurrently with ordered results
- `WithBatchConcurrency` controls parallel execution limit
- Context cancellation aborts all in-flight runs

#### Innovation 5: Model Router
- `ModelRouter` interface and `RouterModel` implementing `Model`
- `ClassifierRouter` for function-based routing
- `ThresholdRouter` for prompt-length-based model selection
- `RoundRobinRouter` for even distribution across models

#### Innovation 6: Conversation Memory Strategies
- `SlidingWindowMemory` keeps last N message pairs
- `TokenBudgetMemory` drops oldest messages to fit token budget
- `SummaryMemory` uses a model to summarize older messages
- All implement `HistoryProcessor` for use with `WithHistoryProcessor`

#### Innovation 7: Output Auto-Repair
- `RepairFunc[T]` intercepts parse failures before retry flow
- `WithOutputRepair` agent option for custom repair logic
- `ModelRepair[T]` helper uses a model to fix malformed JSON
- Repaired output still runs through validators

#### Innovation 8: Agent Composition
- `Clone` creates independent agent copies with additional options
- `ChainRun` pipes agents: first output transforms to second prompt
- `ChainRunFull` returns both intermediate and final results with combined usage

#### Innovation 9: Structured Run Traces
- `RunTrace` captures all execution steps with timestamps and durations
- `TraceStep` with kinds: `model_request`, `model_response`, `tool_call`, `tool_result`
- `WithTracing` agent option; trace available on `RunResult.Trace`
- JSON-serializable for debugging, replay, and compliance auditing

#### Innovation 10: Tool Result Validators
- `ToolResultValidatorFunc` validates tool results before passing to model
- Per-tool validators via `WithToolResultValidator` tool option
- Agent-wide validators via `WithGlobalToolResultValidator`
- Invalid results become `RetryPromptPart` with validation error

### Phase 11: Ten Innovations

#### Innovation 1: KnowledgeBase Interface
- `KnowledgeBase` interface for pluggable RAG, graph databases, and memory services
- Agent transparently calls `Retrieve()` before each request and `Store()` after successful runs
- `StaticKnowledgeBase` for testing and simple use cases
- `WithKnowledgeBase` and `WithKnowledgeBaseAutoStore` agent options

#### Innovation 2: Message Serialization API
- `MarshalMessages` / `UnmarshalMessages` for JSON round-trip of conversations
- Envelope pattern with `kind` and `type` discriminators for all part types
- `RunResult.AllMessagesJSON()` and `NewMessagesJSON()` helpers

#### Innovation 3: Multimodal Message Parts
- `ImagePart`, `AudioPart`, `DocumentPart` types implementing `ModelRequestPart`
- `BinaryContent()` helper for base64 data: URI generation
- Full serialization support for multimodal parts

#### Innovation 4: Tool Prepare Functions
- Per-tool `PrepareFunc` for dynamic include/exclude/modify at each agent step
- Agent-wide `WithToolsPrepare` for bulk tool filtering
- Context-based tool availability (e.g., hide tools based on run state)

#### Innovation 5: Deferred Tool Calls
- `CallDeferred` error type for tools that pause the agent for external resolution
- `RunResultDeferred` / `ErrDeferred` for clean deferred signaling
- `WithDeferredResults` to resume runs with externally-resolved tool results
- Mixed deferred and normal tool calls in the same step

#### Innovation 6: Graph Fan-Out / Map-Reduce
- `FanOutNode` for parallel branch execution via goroutines
- `Send[S]` directives and `ReduceFunc` for state merging
- Error propagation from parallel branches
- Mermaid diagram support for fan-out nodes

#### Innovation 7: Checkpoint Replay, Fork, and Tool State
- `GetHistory()` for browsing checkpoint history
- `ReplayFrom()` to resume from any checkpoint step
- `ForkFrom()` to branch with modified state
- `StatefulTool` interface for tool state persistence across checkpoints
- `ExportToolStates` / `RestoreToolStates` for checkpoint-aware tools

#### Innovation 8: Persistent Memory Store
- `Store` interface with namespace-scoped CRUD and search
- `MemoryStore` (in-memory, thread-safe) implementation
- `SQLiteStore` (persistent, pure-Go via modernc.org/sqlite)
- `StoreKnowledgeBase` adapter bridging Store to KnowledgeBase interface
- `MemoryTool` for agent-accessible memory operations

#### Innovation 9: Step-by-Step Evaluation
- `StepEvaluator` interface for per-step scoring
- Built-in evaluators: `MaxStepsEvaluator`, `NoRetryEvaluator`
- Step scores in `CaseResult` and aggregated reports

#### Innovation 10: TUI Agent Debugger
- Terminal UI using bubbletea with color-coded message display
- Step mode (press 's') and auto mode (press 'a') for agent execution
- Tool call formatting, usage stats, and scroll navigation
- `cmd/gollem` CLI entry point for interactive debugging

### Phase 10: Innovations
- Provider fallback chains — FallbackModel tries multiple models in order until one succeeds
- Rate limiting middleware — token bucket rate limiter with configurable rps and burst
- Retry middleware with exponential backoff — configurable max retries, delay caps, RetryIf predicates
- Request/response caching middleware — SHA-256 hash-based cache with TTL expiration and stats
- Reflection/self-correction pattern — RunWithReflection loops output through a validator with configurable iterations

### Phase 9: Documentation, README & Examples
- Comprehensive README.md with quick start, architecture diagram, and feature documentation
- CONTRIBUTING.md with development setup, code style, and PR process
- CHANGELOG.md documenting all phases
- New examples: temporal, evaluation, multi-agent delegation, deep context management, graph workflows

### Phase 8: Extended MCP & Observability
- SSE transport for MCP servers
- Multi-server Manager with namespaced tool aggregation
- ToolSource interface for unified client usage
- OpenTelemetry tracing and metrics middleware
- Streaming middleware support

### Phase 7: Evaluation Framework
- Dataset and Case types for structured evaluation
- Built-in evaluators: ExactMatch, Contains, JSONMatch, Custom, LLMJudge
- Runner with multi-evaluator support
- Report aggregation with pass/fail scoring

### Phase 6: Multi-Agent Framework
- Agent delegation via AgentTool
- Sequential handoff pipelines
- Typed graph engine with conditional branching and cycle detection
- Mermaid diagram generation

### Phase 5: Temporal Durable Execution
- TemporalModel wrapping model requests as activities
- Tool call wrapping as activities
- TemporalAgent orchestrator
- Activity collection for worker registration

### Phase 4: Deep Package -- Planning & Checkpointing
- Planning tool for multi-step task coherence
- Checkpoint save/load/resume system
- Custom JSON serialization for ModelMessage interfaces
- LongRunAgent wrapper combining all deep features

### Phase 3: Deep Package -- Context Management
- Three-tier context compression (offload large results, offload inputs, LLM summarization)
- Token estimation utility
- Filesystem-backed context store
- ContextManager as HistoryProcessor

### Phase 2: Core Framework Enhancements
- Dynamic system prompts (WithDynamicSystemPrompt)
- History processors (WithHistoryProcessor)
- Human-in-the-loop tool approval (WithToolApproval)
- Node-by-node agent iteration (Agent.Iter)
- Concurrency and tool call limits
- Toolsets for grouped tool management

### Phase 1: Go Best Practices & Infrastructure
- Makefile with comprehensive targets
- golangci-lint v2 configuration
- GitHub Actions CI/CD workflows
- MIT License, .gitignore, codecov config
- Testable examples
