# Gollem Self-Improvement Harness Plan (Terminal-Bench 2.0)

## Objective
Build a closed-loop system that continuously improves Gollem’s Terminal-Bench (TB2) score while preserving rule compliance, reproducibility, and operational cost control.

## Scope
- In scope: automated run orchestration, telemetry capture, failure triage, candidate patch generation, gated validation, promotion strategy, and leaderboard-submission readiness checks.
- Out of scope: bypassing TB2 rules, task-specific hardcoding, or non-reproducible manual interventions.

## Success Criteria
- Increase pass-rate and/or mean reward on internal TB2 eval set over rolling 7-day windows.
- Reduce `AgentTimeoutError` rate and mean wall-clock per successful trial.
- Maintain submission readiness with clean artifacts and documented run metadata.
- Produce promotion-ready candidate branches with auditable evidence.

## Non-Negotiable Constraints
- Rule compliance first (official TB2 constraints, no cheating shortcuts).
- Deterministic eval config snapshots per experiment.
- Strict separation between experimental and official submission runs.
- No automatic merge to `main` without gate checks.

## Architecture
1. Orchestrator
- Schedules baseline/candidate runs.
- Controls concurrency, budget caps, provider routing, and retry policy.

2. Observer
- Collects per-trial signals: pass/fail, reward, exception class, timeout, token/caching metrics, tool usage, turn counts, setup/agent/verifier timing.

3. Diagnoser
- Buckets failures by root-cause families:
  - Timeouts with solvable outputs.
  - Tool-loop / no-termination behavior.
  - Setup/env dependency misses.
  - Provider-specific 429/timeout behavior.
  - Multimodal path regressions.

4. Improver
- Generates bounded patch proposals from prioritized failure buckets.
- Patch classes:
  - Prompt/policy changes.
  - Harness/runtime config changes.
  - Tooling behavior improvements (e.g., execute_code guardrails).
  - Provider feature integration (caching/service tier/retries).

5. Gatekeeper
- Runs fixed benchmark slices and full regression gates.
- Rejects candidates failing variance-adjusted thresholds.
- Exports signed run manifests for submission readiness.

## Data Model (Minimum)
Per trial capture:
- `job_id`, `trial_id`, `task_id`, `attempt`
- model/provider/region/service-tier/cache flags
- setup duration, agent duration, verifier duration
- total turns, tool call counts by tool
- token metrics: in/out/reasoning/cache_read/cache_write (if available)
- exception type + final reward
- artifact paths and hashes

## Optimization Priorities (Ordered)
1. Stop-late behavior
- Add explicit completion controller: if solution confidence and verification pass, terminate immediately.
- Add loop detectors for repetitive verify/edit cycles.

2. Timeout efficiency
- Separate setup budget from agent budget in analytics.
- Push expensive dependency setup into prebuilt environment where allowed.

3. Provider reliability
- Tune retry envelopes per provider/model.
- Add/validate prompt caching controls for all supported providers.

4. Tool strategy quality
- Bias against brittle pixel-parsing loops when multimodal image inputs are natively supported.
- Prefer direct model perception path when provider supports image parts.

5. Cost/latency controls
- Service tier policy by run mode (official vs dev).
- Dynamic reasoning level under time pressure.

## Experiment Workflow
1. Baseline snapshot
- Pin commit, config, model, provider, region, and task subset.

2. Candidate generation
- One hypothesis per branch (smallest viable change).

3. Evaluation
- Run A/B on identical task slice with fixed seeds and retry policy.

4. Decision
- Promote only if candidate beats baseline on primary KPI and does not regress critical guardrails.

5. Roll-forward
- Merge, tag, and update baseline.

## Guardrails
- Hard cap daily budget (tokens + infra time).
- Abort rules for runaway loops (tool-call/turn thresholds).
- Auto-fail candidates increasing critical error classes beyond threshold.
- Preserve full run logs + trajectory for auditability.

## TB2 Submission Readiness Checklist
- Clean git worktree for official run commit.
- All required metadata filled (team/org identity, model/provider config, run manifest).
- Required number of official trials per TB2 policy completed.
- Artifacts retained and reproducible from pinned commit + config.
- Submission package validated by local checklist script.

## Milestones
M1 (1-2 days):
- Unified metrics schema and collector.
- Failure bucketing dashboard/report.

M2 (2-4 days):
- Completion controller + loop detector.
- Timeout efficiency pass (setup/runtime split visibility).

M3 (3-5 days):
- Candidate patch autopipeline (generate -> gate -> report).
- Provider reliability tuning playbooks.

M4 (ongoing):
- Nightly self-improvement cycle with promotion rules.
- Weekly official-readiness review and submission run.

## Immediate Next Actions
1. Implement completion controller issue from the active `regex-chess` pattern.
2. Add standardized per-trial JSON summary export for all runs.
3. Wire a simple ranker that prioritizes fixes by expected score lift / engineering effort.
4. Run a 24-hour pilot on a small curated TB2 slice, then tighten gates.
