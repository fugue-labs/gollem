// Package temporal provides Temporal durable execution support for gollem
// agents.
//
// TemporalAgent.Run itself still delegates to the wrapped agent directly for
// in-process use. Durable execution happens through TemporalAgent.RunWorkflow
// (or the registered workflow name returned by WorkflowName), which offloads
// model requests, tool calls, and callback-driven side effects to Temporal
// activities.
//
// While the workflow is running, clients can query ta.StatusQueryName() for a
// WorkflowStatus snapshot, signal ta.ApprovalSignalName() for tools marked with
// core.WithRequiresApproval(), signal ta.DeferredResultSignalName() for deferred
// tool results, or signal ta.AbortSignalName() to stop a waiting run.
//
// TemporalAgent also supports stable registration versioning with
// temporal.WithVersion(...) and workflow rollover thresholds with
// temporal.WithContinueAsNew(...). WorkflowStatus and WorkflowOutput include
// continue-as-new metadata so callers can observe how often a run has rolled
// over. For workers that host multiple durable agents, temporal.RegisterAll(...)
// registers every workflow and activity set in one call.
//
// The current workflow path also executes dynamic system prompts, history
// processors, input/turn guardrails, lifecycle hooks, run conditions,
// tool preparation callbacks, non-streaming request middleware,
// message/response interceptors, output repair/validation, custom tool
// approval callbacks, knowledge-base retrieval/storage, usage quota checks,
// toolsets, tool result validators, tracing, trace exporters, cost
// estimates, event bus integration, agent deps, and auto-context compression.
// Stream middleware is available through the streaming model activity for
// custom workflows; the built-in RunWorkflow path uses non-streaming model
// requests.
//
// For the full execution model, operational API, caveats, and examples, see
// ext/temporal/README.md in the repository.
//
// Usage:
//
//	agent := core.NewAgent[string](model, core.WithTools(myTool))
//	temporalAgent := temporal.NewTemporalAgent(agent,
//		temporal.WithName("my-agent"),
//		temporal.WithVersion("2026_03"),
//	)
//
//	// Worker setup:
//	w := worker.New(client, "my-queue", worker.Options{})
//	_ = temporal.RegisterAll(w, temporalAgent)
//
//	// Start the durable workflow:
//	run, _ := client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
//		TaskQueue: "my-queue",
//	}, temporalAgent.WorkflowName(), temporal.WorkflowInput{
//		Prompt: "Hello",
//	})
//
//	var output temporal.WorkflowOutput
//	_ = run.Get(ctx, &output)
//	result, _ := temporalAgent.DecodeWorkflowOutput(&output)
package temporal
