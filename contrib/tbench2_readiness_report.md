# Terminal-Bench 2.0 Readiness Report For Gollem Harness

Date: 2026-02-26

## Executive Summary

- Scope: all **89** tasks in Terminal-Bench 2.0 (official task repo commit `f5b891cb4f7c20e306f9d05887628b43af740f43`).
- Overall readiness score (mean, audited): **62.9/100**.
- Score range (audited): **27/100** to **90/100**.
- Highest-readiness cluster: git/software-engineering/debugging tasks.
- Lowest-readiness cluster: multimodal video/image, heavy ML/distributed training, R/Stan, and virtualization-heavy tasks.
- Competitive implication: harness fundamentals are stronger than initially scored; remaining competitiveness gap is concentrated in ML runtime profiles, multimodal parity/tooling, and task-specific resource orchestration.

## What Was Scored

- Source of truth for tasks: `laude-institute/terminal-bench-2` task folders (`task.toml` + `instruction.md`).
- Scoring unit: one readiness score per benchmark task (0-100).
- Readiness definition: expected probability-adjusted execution quality for this harness (not raw model IQ), given current tools, setup, middleware, and runtime defaults.

## Scoring Method

- Base score components:
  - Base = `50`
  - Harness implementation boost = `+22` after code audit (verification hard-blocks, context injection/test auto-read, progress/time-budget controls, context-overflow recovery, subagent delegation, Harbor prewarm, priority-tier wiring).
  - Difficulty adjustment: easy `+8`, medium `+0`, hard `-10`.
  - Category adjustments by benchmark domain (e.g., software-engineering positive, model-training/video-processing negative).
- Capability flags from tags/instructions/task IDs then apply impacts, e.g.:
  - Multimodal/video/image/OCR, QEMU/Windows VM, ML-heavy/distributed, R/Stan, reverse engineering, crypto/math, large-data, strict performance targets.
- Final score clipped to `[5,95]` to avoid false precision at extremes.

### Difficulty Breakdown

| Difficulty | Task Count | Mean Score |
|---|---:|---:|
| easy | 4 | 79.5 |
| medium | 55 | 67.7 |
| hard | 30 | 52.0 |

### Category Breakdown

| Category | Task Count | Mean Score |
|---|---:|---:|
| debugging | 5 | 74.4 |
| personal-assistant | 1 | 73.0 |
| data-processing | 4 | 68.5 |
| software-engineering | 26 | 67.7 |
| optimization | 1 | 67.0 |
| security | 8 | 66.1 |
| file-operations | 5 | 65.8 |
| system-administration | 9 | 64.8 |
| games | 1 | 61.0 |
| scientific-computing | 8 | 59.6 |
| data-querying | 1 | 59.0 |
| data-science | 8 | 56.5 |
| mathematics | 4 | 50.2 |
| model-training | 4 | 49.0 |
| machine-learning | 3 | 46.3 |
| video-processing | 1 | 27.0 |

## Framework Capability Audit (Code-Evidenced)

