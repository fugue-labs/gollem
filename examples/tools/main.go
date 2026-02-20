// Example tools demonstrates Agent with FuncTool, showing how the model
// can call tools and use their results to produce a final answer.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/anthropic"
)

// WeatherParams defines the parameters for the weather tool.
type WeatherParams struct {
	City string `json:"city" jsonschema:"description=The city to get weather for"`
}

// WeatherResult is the structured output type.
type WeatherResult struct {
	City        string `json:"city"`
	Temperature int    `json:"temperature"`
	Conditions  string `json:"conditions"`
	Suggestion  string `json:"suggestion"`
}

func main() {
	model := anthropic.New()

	// Create a tool that the agent can call.
	weatherTool := core.FuncTool[WeatherParams](
		"get_weather",
		"Get the current weather for a city",
		func(ctx context.Context, params WeatherParams) (string, error) {
			// In a real app, this would call a weather API.
			return fmt.Sprintf("Weather in %s: 72°F, sunny with light clouds", params.City), nil
		},
	)

	// Create an agent with the tool.
	agent := core.NewAgent[WeatherResult](model,
		core.WithSystemPrompt[WeatherResult]("You are a helpful weather assistant. Use the get_weather tool to look up weather, then provide a summary with clothing suggestions."),
		core.WithTools[WeatherResult](weatherTool),
	)

	result, err := agent.Run(context.Background(), "What's the weather like in San Francisco?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("City: %s\n", result.Output.City)
	fmt.Printf("Temperature: %d°F\n", result.Output.Temperature)
	fmt.Printf("Conditions: %s\n", result.Output.Conditions)
	fmt.Printf("Suggestion: %s\n", result.Output.Suggestion)
	fmt.Printf("\nRequests made: %d, Tool calls: %d\n",
		result.Usage.Requests, result.Usage.ToolCalls)
}
