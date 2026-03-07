// Package codetool provides production-grade coding agent tools for gollem.
//
// These tools give LLM agents the ability to read, write, search, and execute
// code in a local development environment. They are designed for use with
// Terminal-Bench style agent harnesses and general-purpose coding agents.
//
// # Tools
//
// The package provides the following tools:
//
//   - BashTool: Execute shell commands with timeout and working directory support
//   - BashStatusTool: Inspect background processes started or adopted by BashTool
//   - ViewTool: Read file contents with optional line range selection
//   - WriteTool: Create or overwrite files
//   - EditTool: Apply surgical string replacements to files
//   - GrepTool: Search file contents using regular expressions
//   - GlobTool: Find files matching glob patterns
//   - LsTool: List directory contents with depth control
//
// # Usage
//
// Use [AgentOptions] for the recommended coding-agent setup with automatic
// background-process lifecycle, or use [Toolset] / individual tools directly
// when you want to wire that lifecycle yourself:
//
//	// Recommended: automatic lifecycle via AgentOptions
//	opts := codetool.AgentOptions("/my/project")
//	agent := core.NewAgent(model, opts...)
//
//	// Direct toolset use: stateless, so provide a manager yourself
//	mgr := codetool.NewBackgroundProcessManager()
//	ts := codetool.Toolset(
//		codetool.WithWorkDir("/my/project"),
//		codetool.WithBackgroundProcessManager(mgr),
//	)
//	agent := core.NewAgent(model, "You are a coding agent.",
//		core.WithToolsets[string](ts),
//		core.WithHooks[string](core.Hook{
//			OnRunEnd: func(_ context.Context, _ *core.RunContext, _ []core.ModelMessage, _ error) {
//				mgr.Cleanup()
//			},
//		}),
//		core.WithDynamicSystemPrompt[string](mgr.CompletionPrompt),
//	)
//
//	// Individual tools with options
//	mgr := codetool.NewBackgroundProcessManager()
//	bash := codetool.Bash(
//		codetool.WithWorkDir("/my/project"),
//		codetool.WithBashTimeout(30*time.Second),
//		codetool.WithBackgroundProcessManager(mgr),
//	)
//	status := codetool.BashStatus(codetool.WithBackgroundProcessManager(mgr))
//	agent := core.NewAgent(model, "You are a coding agent.", core.WithTools(bash, status, codetool.View(), codetool.Edit()))
//
// Background process support is available in two layers:
//
//   - Tool-level: call Bash with `background=true` and query progress with BashStatus.
//     When you construct tools manually, pass a shared BackgroundProcessManager via
//     WithBackgroundProcessManager so both tools reference the same process pool.
//     [Toolset] is stateless and does not auto-wire cleanup or completion prompts.
//   - Manager-level: if you start a process yourself, hand it to
//     BackgroundProcessManager.Adopt or AdoptWithWait so the manager assigns an ID,
//     captures output, tracks completion, and exposes status through BashStatus.
package codetool
