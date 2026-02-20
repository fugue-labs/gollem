// Example simple demonstrates basic Agent[CityInfo] usage with the Vertex AI provider,
// showing structured output extraction from an LLM.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/vertexai"
)

// CityInfo is the structured output type the agent will produce.
type CityInfo struct {
	Name              string  `json:"name" jsonschema:"description=City name"`
	Country           string  `json:"country" jsonschema:"description=Country the city is in"`
	Population        int     `json:"population" jsonschema:"description=Approximate population"`
	FavoriteFood      string  `json:"favorite_food" jsonschema:"description=A popular local dish"`
	LeastFavoriteFood string  `json:"least_favorite_food" jsonschema:"description=A less popular local dish"`
	Latitude          float64 `json:"latitude" jsonschema:"description=Latitude coordinate"`
	Longitude         float64 `json:"longitude" jsonschema:"description=Longitude coordinate"`
}

func main() {
	// Create a provider using Vertex AI with Gemini.
	model := vertexai.New(
		vertexai.WithModel("gemini-3-flash-preview"),
		vertexai.WithLocation("global"),
	)

	// Create an agent that returns structured CityInfo.
	agent := core.NewAgent[CityInfo](model,
		core.WithSystemPrompt[CityInfo]("You are a geography expert. Answer questions about cities with accurate data."),
	)

	// Run the agent with a prompt.
	result, err := agent.Run(context.Background(), "Tell me about Tokyo")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%v\n", result)
	fmt.Printf("\nToken usage: %d input, %d output\n",
		result.Usage.InputTokens, result.Usage.OutputTokens)
}
