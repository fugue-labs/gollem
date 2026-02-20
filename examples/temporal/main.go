// Example temporal demonstrates how to wrap a gollem agent for Temporal durable
// execution. Model requests and tool calls are wrapped as Temporal activities,
// providing automatic checkpointing and fault tolerance.
//
// This example uses TestModel so it compiles and runs without a real Temporal
// server or LLM provider. In production, you would replace TestModel with a
// real provider and register the activities with a Temporal worker.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/temporal"
)

// TaskResult is the structured output type for the agent.
type TaskResult struct {
	Summary string `json:"summary" jsonschema:"description=Summary of the completed task"`
	Status  string `json:"status" jsonschema:"description=Task status: success or failure"`
}

func main() {
	// Create a TestModel with canned responses simulating a tool call followed
	// by a final result.
	model := core.NewTestModel(
		// First response: model calls the "lookup" tool.
		core.ToolCallResponse("lookup", `{"query":"project status"}`),
		// Second response: model returns the final structured result.
		core.ToolCallResponse("final_result", `{"summary":"Project is on track with 90% completion","status":"success"}`),
	)

	// Create a tool that the agent can use.
	lookupTool := core.FuncTool[struct {
		Query string `json:"query" jsonschema:"description=The query to look up"`
	}](
		"lookup",
		"Look up information about a topic",
		func(_ context.Context, params struct {
			Query string `json:"query" jsonschema:"description=The query to look up"`
		}) (string, error) {
			return fmt.Sprintf("Results for %q: Everything is proceeding as planned.", params.Query), nil
		},
	)

	// Create a standard gollem agent.
	agent := core.NewAgent[TaskResult](model,
		core.WithSystemPrompt[TaskResult]("You are a project management assistant."),
		core.WithTools[TaskResult](lookupTool),
	)

	// Wrap the agent for Temporal durable execution.
	// In production, this would enable automatic checkpointing and retry.
	ta := temporal.NewTemporalAgent(agent,
		temporal.WithName("project-assistant"),
		temporal.WithActivityConfig(temporal.ActivityConfig{
			StartToCloseTimeout: 120 * time.Second,
			MaxRetries:          3,
			InitialInterval:     time.Second,
		}),
	)

	// Retrieve the activities map. In production, you would register these
	// with a Temporal worker:
	//
	//   w := worker.New(temporalClient, "task-queue", worker.Options{})
	//   temporal.RegisterActivities(w, ta)
	//   w.Run(worker.InterruptCh())
	activities := ta.Activities()
	fmt.Printf("Registered %d Temporal activities:\n", len(activities))
	for name := range activities {
		fmt.Printf("  - %s\n", name)
	}
	fmt.Println()

	// For demonstration, run the agent directly (outside a Temporal workflow).
	// In production, this would be called inside a workflow function.
	result, err := ta.Run(context.Background(), "What is the current project status?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Summary: %s\n", result.Output.Summary)
	fmt.Printf("Status: %s\n", result.Output.Status)
	fmt.Printf("Requests: %d, Tool calls: %d\n", result.Usage.Requests, result.Usage.ToolCalls)
}
