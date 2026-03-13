// Example temporal demonstrates the current Temporal integration surface:
// compatible model requests and tool calls are exported as Temporal activities
// that you can register with a worker and call from your own workflow.
//
// This example uses TestModel so it compiles and runs without a real Temporal
// server or LLM provider. In production, you would replace TestModel with a
// real provider, register the activities with a Temporal worker, and invoke
// them from a workflow. Calling ta.Run directly still executes in-process.
package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/temporal"
)

// TaskResult is the structured output type for the agent.
type TaskResult struct {
	Summary  string `json:"summary" jsonschema:"description=Summary of the completed task"`
	Status   string `json:"status" jsonschema:"description=Task status: success, warning, or failure"`
	NextStep string `json:"next_step" jsonschema:"description=Recommended next step"`
}

func main() {
	// Create a TestModel with canned responses simulating a tool call followed
	// by a final result.
	model := core.NewTestModel(
		// Run 1: status check.
		core.ToolCallResponse("lookup", `{"query":"project status"}`),
		core.ToolCallResponse("final_result", `{
			"summary":"Project is on track with 90% completion",
			"status":"success",
			"next_step":"Start release readiness checklist"
		}`),
		// Run 2: risk check.
		core.ToolCallResponse("lookup", `{"query":"project risks"}`),
		core.ToolCallResponse("final_result", `{
			"summary":"Two delivery risks identified: QA staffing and dependency lag",
			"status":"warning",
			"next_step":"Escalate staffing request and lock dependency versions"
		}`),
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
			switch params.Query {
			case "project status":
				return `Timeline: green; Milestones: 9/10 complete; Blockers: none`, nil
			case "project risks":
				return `Risks: QA staffing low, upstream dependency still changing`, nil
			default:
				return fmt.Sprintf("Results for %q: no critical updates.", params.Query), nil
			}
		},
	)

	// Create a standard gollem agent.
	agent := core.NewAgent[TaskResult](model,
		core.WithSystemPrompt[TaskResult]("You are a project management assistant."),
		core.WithTools[TaskResult](lookupTool),
	)

	// Wrap the agent and export Temporal activities for a custom workflow.
	// NewTemporalAgent validates that the agent only uses features supported by
	// the current Temporal activity scaffolding path.
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
	names := make([]string, 0, len(activities))
	for name := range activities {
		names = append(names, name)
	}
	// Stable ordering keeps example output easy to compare.
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("  - %s\n", name)
	}
	fmt.Println()

	scenarios := []string{
		"What is the current project status?",
		"What are the top project risks?",
	}
	for i, prompt := range scenarios {
		result, err := ta.Run(context.Background(), prompt)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("=== Scenario %d ===\n", i+1)
		fmt.Printf("Prompt: %s\n", prompt)
		fmt.Printf("Summary: %s\n", result.Output.Summary)
		fmt.Printf("Status: %s\n", result.Output.Status)
		fmt.Printf("Next step: %s\n", result.Output.NextStep)
		fmt.Printf("Requests: %d, Tool calls: %d\n\n", result.Usage.Requests, result.Usage.ToolCalls)
	}
}
