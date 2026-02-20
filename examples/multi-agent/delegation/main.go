// Example delegation demonstrates the AgentTool pattern where one agent
// delegates work to another agent by calling it as a tool. The outer
// "orchestrator" agent decides when to invoke the inner "specialist" agent,
// and the inner agent's output is returned as the tool result.
//
// This example uses TestModel so it compiles and runs without any API keys.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/orchestration"
)

// ResearchResult is the output type for the specialist research agent.
type ResearchResult struct {
	Topic   string   `json:"topic" jsonschema:"description=The research topic"`
	Summary string   `json:"summary" jsonschema:"description=Summary of findings"`
	Sources []string `json:"sources" jsonschema:"description=List of sources consulted"`
}

// FinalReport is the output type for the orchestrator agent.
type FinalReport struct {
	Title      string `json:"title" jsonschema:"description=Report title"`
	Content    string `json:"content" jsonschema:"description=Full report content"`
	Conclusion string `json:"conclusion" jsonschema:"description=Final conclusion"`
}

func main() {
	// --- Inner Agent: Research Specialist ---
	// This agent handles research tasks delegated by the orchestrator.
	researchModel := core.NewTestModel(
		core.ToolCallResponse("final_result", `{
			"topic": "Go generics",
			"summary": "Go 1.18 introduced type parameters enabling generic programming. Key features include type constraints via interfaces, type inference, and the comparable constraint.",
			"sources": ["go.dev/doc/tutorial/generics", "go.dev/ref/spec#Type_parameters"]
		}`),
	)

	researchAgent := core.NewAgent[ResearchResult](researchModel,
		core.WithSystemPrompt[ResearchResult]("You are a research specialist. Investigate topics thoroughly and return structured findings."),
	)

	// --- Outer Agent: Orchestrator ---
	// This agent decides what to research and composes the final report.
	orchestratorModel := core.NewTestModel(
		// First response: orchestrator calls the research agent tool.
		core.ToolCallResponse("research", `{"prompt":"Research Go generics: history, features, and best practices"}`),
		// Second response: orchestrator produces the final report.
		core.ToolCallResponse("final_result", `{
			"title": "The State of Go Generics",
			"content": "Go generics were introduced in Go 1.18 and have since become a foundational feature. Type parameters enable writing reusable, type-safe code without sacrificing Go's simplicity.",
			"conclusion": "Go generics strike a balance between expressiveness and simplicity, making them suitable for libraries and frameworks."
		}`),
	)

	// Wrap the research agent as a tool using AgentTool.
	researchTool := orchestration.AgentTool("research", "Delegate research tasks to a specialist agent", researchAgent)

	orchestratorAgent := core.NewAgent[FinalReport](orchestratorModel,
		core.WithSystemPrompt[FinalReport]("You are a report writer. Use the research tool to gather information, then compose a comprehensive report."),
		core.WithTools[FinalReport](researchTool),
	)

	// Run the orchestrator agent.
	result, err := orchestratorAgent.Run(context.Background(), "Write a report about Go generics")
	if err != nil {
		log.Fatal(err)
	}

	// Print the final report.
	fmt.Println("=== Final Report ===")
	fmt.Printf("Title: %s\n\n", result.Output.Title)
	fmt.Printf("Content:\n%s\n\n", result.Output.Content)
	fmt.Printf("Conclusion:\n%s\n\n", result.Output.Conclusion)
	fmt.Printf("Orchestrator usage: %d requests, %d tool calls\n",
		result.Usage.Requests, result.Usage.ToolCalls)
}
