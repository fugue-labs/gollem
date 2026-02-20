// Example streaming demonstrates RunStream with iter.Seq2 text streaming,
// printing model output to the terminal in real time as it arrives.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/anthropic"
)

func main() {
	model := anthropic.New()

	// Create a string agent for free-form text output.
	agent := core.NewAgent[string](model,
		core.WithSystemPrompt[string]("You are a creative storyteller. Write engaging short stories."),
	)

	// Start a streaming run.
	stream, err := agent.RunStream(context.Background(), "Write a very short story about a robot learning to paint")
	if err != nil {
		log.Fatal(err)
	}

	// Stream text deltas to stdout in real time.
	fmt.Println("--- Story ---")
	for text, err := range stream.StreamText(true) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(text)
	}
	fmt.Println("\n--- End ---")

	// Get the final response with usage info.
	resp, err := stream.GetOutput()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nModel: %s\n", resp.ModelName)
	fmt.Printf("Tokens: %d input, %d output\n",
		resp.Usage.InputTokens, resp.Usage.OutputTokens)
}
