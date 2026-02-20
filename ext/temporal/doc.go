// Package temporal provides Temporal durable execution for gollem agents.
// It wraps standard core.Agent runs as Temporal workflows, with model requests
// and tool calls executed as Temporal activities for automatic checkpointing,
// fault tolerance, and replay.
//
// Usage:
//
//	agent := core.NewAgent[string](model, core.WithTools(myTool))
//	temporalAgent := temporal.NewTemporalAgent(agent, temporal.WithName("my-agent"))
//
//	// In a Temporal workflow:
//	result, err := temporalAgent.Run(ctx, "Hello")
//
//	// Worker setup:
//	w := worker.New(client, "my-queue", worker.Options{})
//	w.RegisterWorkflow(MyWorkflow)
//	temporal.RegisterActivities(w, temporalAgent)
package temporal
