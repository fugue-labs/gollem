// Command gollem provides a minimal CLI for debugging gollem agents via the TUI.
//
// Usage:
//
//	gollem debug --provider openai --model gpt-4o "prompt"
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/tui"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Parse arguments.
	cmd := os.Args[1]
	if cmd != "debug" {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	provider := "test"
	modelName := ""
	var prompt string

	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider":
			if i+1 < len(args) {
				provider = args[i+1]
				i++
			}
		case "--model":
			if i+1 < len(args) {
				modelName = args[i+1]
				i++
			}
		case "--help", "-h":
			printUsage()
			os.Exit(0)
		default:
			if !strings.HasPrefix(args[i], "-") {
				prompt = args[i]
			}
		}
	}

	if prompt == "" {
		fmt.Fprintln(os.Stderr, "error: prompt is required")
		printUsage()
		os.Exit(1)
	}

	// Create model based on provider.
	model, err := createModel(provider, modelName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating model: %v\n", err)
		os.Exit(1)
	}

	// Create and run agent with TUI.
	agent := core.NewAgent[string](model,
		core.WithSystemPrompt[string]("You are a helpful assistant."),
	)

	result, err := tui.DebugUI(agent, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if result != nil {
		fmt.Printf("\nResult: %s\n", result.Output)
		fmt.Printf("Tokens: %d input, %d output\n",
			result.Usage.InputTokens, result.Usage.OutputTokens)
	}
}

func createModel(provider, modelName string) (core.Model, error) {
	switch provider {
	case "test":
		// Use the test model for demonstration/testing.
		return core.NewTestModel(
			core.TextResponse("Hello! I'm a test model. This is a demonstration of the TUI debugger."),
		), nil
	default:
		return nil, fmt.Errorf("provider %q not supported in CLI (use 'test' for demo, or import a real provider in your own code)", provider)
	}

	// Note: For real providers, users should import the appropriate provider package
	// and use the TUI directly in their own code:
	//
	//   import "github.com/fugue-labs/gollem/provider/openai"
	//   model := openai.New(openai.WithModel("gpt-4o"))
	//   result, err := tui.DebugUI(agent, prompt)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `gollem - Agent debugging TUI

Usage:
  gollem debug [options] "prompt"

Options:
  --provider <name>  Model provider (default: test)
  --model <name>     Model name
  -h, --help         Show this help

Examples:
  gollem debug "Tell me about Tokyo"
  gollem debug --provider test "What is 2+2?"
`)
}