- `cmd/gollem/main.go:423-434,642-711`: prompt-image detection + attachment to initial request for OpenAI runs; provider-gated fallback messaging for non-OpenAI.
- `ext/codetool/verification.go:17-30,245-278`: hard completion blockers (must verify, no post-verify edits, last verify cannot fail).
- `ext/codetool/middleware.go:369-425,671-713,1502-1579`: environment + tests + constraints + action-summary context injection on first turn.
- `ext/codetool/middleware.go:8245-8412`: progress-tracking middleware with time/turn-triggered anti-stall nudges.
- `ext/codetool/reasoning.go:199-231,316-408`: time-budget middleware with staged urgency + greedy token/reasoning caps near deadline.
- `ext/codetool/middleware.go:8661-8724`: automatic 413/overflow recovery via progressive emergency context compression + retry.
- `ext/codetool/subagent.go:26-120` and `ext/codetool/toolset.go:105-128`: always-available `delegate` subagent tool plus optional team orchestration.
- `ext/codetool/bash.go:122-132,156-217,252-330`: resilient bash execution (adaptive timeout floors, transient auto-retry, high-signal failure hints).
- `harbor/gollem_harbor/agent.py:340-546,651-700`: setup-time package/tool prewarm + env defaults (`GOLLEM_TEAM_MODE=off`, `GOLLEM_DISABLE_RUNTIME_DEP_INSTALL=1`, OpenAI service tier priority).
- `harbor/run-eval.sh:75-79`, `harbor/run-official-leaderboard.sh:66-76`, `provider/openai/openai.go:106-144,188-190`: priority processing and prompt-cache settings are wired end-to-end for OpenAI eval paths.
- `ext/codetool/middleware.go:23-140`: loop-detection middleware catches repeated edit/read/search/bash patterns and injects recovery guidance.
- `ext/codetool/toolset.go:77-94`, `cmd/gollem/main.go:277-287`: code mode (`execute_code`) and persistent planning tool are integrated into standard tool stacks.
- `ext/codetool/bash.go:111-119`: destructive test-file mutation attempts via bash are blocked to preserve verifier integrity.
- `ext/codetool/middleware.go:1437-1543`: expected-output, invocation-pattern, env-var, timeout, and diff-target extraction from tests is implemented and auto-injected.

## Audit Corrections vs Prior Findings

- Multimodal capability is not absent: image attachment exists for OpenAI path today; gap is provider parity and explicit OCR/video tools.
- Runtime dependency support is stronger than initially scored: large auto-install surface exists in middleware, but Harbor defaults disable runtime installs (`GOLLEM_DISABLE_RUNTIME_DEP_INSTALL=1`) to conserve model budget.
- Team orchestration is stronger than initially scored: LLM-routed team-mode selection and enforced early delegation exist, but Harbor eval scripts default to `GOLLEM_TEAM_MODE=off`.
- Timeout/performance readiness is stronger than initially scored: per-test timeout extraction, time-budget urgency, and timeout-aware bash hints are already implemented.
- Verifier-integrity safeguards are stronger than initially scored: test-file destructive bash commands are blocked and completion is verification-gated.
- Large-task resilience is stronger than initially scored: context overflow recovery plus content truncation are integrated for both main agent and subagents.

## Capability Status Matrix

| Capability | Status | Evidence | Remaining Gap |
|---|---|---|---|
| Verification-gated completion | Implemented | `ext/codetool/verification.go:17-30,245-278` | None for baseline coding tasks. |
| Environment + test-aware context bootstrap | Implemented | `ext/codetool/middleware.go:369-425,671-713,1437-1543` | Improve precision for highly bespoke verifier logic. |
| Time/progress anti-timeout controls | Implemented | `ext/codetool/middleware.go:8245-8412`, `ext/codetool/reasoning.go:316-408` | Add automated profile-optimize loops. |
| Context overflow recovery | Implemented | `ext/codetool/middleware.go:8661-8724` | Tune compression quality for long multistep tasks. |
| Subagent delegation/team coordination | Implemented | `ext/codetool/subagent.go:26-120`, `ext/codetool/toolset.go:105-128` | Harbor default `team=off` for throughput; add task-level routing. |
| Code mode + planning | Implemented | `cmd/gollem/main.go:277-287`, `ext/codetool/toolset.go:92-94` | Improve fallback behavior on code-mode unavailability. |
| Test-file integrity protection | Implemented | `ext/codetool/bash.go:111-119` | Extend guardrails to more indirect mutation vectors. |
| OpenAI priority + prompt cache | Implemented | `provider/openai/openai.go:106-144,188-190`, `harbor/run-eval.sh:75-79` | Add parity controls/verification for non-OpenAI providers where possible. |
| Multimodal image input | Partial | `cmd/gollem/main.go:423-434,642-711` | Provider parity, OCR primitives, and image-tool feedback loop. |
| Video understanding pipeline | Missing | (no first-class video tool in toolset) | Add ffmpeg/frame/audio extraction + summarization tool wrappers. |
| ML runtime profile routing | Partial | Harbor prewarm in `harbor/gollem_harbor/agent.py:340-546` | Add GPU-aware runner profiles and task-routed environment selection. |
| VM/QEMU orchestration helpers | Partial | guidance-only in `ext/codetool/middleware.go:2485-2494` | Add boot/readiness probes and SSH orchestration helpers. |

