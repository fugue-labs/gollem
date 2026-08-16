# Provider Driver Conformance

This matrix is the authority for the common provider behavior that Slang may
present as runnable. A `proven` entry has deterministic fixture coverage through
the provider-neutral `core.Model` boundary. An entry marked `unsupported` must
remain unavailable in the catalog and renderer. `Not yet proven` is not a claim
of behavior and must not enable a control solely on provider identity.

## Common Driver Matrix

| Behavior | Native OpenAI | OpenAI-compatible local | Native Anthropic | Evidence |
| --- | --- | --- | --- | --- |
| Non-stream text response | Proven | Proven | Proven | `provider/conformance` |
| Function-tool request and normalized tool-call name, ID, and argument JSON | Proven | Proven | Proven | `provider/conformance` |
| Streaming text and terminal usage | Proven | Proven | Proven | `provider/conformance` |
| In-flight request cancellation | Proven | Proven | Proven | `provider/conformance` |
| Structured output | Proven native JSON-schema output | Unsupported | Proven schema-backed `final_result` tool output | `provider/conformance` runs native output mode through a typed agent and verifies the normalized result; fixtures assert OpenAI `response_format` and Anthropic's generated output tool schema |
| Vision image input | Proven | Unsupported | Proven | `provider/conformance` sends a deterministic PNG data URI through `core.ImagePart`, asserts each provider's native image wire format, and verifies the normalized response |
| Namespace tool grouping | Proven for catalog-listed GPT-5 Responses models | Unsupported | Unsupported | `provider/conformance` groups a namespaced function through the OpenAI Responses API and verifies both the native `namespace` object and normalized tool-call namespace metadata |
| Deferred tool search | Adapter proven for `gpt-5.4`, but not catalog-enabled | Unsupported | Proven for catalog-listed Sonnet and Opus models | `provider/conformance` sends a `DeferLoading` tool, verifies the normalized response, and fixtures assert OpenAI's `tool_search` or Anthropic's regex search primitive plus the deferred tool |
| Reasoning visibility | Proven where catalog-supported | Unsupported | Proven where catalog-supported | `provider/conformance` verifies native `ThinkingPart` start/delta events and final retention; local Chat Completions remains unsupported |
| Prompt-cache activation | Proven | Unsupported | Proven | `provider/conformance` sends `core.ModelSettings.PromptCacheEnabled=true` on normal and streaming requests and fixtures observe OpenAI cache metadata or Anthropic `cache_control` markers |
| Cache-read token accounting | Proven | Unsupported | Proven | `provider/conformance` verifies provider-reported cache reads normalize to `core.Usage.CacheReadTokens`; accounting and activation remain distinct evidence |
| Malformed JSON stream event normalization | Proven | Proven | Proven | `provider/conformance` plus provider parser tests; returns `StreamProtocolError` without raw event data |
| Abrupt EOF partial-stream result | Proven | Proven | Proven | `provider/conformance` plus provider parser tests; preserves partial response and returns `StreamIncompleteError` |
| Read-error peer-disconnect classification | Proven | Proven | Proven | `provider/conformance` plus provider parser tests; returns source-free `StreamTransportError`, while context cancellation remains intact |
| Retryable 429 request recovery | Proven | Proven | Proven | `provider/conformance` exercises bounded `modelutil.RetryModel` recovery |
| Request deadline propagation | Proven | Proven | Proven | `provider/conformance` confirms initial-request and post-header streaming cancellation with `context.DeadlineExceeded` normalization |
| Post-output retry / replay | Not yet proven | Not yet proven | Not yet proven | A stream may have produced caller-visible output; recovery must not replay it without an explicit safe-resume contract |
| Endpoint health probe | Unsupported | Proven | Unsupported | `provider/health/probe` performs a loopback-only `GET /v1/models`; it returns only a typed status and never starts a model turn |
| Capability mismatch | Catalog/daemon proven | Catalog/daemon proven | Catalog/daemon proven | `ValidateAgentRuntimeSelection` rejects unconfigured, unknown, cross-provider, and non-streaming/non-tool-capable selections before the daemon persists a thread or turn; model-specific manual/adaptive thinking selection is likewise rejected before persistence, and Slang continues to render the same condition as unavailable |

`core.ModelSettings.PromptCacheEnabled` is optional: `nil` preserves the
provider default, `true` requests its configured native cache control, and
`false` suppresses Gollem-managed explicit cache metadata. It does not create
cache entries, guarantee a cache hit, disable opaque provider-side automatic
caching, or authorize a local response replay.

## Vertex Catalog Profiles

The hidden Vertex profiles remain catalog-visible through the app-server API,
so their advertised capabilities require the same deterministic evidence as
the visible providers. Their package-local fixtures use static test tokens and
an in-process HTTP server; they do not create Google credentials or contact a
remote endpoint.

| Profile | Catalog-supported behavior | Evidence |
| --- | --- | --- |
| Vertex AI Gemini | Function tools, native structured output, inline image input, streaming, terminal usage, and configured cached-content attachment | `provider/vertexai` calls `provider/conformance.Verify`; the fixture asserts Gemini function declarations, `inlineData`, cached-content on normal and streaming requests, and normalized responses |
| Vertex AI Anthropic | Function tools, schema-backed `final_result` output, base64 image input, streaming, terminal usage, prompt-cache activation, and reasoning visibility | `provider/vertexai_anthropic` calls `provider/conformance.Verify`; the fixture asserts Messages-tool/cache/image wire forms plus normalized `ThinkingPart` start, delta, and final retention |

`Catalog-supported` means the catalog may expose the capability only for the
listed provider/model profile. It does not make that behavior part of the common
driver contract until a deterministic conformance scenario covers it.

## Custody And Local Endpoint Rules

- Credentials, base URLs, headers, raw payloads, and transport identity remain
  inside the Gollem process. Catalog entries expose only provider IDs, model IDs,
  capability descriptors, and configuration variable names.
- The local OpenAI-compatible profile accepts only loopback HTTP(S) endpoints,
  forces Chat Completions, and does not inherit OpenAI Responses, ChatGPT,
  prompt-cache, or remote transport settings.
- A local endpoint connection failure is a bounded `local endpoint unavailable`
  error. It must not reveal the endpoint or local token through app-server,
  catalog, or renderer diagnostics.
- `provider/health/probe` is available only for the explicitly configured local
  profile. It returns `available`, `unavailable`, `not-configured`, or
  `unsupported`; the probe reads `/v1/models` and does not invoke a model.

## Adding Or Expanding A Claim

1. Add a deterministic fixture scenario to `provider/conformance` for every
   claimed driver.
2. Assert the same normalized `core.Model` result and terminal behavior for all
   drivers that claim it.
3. Add provider-specific tests when a wire format needs richer assertions.
4. Update the catalog capability and Slang control gate only after the common
   scenario is green.
5. Keep unsupported and not-yet-proven behavior visible only as typed
   unavailable or degraded state; do not silently fall back to another provider
   surface.
