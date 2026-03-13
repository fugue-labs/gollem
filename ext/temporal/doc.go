// Package temporal provides Temporal activity scaffolding for gollem agents.
// It exports named model and tool activities plus worker registration helpers
// so callers can build Temporal workflows around compatible core.Agent runs.
//
// TemporalAgent.Run itself delegates to the wrapped agent directly. Durable
// execution happens when you call the exported activities from your own
// Temporal workflow. NewTemporalAgent conservatively rejects agent features
// that the current integration cannot support yet.
//
// Usage:
//
//	agent := core.NewAgent[string](model, core.WithTools(myTool))
//	temporalAgent := temporal.NewTemporalAgent(agent, temporal.WithName("my-agent"))
//
//	// Outside a workflow, Run delegates to the wrapped agent:
//	result, err := temporalAgent.Run(ctx, "Hello")
//
//	// Worker setup for custom workflows:
//	w := worker.New(client, "my-queue", worker.Options{})
//	w.RegisterWorkflow(MyWorkflow)
//	temporal.RegisterActivities(w, temporalAgent)
package temporal