## Execution Profile Reality (Official Harbor Defaults)

| Area | Core Framework Capability | Official Harbor Default | Practical Effect On TB2 |
|---|---|---|---|
| Team coordination | Implemented (`cmd/gollem/main.go:837-888`, `ext/codetool/middleware.go:8488-8557`) | `GOLLEM_TEAM_MODE=off` in run scripts (`harbor/run-eval.sh:35-38`, `harbor/run-official-leaderboard.sh:58-69`) | Throughput-friendly, but leaves some complex tasks under-delegated unless overridden. |
| Runtime dependency installs | Broad auto-install logic in middleware (`ext/codetool/middleware.go:803-1277`) | `GOLLEM_DISABLE_RUNTIME_DEP_INSTALL=1` (`harbor/gollem_harbor/agent.py:665-667`, `harbor/run-official-leaderboard.sh:59-70`) | Fewer wasted turns on installs; higher risk on unusual missing deps not covered in setup prewarm. |
| LSP availability | LSP-aware context + tool integration (`ext/codetool/middleware.go:460-507`) | `GOLLEM_SETUP_INSTALL_LSP=1` in official script (`harbor/run-official-leaderboard.sh:61-72`) | Better semantic navigation on supported languages. |
| Timeout awareness | task.toml timeout parsing in Harbor + gollem + middleware | Enabled by default | Stronger deadline-aware behavior and warning timing. |
| Priority processing | OpenAI provider supports `service_tier` (`provider/openai/openai.go:106-144,188-190`) | `OPENAI_SERVICE_TIER=priority` (`harbor/run-official-leaderboard.sh:66-76`) | Lower latency for OpenAI eval runs; non-OpenAI runs do not use this path. |

## Provider Feature Matrix (TB2-Relevant)

| Feature | OpenAI Provider | Anthropic Provider | VertexAI Provider | VertexAI-Anthropic Provider |
|---|---|---|---|---|
| Request `ImagePart` support | Yes (`provider/openai/message.go:330-338`) | No (`provider/anthropic/message.go:203`) | No (`provider/vertexai/message.go:254`) | No (`provider/vertexai_anthropic/message.go:202`) |
| Priority tier control | Yes (`provider/openai/openai.go:106-144,188-190`) | N/A | N/A | N/A |
| Prompt cache integration | Yes (`OPENAI_PROMPT_CACHE_*`) | Not wired in provider | Not wired in provider | Yes (`cache_control` ephemeral via `VERTEXAI_ANTHROPIC_PROMPT_CACHE*`) |

Implication: image-heavy TB2 tasks currently favor OpenAI runs because non-OpenAI providers reject `ImagePart` in this framework path.

## Competitive Snapshot (Public Leaderboard Research)

- Leaderboard entries parsed: **111** (snapshot date: 2026-02-26).
- Top overall entries at snapshot:

