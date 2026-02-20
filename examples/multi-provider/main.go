// Example multi-provider demonstrates using the same agent with different
// LLM providers, showing how gollem enables provider-agnostic code.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/anthropic"
	"github.com/fugue-labs/gollem/provider/openai"
)

// Answer is the structured output type.
type Answer struct {
	Text       string `json:"text" jsonschema:"description=The answer text"`
	Confidence string `json:"confidence" jsonschema:"description=Confidence level: high, medium, or low,enum=high|medium|low"`
}

func main() {
	// Select provider based on command line or environment.
	var model core.Model
	provider := "anthropic"
	if len(os.Args) > 1 {
		provider = os.Args[1]
	}

	switch provider {
	case "anthropic":
		model = anthropic.New()
	case "openai":
		model = openai.New()
	default:
		log.Fatalf("Unknown provider: %s (use 'anthropic' or 'openai')", provider)
	}

	fmt.Printf("Using provider: %s (model: %s)\n\n", provider, model.ModelName())

	// The same agent definition works with any provider.
	agent := core.NewAgent[Answer](model,
		core.WithSystemPrompt[Answer]("You are a knowledgeable assistant. Answer questions concisely and indicate your confidence level."),
	)

	result, err := agent.Run(context.Background(), "What is the speed of light in a vacuum?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Answer: %s\n", result.Output.Text)
	fmt.Printf("Confidence: %s\n", result.Output.Confidence)
	fmt.Printf("Tokens: %d input, %d output\n",
		result.Usage.InputTokens, result.Usage.OutputTokens)
}
