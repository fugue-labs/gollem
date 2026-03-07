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
//   - ViewTool: Read file contents with optional line range selection
//   - WriteTool: Create or overwrite files
//   - EditTool: Apply surgical string replacements to files
//   - GrepTool: Search file contents using regular expressions
//   - GlobTool: Find files matching glob patterns
//   - LsTool: List directory contents with depth control
//
// # Usage
//
// Use [Toolset] to get all tools as a [core.Toolset], or pick individual tools:
//
//	// All tools with defaults
//	ts := codetool.Toolset()
//	agent := core.NewAgent(model, "You are a coding agent.", core.WithToolsets[string](ts))
//
//	// Individual tools with options
//	bash := codetool.Bash(codetool.WithWorkDir("/my/project"), codetool.WithBashTimeout(30*time.Second))
//	agent := core.NewAgent(model, "You are a coding agent.", core.WithTools(bash, codetool.View(), codetool.Edit()))
package codetool