| Rank | Agent | Model | Accuracy | Date | Verified |
|---:|---|---|---:|---|---|
| 1 | Droid | GPT-5.3-Codex | 0.7730 | 2026-02-24 | no |
| 2 | Simple Codex | GPT-5.3-Codex | 0.7506 | 2026-02-06 | yes |
| 3 | Terminus-KIRA | Gemini 3.1 Pro | 0.7483 | 2026-02-23 | no |
| 4 | Terminus-KIRA | Claude Opus 4.6 | 0.7472 | 2026-02-22 | no |
| 5 | Judy | Claude Opus 4.6 | 0.7191 | 2026-02-22 | no |
| 6 | CodeBrain-1 | GPT-5.3-Codex | 0.7034 | 2026-02-10 | no |
| 7 | Droid | Claude Opus 4.6 | 0.6989 | 2026-02-05 | no |
| 8 | Mux | GPT-5.3-Codex | 0.6854 | 2026-02-09 | no |
| 9 | Crux | Claude Opus 4.6 | 0.6687 | 2026-02-23 | no |
| 10 | Deep Agents | GPT-5.2-Codex | 0.6652 | 2026-02-12 | no |

- Top verified entries at snapshot:

| Rank* | Agent | Model | Accuracy | Date |
|---:|---|---|---:|---|
| 1 | Simple Codex | GPT-5.3-Codex | 0.7506 | 2026-02-06 |
| 2 | Terminus 2 | GPT-5.3-Codex | 0.6472 | 2026-02-05 |
| 3 | Ante | Gemini 3 Pro | 0.6472 | 2026-01-06 |
| 4 | Terminus 2 | Claude Opus 4.6 | 0.6292 | 2026-02-06 |
| 5 | Codex CLI | GPT-5.2 | 0.6292 | 2025-12-18 |
| 6 | Codex CLI | GPT-5.1-Codex-Max | 0.6045 | 2025-11-24 |
| 7 | Claude Code | Claude Opus 4.6 | 0.5798 | 2026-02-07 |
| 8 | Terminus 2 | Claude Opus 4.5 | 0.5775 | 2025-11-22 |

*Rank within verified-only subset, not absolute leaderboard rank.

- Comparable public agent baselines (best listed run per agent):

| Agent | Best Accuracy | Model | Date | Verified |
|---|---:|---|---|---|
| Deep Agents | 0.6652 | GPT-5.2-Codex | 2026-02-12 | no |
| Terminus 2 | 0.6472 | GPT-5.3-Codex | 2026-02-05 | yes |
| Codex CLI | 0.6292 | GPT-5.2 | 2025-12-18 | yes |
| Claude Code | 0.5798 | Claude Opus 4.6 | 2026-02-07 | yes |
| OpenHands | 0.5191 | Claude Opus 4.5 | 2026-01-04 | yes |
| Mini-SWE-Agent | 0.4253 | Claude Sonnet 4.5 | 2025-11-03 | yes |
| Gemini CLI | 0.5101 | Gemini 3 Flash | 2025-12-23 | no |
| Simple Codex | 0.7506 | GPT-5.3-Codex | 2026-02-06 | yes |

## Framework Gaps To Close (Prioritized)

| Priority | Capability Gap | Affected Tasks | Estimated Score Lift Potential* | What To Add |
|---:|---|---:|---:|---|
| 1 | ML runtime profiles (GPU + model cache strategy) | 11 | +72 | Add task-routed runner profiles with CUDA/PyTorch/HF prewarm and explicit CPU fallback policy. |
| 2 | Multimodal parity + OCR/video tooling | 8 | +64 | Keep OpenAI image attach path, add provider-parity multimodal, and ship first-class OCR/frame/audio extraction tools. |
| 3 | TB2 task resource declarations + adaptive runner selection | 89 | +45 | Parse task metadata into runner policy (`cpu`, `highmem`, `gpu`, `vm`) and auto-select Harbor environment profile. |
| 4 | Niche toolchain reliability (R/Stan, Coq, OCaml, Cobol) | 9 | +45 | Add prevalidated toolchain packs with smoke tests during setup. |
| 5 | Large-data streaming/chunk-safe transforms | 13 | +39 | Add native chunked file/map-reduce helpers and streaming-safe patch/edit operations. |
| 6 | Auto profile-optimize-verify loops | 11 | +33 | Add middleware that detects perf-limited tests and runs scripted profiling/optimization passes. |
| 7 | Reverse-engineering + forensics pack | 8 | +32 | Bundle deterministic helpers for decompile, ELF triage, sqlite salvage, and artifact recovery. |
| 8 | QEMU/VM orchestration helpers | 3 | +24 | Add boot/readiness probes, SSH waiters, and reusable VM launch macros. |
| 9 | Crypto/math attack templates | 3 | +21 | Add reusable notebooks/scripts for linear/differential attacks and symbolic solving workflows. |
| 10 | Bioinformatics helper toolkit | 3 | +18 | Add FASTA/plasmid/primer validation and assembly helper scripts. |

