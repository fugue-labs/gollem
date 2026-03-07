package codetool

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/modelutil"
)

// subagentParams is the input schema for the delegate tool.
type subagentParams struct {
	Task string `json:"task" jsonschema:"description=A clear, self-contained description of the subtask to delegate. Include all necessary context — the subagent has no memory of previous conversation."`
}

// SubAgentTool creates a tool that delegates subtasks to a fresh agent.
// The subagent gets the same coding tools but runs with a focused prompt
// and limited turns, preventing runaway execution. Results are returned
// to the parent agent.
//
// This mirrors the "task" tool in Deep Agents / Claude Code — the key
// differentiator on benchmarks like Terminal-Bench where complex tasks
// benefit from decomposition into focused subtasks.
func SubAgentTool(model core.Model, opts ...Option) core.Tool {
	cfg := applyOpts(opts)
	return core.FuncTool[subagentParams](
		"delegate",
		"Delegate a subtask to a focused subagent. The subagent gets the same "+
			"coding tools (bash, view, edit, multi_edit, write, grep, glob, ls, lsp) and runs "+
			"independently with its own context. Use this for: (1) parallel-safe "+
			"subtasks like researching an API or reading large files, (2) focused "+
			"debugging of a specific component, (3) writing a self-contained module "+
			"or test. The subagent has NO memory of your conversation — include all "+
			"necessary context in the task description.",
		func(ctx context.Context, params subagentParams) (any, error) {
			if params.Task == "" {
				return nil, &core.ModelRetryError{Message: "task description must not be empty"}
			}

			// Generate a task-specific system prompt if a personality generator
			// is configured, falling back to the static prompt on error.
			systemPrompt := subAgentSystemPrompt
			if cfg.PersonalityGenerator != nil {
				if generated, err := cfg.PersonalityGenerator(ctx, modelutil.PersonalityRequest{
					Task:       params.Task,
					Role:       "focused coding subagent",
					BasePrompt: subAgentSystemPrompt,
				}); err == nil {
					systemPrompt = generated
					fmt.Fprintf(os.Stderr, "[gollem] subagent:personality generated (%d chars)\n", len(generated))
				} else {
					fmt.Fprintf(os.Stderr, "[gollem] subagent:personality fallback: %v\n", err)
				}
			}

			// Build a lightweight subagent with coding tools but no delegation
			// (prevents infinite recursion).
			acConfig := subagentAutoContextConfig(cfg)

			// Each subagent gets its own BackgroundProcessManager so
			// background state is isolated from the parent and from
			// sibling subagents (important in team mode where the
			// delegate tool may close over the leader's opts).
			subMgr := NewBackgroundProcessManager()
			subToolOpts := make([]Option, len(opts))
			copy(subToolOpts, opts)
			subToolOpts = append(subToolOpts, WithBackgroundProcessManager(subMgr))

			// Verification tracking middleware (not validator) — gives subagents
			// stagnation, regression, same-error, and stale-test detection without
			// blocking their completion.
			verifyMW, _ := VerificationCheckpoint("")
			reasoningCfg := subagentReasoningConfig()
			if cfg.ReasoningSandwichConfig != nil {
				reasoningCfg = subagentReasoningConfigForMaxEffort(
					cfg.ReasoningSandwichConfig.Planning.ReasoningEffort,
				)
			}

			subOpts := []core.AgentOption[string]{
				core.WithSystemPrompt[string](systemPrompt),
				core.WithToolsets[string](Toolset(subToolOpts...)),
				// Background lifecycle for this subagent: cleanup on run
				// end, completion notifications between turns.
				core.WithHooks[string](core.Hook{
					OnRunEnd: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage, _ error) {
						subMgr.Cleanup()
					},
				}),
				core.WithDynamicSystemPrompt[string](subMgr.CompletionPrompt),
				core.WithTools[string](InvariantsTool(model)),
				core.WithMaxRetries[string](2),
				core.WithUsageLimits[string](core.UsageLimits{RequestLimit: core.IntPtr(50)}),
				core.WithTurnGuardrail[string]("max-turns", core.MaxTurns(50)),
				core.WithDefaultToolTimeout[string](2 * time.Minute),
				// Auto-compress context on long subtasks to prevent context overflow.
				// Uses provider-aware limits when configured (e.g., 150K for Claude).
				core.WithAutoContext[string](acConfig),
				// Truncate oversized content blocks before auto-context sees them.
				// Prevents a single large tool result from dominating subagent context.
				core.WithHistoryProcessor[string](ContentTruncationProcessor(50000)),
				// Environment discovery: give the subagent awareness of directory
				// structure, README, tests, and task type so it doesn't waste turns
				// on basic orientation. #1 source of wasted subagent turns.
				core.WithAgentMiddleware[string](ContextInjectionMiddleware(cfg.WorkDir)),
				// Loop detection: catch subagent doom loops early (3 repeated edits).
				core.WithAgentMiddleware[string](LoopDetectionMiddleware(3)),
				// Progress tracking: nudge subagent to produce output files early.
				// Subagents are prone to analysis paralysis just like the parent.
				// No timeout arg — subagents use turn-based triggers only.
				core.WithAgentMiddleware[string](ProgressTrackingMiddleware(cfg.WorkDir)),
				// Reasoning sandwich: vary thinking budget by phase (planning vs
				// implementation vs verification). Helps subagent reason carefully
				// when analyzing errors and verifying fixes.
				core.WithAgentMiddleware[string](ReasoningSandwichMiddleware(reasoningCfg)),
				// Verification tracking: detect stagnation, regression, same-error
				// patterns, and stale tests. Subagents are not blocked from
				// completing, but they get guidance when stuck in failing loops.
				core.WithAgentMiddleware[string](verifyMW),
				// Context overflow recovery: catches 413 and retries with compressed
				// messages. Subagents can hit overflow on long-running subtasks.
				core.WithAgentMiddleware[string](ContextOverflowMiddleware()),
				// Stderr logging: real-time per-turn observability for subagents.
				core.WithAgentMiddleware[string](stderrLoggingMiddleware()),
			}

			agent := core.NewAgent[string](model, subOpts...)

			fmt.Fprintf(os.Stderr, "[gollem] subagent:start task=%q (context: %dK tokens)\n",
				truncateForLog(params.Task, 100), acConfig.MaxTokens/1000)

			result, err := agent.Run(ctx, params.Task)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[gollem] subagent:error %v\n", err)
				return nil, fmt.Errorf("subagent failed: %w", err)
			}

			fmt.Fprintf(os.Stderr, "[gollem] subagent:done (tokens: %d in, %d out, tools: %d)\n",
				result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.ToolCalls)

			return result.Output, nil
		},
	)
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

