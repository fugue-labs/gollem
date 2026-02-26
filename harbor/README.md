# gollem-harbor

Harbor adapter for running the [gollem](https://github.com/fugue-labs/gollem) coding agent on [Terminal-Bench 2.0](https://www.tbench.ai/).

## Quick Start

```bash
# Install dependencies
cd harbor
uv pip install -e .

# Run on Terminal-Bench 2.0
harbor run \
  -d terminal-bench@2.0 \
  --agent-import-path gollem_harbor:GollemAgent \
  -m anthropic/claude-sonnet-4-5 \
  --env docker \
  -n 4

# With extended thinking
harbor run \
  -d terminal-bench@2.0 \
  --agent-import-path gollem_harbor:GollemAgent \
  -m anthropic/claude-sonnet-4-5 \
  --env docker
```

## How It Works

1. Harbor creates a Docker container for each benchmark task
2. The `install.sh.j2` template installs Go and builds the `gollem` binary inside the container
3. `gollem run` executes with the task instruction, using the full coding agent toolset (bash, view, edit, grep, glob, ls, write)
4. The verification checkpoint forces the agent to run tests/builds before declaring completion
5. Results are collected and scored by Harbor

## TB2 Resource Declarations and Host Sizing

Terminal-Bench 2.0 defines per-task limits in each task's `task.toml`:

- `[environment]`:
  - `cpus`
  - `memory`
  - `storage`
  - `build_timeout_sec`
- `[agent]`:
  - `timeout_sec`
- `[verifier]`:
  - `timeout_sec`

For the current TB2 task set (89 tasks), the maximum declared task footprint is about:

- `4` vCPU
- `8G` memory
- `10G` storage

Recommended host sizing for `harbor run`:

| Concurrency (`-n`) | vCPU | RAM | NVMe Disk |
|---:|---:|---:|---:|
| 1 | 8 | 32 GB | 250 GB |
| 2 | 16 | 64 GB | 500 GB |
| 4 | 32 | 128 GB | 1 TB |

Rule of thumb for planning:

- vCPU: about `5 * n`
- RAM: about `10 * n` GB
- Disk working set: about `15 * n` GB, plus extra for Docker/build caches and logs

Leaderboard-oriented runs should use Linux `x86_64` hosts and should not override task timeouts or resources.

## Supported Providers

- `anthropic/claude-*` — Anthropic models
- `openai/gpt-*` — OpenAI models
- `google/gemini-*` — Google Vertex AI models

## Environment Variables

Set your API key before running:

```bash
export ANTHROPIC_API_KEY="sk-..."
# or
export OPENAI_API_KEY="sk-..."
# Optional: improve OpenAI prompt cache hit rate/retention
export OPENAI_PROMPT_CACHE_KEY="tbench2-gollem"
export OPENAI_PROMPT_CACHE_RETENTION="in_memory"
# Optional: request fastest OpenAI processing tier
export OPENAI_SERVICE_TIER="priority"
# Optional: use Responses API websocket mode for tool-heavy loops
export OPENAI_TRANSPORT="websocket"
# Optional: strict WS mode (default), do not silently fall back to HTTP
export OPENAI_WEBSOCKET_HTTP_FALLBACK="0"
```

## OpenAI WebSocket Mode Notes

When `OPENAI_TRANSPORT=websocket` is enabled:

- It applies only to Responses API models (for example Codex-style models).
- It optimizes multi-turn tool loops in non-streaming `Request()` flows.
- It does not provide token-by-token stream output for `RequestStream()`.
- A single provider session uses one in-flight request at a time.
- Continuation state is per-session and in-memory; if history is rewritten,
  gollem falls back to full-context sends safely.
- WebSocket mode uses `store=false` when unset; on HTTP fallback, gollem
  restores the original `store` intent for the HTTP request.

For Terminal-Bench style coding loops, this mode is usually beneficial.