*Lift potential is a directional estimate from residual rubric penalties after this capability audit; not guaranteed benchmark-point gain.

## Recommended Implementation Plan

1. Implement TB2 task resource declarations and route each task to the right Harbor runner profile (`cpu`, `highmem`, `gpu`, `vm`).
2. Add ML-specific runtime profiles: CUDA-aware images, torch/transformers cache warmup, and deterministic fallback behavior when GPU is absent.
3. Expand multimodal support beyond OpenAI and add explicit OCR/video tool wrappers (frame extraction, audio extraction, OCR, board-state extraction).
4. Add first-class large-data streaming helpers for chunked processing and chunk-safe edits.
5. Add a perf middleware loop that auto-runs profile -> optimize -> verify for timeout-constrained tasks.
6. Add VM/QEMU helper primitives (boot detection, port probes, SSH readiness, retries).
7. Add curated niche-language setup packs (R+Stan, Coq, OCaml, Cobol) with setup smoke tests.
8. Keep OpenAI priority defaults enabled and enforce with preflight assertions in all eval paths.


## Harness Strengths Already Present

- Hard verification gate with completion blockers and regression/stagnation guidance.
- High-signal context injection (README/tests/constraints/task hints/action summary) on first turn.
- Strong anti-timeout stack: progress tracking, time-budget urgency, and overflow-compression retries.
- Delegation stack is implemented (`delegate`, optional team mode), with subagent-specific safeguards.
- Harbor setup prewarms broad dependencies and passes task timeout/priority-tier/runtime flags into runs.
- OpenAI priority processing is wired end-to-end via provider + Harbor scripts (`OPENAI_SERVICE_TIER=priority`).


## Full Task-by-Task Readiness Scores