const subAgentSystemPrompt = `You are a focused coding assistant executing a specific subtask.
You have access to bash, view, edit, multi_edit, write, grep, glob, ls, and lsp tools.

## Be Concise
Minimize text output. Every character costs tokens. Don't explain what you're about to do — just do it.

## Rules
1. Complete the assigned task precisely — don't do extra work
2. Read relevant files before modifying them
3. Make precise edits — match exact strings including whitespace. Use multi_edit to batch related changes across files in one call.
4. Do NOT use bash (sed, awk, echo, printf) for file editing — use edit, multi_edit, or write instead.
5. Verify your changes work (run tests/builds when appropriate)
6. If something fails, read the FULL error message, fix the root cause, and verify
7. Clean up any temporary/build artifacts you create
8. Report what you did and the outcome clearly in your final response
9. If the task is impossible or blocked, explain why immediately — don't waste turns

## Output First
Create required output files EARLY — even rough drafts. A wrong answer that exists beats a perfect answer that doesn't. You can iterate.

## NEVER Modify Test, Benchmark, or Verifier Files
- DO NOT edit files in /tests/, test directories, benchmark scripts, or verifier scripts
- DO NOT change test parameters, thresholds, data sizes, or expected values
- If a benchmark times out, optimize YOUR code — not the test
- The verifier runs the ORIGINAL test files. Any modifications are ignored during evaluation.

## Error Recovery
When something fails:
1. Read the FULL error output — don't skim
2. Identify the file and line number
3. View that file section
4. Understand WHY it failed before attempting a fix
5. Make the minimal fix needed
6. Re-run the exact same command that failed

If the same fix fails twice, try a fundamentally different approach. Don't keep tweaking the same broken code.

## Working with Data
- For large/binary files (images, data dumps): write a Python script to process them. NEVER read large files line-by-line with sed/awk/head in a loop.
- For pip installs: use --break-system-packages flag
- For stdin: use echo 'input' | program or heredocs, never try to interact

## Performance
Write efficient code. Use O(n log n) over O(n²), hash maps for lookups, vectorized operations over loops. Prefer built-in/native operations.

## Parallel Tool Calls
You can call MULTIPLE tools in a single turn. Batch independent operations: read 3 files at once, write a file and run a test simultaneously. This saves turns.

## Avoid These Failure Modes
1. Don't spend 5+ turns exploring without writing any code
2. Don't modify test files — they define success criteria
3. Don't ignore error messages — they tell you exactly what's wrong
4. Don't overthink — try the simple solution first

Your response will be returned to the parent agent, so be concise but complete.`