| Task | Category | Difficulty | Readiness Score | Main Risk Drivers | Recommended Additions |
|---|---|---|---:|---|---|
| `adaptive-rejection-sampler` | scientific-computing | medium | 69 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `bn-fit-modify` | scientific-computing | hard | 47 | reverse engineering, large data volume | Integrate disassembler/decompiler wrappers (rizin/radare2/Ghidra headless).; Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `break-filter-js-from-html` | security | medium | 69 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `build-cython-ext` | debugging | medium | 79 | heavy compilation | Add adaptive build orchestration (auto-timeout ramp + ccache-like warm cache). |
| `build-pmars` | software-engineering | medium | 74 | heavy compilation | Add adaptive build orchestration (auto-timeout ramp + ccache-like warm cache). |
| `build-pov-ray` | software-engineering | medium | 72 | heavy compilation, network/download dependency | Add adaptive build orchestration (auto-timeout ramp + ccache-like warm cache).; Add resilient fetch utility with retry/backoff/mirror + checksum support. |
| `caffe-cifar-10` | machine-learning | medium | 52 | ML-heavy workload, network/download dependency | Introduce ML-capable runner profiles (GPU-capable envs + model/cache warmup).; Add resilient fetch utility with retry/backoff/mirror + checksum support. |
| `cancel-async-tasks` | software-engineering | hard | 66 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `chess-best-move` | games | medium | 61 | image understanding | Add a vision tool (image read/OCR/board-state extraction) callable from the agent loop. |
| `circuit-fibsqrt` | software-engineering | hard | 66 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `cobol-modernization` | software-engineering | easy | 78 | rare toolchain | Preinstall/verify niche compilers and language servers in setup. |
| `code-from-image` | software-engineering | medium | 58 | image understanding, OCR/doc parsing | Add a vision tool (image read/OCR/board-state extraction) callable from the agent loop.; Add PDF/JPG OCR + document classification primitives with confidence scoring. |
| `compile-compcert` | system-administration | medium | 63 | heavy compilation, rare toolchain | Add adaptive build orchestration (auto-timeout ramp + ccache-like warm cache).; Preinstall/verify niche compilers and language servers in setup. |
| `configure-git-webserver` | system-administration | hard | 67 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `constraints-scheduling` | personal-assistant | medium | 73 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `count-dataset-tokens` | model-training | medium | 51 | ML-heavy workload, large data volume | Introduce ML-capable runner profiles (GPU-capable envs + model/cache warmup).; Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `crack-7z-hash` | security | medium | 69 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `custom-memory-heap-crash` | debugging | medium | 70 | heavy compilation, strict perf target | Add adaptive build orchestration (auto-timeout ramp + ccache-like warm cache).; Add automatic profile->optimize->retest loop with budget-aware cutoffs. |
| `db-wal-recovery` | file-operations | medium | 69 | forensics/recovery | Preinstall forensics stack (sqlite salvage, file-carving, metadata recovery). |
| `distribution-search` | machine-learning | medium | 50 | ML-heavy workload, large data volume | Introduce ML-capable runner profiles (GPU-capable envs + model/cache warmup).; Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `dna-assembly` | scientific-computing | hard | 49 | biology/scientific domain | Add FASTA/plasmid/primer helper tools and validation scripts. |
| `dna-insert` | scientific-computing | medium | 59 | biology/scientific domain | Add FASTA/plasmid/primer helper tools and validation scripts. |
| `extract-elf` | file-operations | medium | 66 | reverse engineering | Integrate disassembler/decompiler wrappers (rizin/radare2/Ghidra headless). |
| `extract-moves-from-video` | file-operations | hard | 42 | video understanding, network/download dependency | Add frame/audio extraction + VLM summarization tools for end-to-end video tasks.; Add resilient fetch utility with retry/backoff/mirror + checksum support. |
| `feal-differential-cryptanalysis` | mathematics | hard | 46 | crypto/math attack | Add crypto-analysis notebooks/templates and symbolic math helpers. |
| `feal-linear-cryptanalysis` | mathematics | hard | 46 | crypto/math attack | Add crypto-analysis notebooks/templates and symbolic math helpers. |
| `filter-js-from-html` | security | medium | 69 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `financial-document-processor` | data-processing | medium | 54 | image understanding, OCR/doc parsing | Add a vision tool (image read/OCR/board-state extraction) callable from the agent loop.; Add PDF/JPG OCR + document classification primitives with confidence scoring. |
| `fix-code-vulnerability` | security | hard | 59 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `fix-git` | software-engineering | easy | 90 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `fix-ocaml-gc` | software-engineering | hard | 55 | heavy compilation, strict perf target, rare toolchain | Add adaptive build orchestration (auto-timeout ramp + ccache-like warm cache).; Add automatic profile->optimize->retest loop with budget-aware cutoffs. |
| `gcode-to-text` | file-operations | medium | 74 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `git-leak-recovery` | software-engineering | medium | 77 | forensics/recovery | Preinstall forensics stack (sqlite salvage, file-carving, metadata recovery). |
| `git-multibranch` | system-administration | medium | 77 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `gpt2-codegolf` | software-engineering | hard | 62 | large data volume | Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `headless-terminal` | software-engineering | medium | 80 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `hf-model-inference` | data-science | medium | 58 | ML-heavy workload, network/download dependency | Introduce ML-capable runner profiles (GPU-capable envs + model/cache warmup).; Add resilient fetch utility with retry/backoff/mirror + checksum support. |
| `install-windows-3.11` | system-administration | hard | 37 | virtualization, windows legacy VM | Add first-class VM helpers (boot detection, port/protocol probes, readiness checks).; Prebake Windows/QEMU runbooks and verification macros for retro OS tasks. |
| `kv-store-grpc` | software-engineering | medium | 76 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `large-scale-text-editing` | file-operations | medium | 78 | large data volume | Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `largest-eigenval` | mathematics | medium | 63 | strict perf target | Add automatic profile->optimize->retest loop with budget-aware cutoffs. |
| `llm-inference-batching-scheduler` | machine-learning | hard | 37 | ML-heavy workload, large data volume, strict perf target | Introduce ML-capable runner profiles (GPU-capable envs + model/cache warmup).; Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `log-summary-date-ranges` | data-processing | medium | 76 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `mailman` | system-administration | medium | 71 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `make-doom-for-mips` | software-engineering | hard | 66 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `make-mips-interpreter` | software-engineering | hard | 66 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `mcmc-sampling-stan` | data-science | hard | 38 | R/Stan workload, large data volume, rare toolchain | Provide prevalidated R+Stan environments and troubleshooting macros.; Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `merge-diff-arc-agi-task` | debugging | medium | 81 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `model-extraction-relu-logits` | mathematics | hard | 46 | crypto/math attack | Add crypto-analysis notebooks/templates and symbolic math helpers. |
| `modernize-scientific-stack` | scientific-computing | medium | 69 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `mteb-leaderboard` | data-science | medium | 70 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `mteb-retrieve` | data-science | medium | 70 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `multi-source-data-merger` | data-processing | medium | 68 | large data volume | Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `nginx-request-logging` | system-administration | medium | 71 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `openssl-selfsigned-cert` | security | medium | 69 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `overfull-hbox` | debugging | easy | 72 | heavy compilation, strict perf target, rare toolchain | Add adaptive build orchestration (auto-timeout ramp + ccache-like warm cache).; Add automatic profile->optimize->retest loop with budget-aware cutoffs. |
| `password-recovery` | security | hard | 54 | forensics/recovery | Preinstall forensics stack (sqlite salvage, file-carving, metadata recovery). |
| `path-tracing` | software-engineering | hard | 56 | image understanding | Add a vision tool (image read/OCR/board-state extraction) callable from the agent loop. |
| `path-tracing-reverse` | software-engineering | hard | 48 | image understanding, reverse engineering | Add a vision tool (image read/OCR/board-state extraction) callable from the agent loop.; Integrate disassembler/decompiler wrappers (rizin/radare2/Ghidra headless). |
| `polyglot-c-py` | software-engineering | medium | 76 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `polyglot-rust-c` | software-engineering | hard | 58 | weak oracle | Add fallback policy: multiple candidate generation + differential verification. |
| `portfolio-optimization` | optimization | medium | 67 | strict perf target | Add automatic profile->optimize->retest loop with budget-aware cutoffs. |
| `protein-assembly` | scientific-computing | hard | 49 | biology/scientific domain | Add FASTA/plasmid/primer helper tools and validation scripts. |
| `prove-plus-comm` | software-engineering | easy | 78 | rare toolchain | Preinstall/verify niche compilers and language servers in setup. |
| `pypi-server` | software-engineering | medium | 76 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `pytorch-model-cli` | model-training | medium | 61 | ML-heavy workload | Introduce ML-capable runner profiles (GPU-capable envs + model/cache warmup). |
| `pytorch-model-recovery` | model-training | medium | 46 | ML-heavy workload, forensics/recovery, large data volume | Introduce ML-capable runner profiles (GPU-capable envs + model/cache warmup).; Preinstall forensics stack (sqlite salvage, file-carving, metadata recovery). |
| `qemu-alpine-ssh` | system-administration | medium | 65 | virtualization | Add first-class VM helpers (boot detection, port/protocol probes, readiness checks). |
| `qemu-startup` | system-administration | medium | 61 | virtualization | Add first-class VM helpers (boot detection, port/protocol probes, readiness checks). |
| `query-optimize` | data-science | medium | 67 | strict perf target | Add automatic profile->optimize->retest loop with budget-aware cutoffs. |
| `raman-fitting` | scientific-computing | medium | 69 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `regex-chess` | software-engineering | hard | 66 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `regex-log` | data-processing | medium | 76 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `reshard-c4-data` | data-science | medium | 66 | large data volume | Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `rstan-to-pystan` | data-science | medium | 43 | R/Stan workload, large data volume, strict perf target | Provide prevalidated R+Stan environments and troubleshooting macros.; Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `sam-cell-seg` | data-science | hard | 40 | image understanding, ML-heavy workload | Add a vision tool (image read/OCR/board-state extraction) callable from the agent loop.; Introduce ML-capable runner profiles (GPU-capable envs + model/cache warmup). |
| `sanitize-git-repo` | security | medium | 71 | large data volume | Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `schemelike-metacircular-eval` | software-engineering | medium | 70 | rare toolchain | Preinstall/verify niche compilers and language servers in setup. |
| `sparql-university` | data-querying | hard | 59 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `sqlite-db-truncate` | debugging | medium | 70 | forensics/recovery | Preinstall forensics stack (sqlite salvage, file-carving, metadata recovery). |
| `sqlite-with-gcov` | system-administration | medium | 71 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `torch-pipeline-parallelism` | software-engineering | hard | 52 | distributed ML | Ship reusable tensor/pipeline parallel code templates and self-check harnesses. |
| `torch-tensor-parallelism` | software-engineering | hard | 52 | distributed ML | Ship reusable tensor/pipeline parallel code templates and self-check harnesses. |
| `train-fasttext` | model-training | hard | 38 | ML-heavy workload, large data volume, strict perf target | Introduce ML-capable runner profiles (GPU-capable envs + model/cache warmup).; Add streaming transforms and chunk-safe edit/search operations for huge files. |
| `tune-mjcf` | scientific-computing | medium | 66 | strict perf target | Add automatic profile->optimize->retest loop with budget-aware cutoffs. |
| `video-processing` | video-processing | hard | 27 | video understanding, strict perf target | Add frame/audio extraction + VLM summarization tools for end-to-end video tasks.; Add automatic profile->optimize->retest loop with budget-aware cutoffs. |
| `vulnerable-secret` | security | medium | 69 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `winning-avg-corewars` | software-engineering | medium | 76 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |
| `write-compressor` | software-engineering | hard | 66 | none prominent | Keep current harness defaults; focus on model quality and iterative benchmark tuning. |

## Sources

- Terminal-Bench 2.0 task repository (authoritative tasks and metadata): https://github.com/laude-institute/terminal-bench-2
- Terminal-Bench 2.0 registry page: https://www.tbench.ai/registry/terminal-bench/2.0
- Terminal-Bench 2.0 leaderboard page: https://www.tbench.ai/leaderboard/terminal-bench/2.0
- Harbor installed agent wrappers (competitive integration patterns):
  - Codex wrapper: https://github.com/laude-institute/harbor/blob/main/src/harbor/agents/installed/codex.py
  - Claude Code wrapper: https://github.com/laude-institute/harbor/blob/main/src/harbor/agents/installed/claude_code.py
  - Gemini CLI wrapper: https://github.com/laude-institute/harbor/blob/main/src/harbor/agents/installed/gemini_cli.py
  - OpenHands wrapper: https://github.com/laude-institute/harbor/blob/main/src/harbor/agents/installed/openhands.py
- Deep Agents Harbor guide (public harness patterns): https://github.com/langchain-ai/deepagents/blob/main/libs/harbor/README.md
- LangChain public harness writeup (self-verification and harness gains): https://blog.langchain.com/how-to-build-great-agent-harnesses
- OpenAI priority processing docs: https://developers.openai.com/api/docs/guides/priority-processing
