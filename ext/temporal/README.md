# Temporal Durable Execution

`ext/temporal` turns a normal `gollem` agent into a Temporal-backed durable
workflow.

The important boundary is this:

- `TemporalAgent.Run(...)` is still the normal in-process path.
- `TemporalAgent.RunWorkflow(...)` is the durable path.

The durable path keeps workflow code deterministic and moves framework-owned
side effects into Temporal activities.

JSON-valued payload fields are emitted as nested JSON rather than `[]byte`
blobs so the Temporal UI and decoded workflow history stay readable for humans.
The main exception is `DepsJSON`, which remains codec-driven binary data.

## What Runs Where

| Surface | What it does | Durable |
| --- | --- | --- |
| `ta.Run(ctx, prompt, ...)` | Calls the wrapped `core.Agent` directly | No |
| `ta.RunWorkflow(ctx, input)` | Built-in Temporal workflow runner | Yes |
| `ta.Register(worker)` | Registers the durable workflow plus all activities | Yes |
| `temporal.RegisterAll(worker, ta...)` | Registers multiple Temporal agents at once | Yes |
| `ta.Activities()` | Returns the activity map for custom worker registration | N/A |
| `ta.GetModel().ModelRequestStreamActivity(...)` | Streaming model activity for custom workflows | Activity is durable; built-in workflow does not use it |

## Execution Model

The built-in workflow is intentionally simple:

1. Build or restore run state.
2. Apply callback-driven preprocessing through activities.
3. Execute the model request through a Temporal activity.
4. Execute tool calls through Temporal activities.
5. Wait on Temporal signals when a tool needs approval or returns `CallDeferred`.
6. Persist state, trace, cost, and tool state in workflow history and snapshots.
7. Continue-as-new when configured thresholds are hit.

This keeps Temporal replay deterministic while still preserving most of the
normal `gollem` agent behavior.

## Quick Start

```go
package main

import (
    "context"
    "time"

    "go.temporal.io/sdk/client"
    "go.temporal.io/sdk/worker"

    "github.com/fugue-labs/gollem/core"
    "github.com/fugue-labs/gollem/ext/temporal"
)

func main() {
    model := core.NewTestModel(core.TextResponse("done"))
    agent := core.NewAgent[string](model,
        core.WithSystemPrompt[string]("Be concise."),
    )

    ta := temporal.NewTemporalAgent(agent,
        temporal.WithName("project-assistant"),
        temporal.WithVersion("2026_03"),
        temporal.WithActivityConfig(temporal.ActivityConfig{
            StartToCloseTimeout: 2 * time.Minute,
            MaxRetries:          3,
            InitialInterval:     time.Second,
        }),
        temporal.WithContinueAsNew(temporal.ContinueAsNewConfig{
            MaxTurns:         50,
            MaxHistoryLength: 10000,
            OnSuggested:      true,
        }),
    )

    c, _ := client.Dial(client.Options{})
    defer c.Close()

    w := worker.New(c, "gollem", worker.Options{})
    _ = ta.Register(w)
    go w.Run(worker.InterruptCh())

    run, _ := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
        ID:        "project-assistant-1",
        TaskQueue: "gollem",
    }, ta.WorkflowName(), temporal.WorkflowInput{
        Prompt: "Summarize the current project status",
    })

    var output temporal.WorkflowOutput
    _ = run.Get(context.Background(), &output)

    result, _ := ta.DecodeWorkflowOutput(&output)
    _ = result
}
```

## Naming And Registration

`WithName(...)` is required. It is the stable base for every workflow and
activity name.

If you also set `WithVersion(...)`, the registration base becomes:

- `<name>__v__<version>`

That registration base is then used to derive:

- workflow name: `agent__<registration>__workflow`
- model activity: `agent__<registration>__model_request`
- model stream activity: `agent__<registration>__model_request_stream`
- tool activity: `agent__<registration>__tool__<tool_name>`
- callback/helper activities: `agent__<registration>__...`

Use versioning when old and new workers may coexist during deployment.

## Workflow Input

Most callers only need `Prompt`.

| Field | Typical use |
| --- | --- |
| `Prompt` | The user prompt for the run |
| `DepsJSON` | Per-workflow dependency override, usually produced by `ta.MarshalDeps(...)` |
| `ModelSettings` | Per-run model settings override |
| `UsageLimits` | Per-run usage limit override |
| `InitialRequestParts` | Advanced: prepend extra initial request parts as `[]core.SerializedPart` |
| `Snapshot` | Advanced/internal: restore a prior run snapshot as `*core.SerializedRunSnapshot`; external resumes behave like `core.WithSnapshot(...)` and append a fresh initial request for the supplied prompt |
| `DeferredResults` | Advanced/internal: inject deferred tool results on resume |
| `TraceSteps` | Internal: preserved across continue-as-new |
| `ContinueAsNew*` | Internal: preserved across continue-as-new |

Legacy compatibility fields still exist on `WorkflowInput`:

- `InitialRequestPartsJSON`
- `SnapshotJSON`
- `TraceStepsJSON`

New code should prefer the structured fields above. The raw `*JSON` forms are
still accepted so older callers and older histories continue to decode cleanly.

## Workflow Output

`WorkflowOutput` is the serialized durable result.

| Field | Meaning |
| --- | --- |
| `Completed` | Whether the workflow completed successfully |
| `OutputJSON` | Serialized final output |
| `Snapshot` | Full run snapshot as `*core.SerializedRunSnapshot` |
| `Trace` | Serialized run trace as `*core.RunTrace` |
| `Cost` | Final cost snapshot when cost tracking is enabled |
| `DeferredRequests` | Present for advanced/manual resume flows |
| `ContinueAsNewCount` | Number of workflow rollovers |

For normal use, call `ta.DecodeWorkflowOutput(&output)` and work with the
returned `*core.RunResult[T]`.

Legacy compatibility fields still exist on `WorkflowOutput`:

- `SnapshotJSON`
- `TraceJSON`

Fresh workflow executions populate `Snapshot` and `Trace` so decoded Temporal
history is readable in the UI and CLI.

## Query And Signal Surface

The built-in durable workflow exposes one query and three signals:

| API | Purpose |
| --- | --- |
| `ta.StatusQueryName()` | Return current `WorkflowStatus` |
| `ta.ApprovalSignalName()` | Approve or deny a waiting tool call |
| `ta.DeferredResultSignalName()` | Supply the result of a deferred tool call |
| `ta.AbortSignalName()` | Abort a workflow that is waiting for external input |

Typical client usage:

```go
workflowID := "project-assistant-1"

query, _ := c.QueryWorkflow(ctx, workflowID, "", ta.StatusQueryName())
var status temporal.WorkflowStatus
_ = query.Get(&status)

messages, _ := temporal.DecodeWorkflowStatusMessages(&status)
trace, _ := temporal.DecodeWorkflowStatusTrace(&status)
_, _ = messages, trace

_ = c.SignalWorkflow(ctx, workflowID, "", ta.ApprovalSignalName(), temporal.ApprovalSignal{
    ToolName:   "dangerous_action",
    ToolCallID: "call_approval",
    Approved:   true,
})

_ = c.SignalWorkflow(ctx, workflowID, "", ta.DeferredResultSignalName(), temporal.DeferredResultSignal{
    ToolName:   "async_task",
    ToolCallID: "call_123",
    Content:    "resolved result",
})

_ = c.SignalWorkflow(ctx, workflowID, "", ta.AbortSignalName(), temporal.AbortSignal{
    Reason: "operator cancelled the run",
})
```

Use an empty Temporal run ID when signaling/querying by stable workflow ID so
you target the current execution after continue-as-new.

## WorkflowStatus

`WorkflowStatus` is the operational view of a running durable agent. It
includes:

- `RunID`, `RunStep`, and `Usage`
- `WorkflowName`, `RegistrationName`, and `Version`
- `Messages`, `Snapshot`, `Trace`, and `Cost`
- `PendingApprovals` and `DeferredRequests`
- `Waiting`, `WaitingReason`, `Completed`, `Aborted`, and `LastError`
- Temporal history metrics and continue-as-new counters

This is the right surface for UIs and operators that need to inspect an active
run without waiting for completion.

## Readable Payload Shapes

The durable workflow now emits structured payloads for the fields humans
actually inspect in Temporal:

- `WorkflowStatus.Messages` is `[]core.SerializedMessage`
- `WorkflowStatus.Snapshot` and `WorkflowOutput.Snapshot` are `*core.SerializedRunSnapshot`
- `WorkflowStatus.Trace` and `WorkflowOutput.Trace` are `*core.RunTrace`
- `ModelActivityInput.Messages` and `ModelActivityOutput.Response` are also structured

Those types are JSON-safe envelope forms, so Temporal shows nested JSON rather
than base64-encoded `[]byte` blobs in decoded history views.

In practice that means a completed workflow result now looks like:

- `output_json`: the final structured agent output
- `snapshot.messages`: the message history as readable request/response envelopes
- `trace`: the run trace, when tracing is enabled

The intentionally different field is `DepsJSON`, which stays codec-driven
binary because dep serialization is pluggable and is not guaranteed to be JSON.

Deprecated raw compatibility fields remain on the public structs:

- `MessagesJSON`
- `SnapshotJSON`
- `TraceJSON`
- `InitialRequestPartsJSON`
- `TraceStepsJSON`

They are there to decode older histories and older callers. New code should
prefer the structured fields and the decode helpers:

- `ta.DecodeWorkflowOutput(&output)`
- `temporal.DecodeWorkflowStatusMessages(&status)`
- `temporal.DecodeWorkflowStatusTrace(&status)`

## Dependency Injection

Temporal workflows cannot carry arbitrary in-memory Go values, so durable dep
overrides must be serialized.

Default behavior:

- If the wrapped agent already has default deps, `NewTemporalAgent(...)`
  infers the concrete dep type from that value.
- Otherwise, provide `WithDepsPrototype(...)` so the Temporal wrapper knows
  what type to decode.
- Serialization uses `JSONDepsCodec` by default.

Custom dep example:

```go
type Deps struct {
    TenantID string `json:"tenant_id"`
}

agent := core.NewAgent[string](model,
    core.WithDeps[string](Deps{TenantID: "default"}),
)

ta := temporal.NewTemporalAgent(agent, temporal.WithName("deps-demo"))

depsJSON, _ := ta.MarshalDeps(Deps{TenantID: "acme"})
startOpts := client.StartWorkflowOptions{TaskQueue: "gollem"}

run, _ := c.ExecuteWorkflow(ctx, startOpts, ta.WorkflowName(), temporal.WorkflowInput{
    Prompt:   "Run for tenant acme",
    DepsJSON: depsJSON,
})
```

The decoded deps are available to tools and callback activities through the
reconstructed `RunContext`.

## Continue-As-New

Use `WithContinueAsNew(...)` to bound workflow history growth.

Supported thresholds:

- `MaxTurns`
- `MaxMessages`
- `MaxHistoryLength`
- `MaxHistorySize`
- `OnSuggested`

When a rollover happens, the Temporal workflow carries forward:

- messages and run snapshot
- trace steps
- usage
- tool state
- dep overrides
- continue-as-new counters

The returned `WorkflowOutput` and queryable `WorkflowStatus` both include
`ContinueAsNewCount`.

Internal continue-as-new resumes restore the logical run in place. They do not
re-run input guardrails, publish a second `run_start` event, or fire
`OnRunStart` again on each rollover.

## Activity Configuration

`ActivityConfig` controls:

- `StartToCloseTimeout`
- `MaxRetries`
- `InitialInterval`

You can set:

- a default config with `WithActivityConfig(...)`
- a model-specific override with `WithModelActivityConfig(...)`
- per-tool overrides with `WithToolActivityConfig(...)`

## Durable Feature Coverage

The built-in `RunWorkflow` path durably supports these core behaviors:

- model requests
- direct tools, toolsets, and stateful tools
- dynamic system prompts
- history processors
- input and turn guardrails
- lifecycle hooks
- run conditions
- output repair and output validation
- custom tool approval callbacks
- tool result validators
- request middleware
- knowledge-base retrieve and auto-store
- usage limits and usage quotas
- tracing, trace exporters, and cost tracking
- event bus publication plus `RunContext.EventBus` inside activities
- agent deps and per-workflow dep overrides
- auto-context compression

Nuances:

- Stream middleware is supported by `TemporalModel.ModelRequestStreamActivity`
  and custom workflows that call it. The built-in `RunWorkflow` path currently
  uses non-streaming model requests.
- Approval waits and deferred tool results are workflow-native. The workflow
  stays alive and waits for signals rather than returning a paused result.

## Built-In Workflow Versus Custom Workflows

Use the built-in workflow when you want standard `gollem` agent semantics with
durability.

Use a custom workflow when you want to orchestrate the activities yourself.
`ta.Activities()` and `temporal.RegisterActivities(...)` expose the full
activity set, including the streaming model activity.

If you build a custom workflow, keep the same rule Temporal requires
everywhere:

- workflow code must stay deterministic
- model calls, tool calls, and other side effects must stay behind activities

## Caveats

- `TemporalAgent.Run(...)` is not durable.
- `WithToolPassthrough(...)` is currently rejected by `NewTemporalAgent(...)`
  because the built-in durable workflow cannot support passthrough execution.
- `WithEventHandler(...)` is exposed back through `ta.EventHandler()` for
  custom workflow integrations. The built-in durable workflow still does not
  stream token events through it.
- The worker must register both the workflow and all activities. `ta.Register`
  or `temporal.RegisterAll` is the simplest way to do that.

## Related Files

- Example: [`examples/temporal/main.go`](../../examples/temporal/main.go) for a
  real worker + workflow + query + approval-signal flow against a Temporal
  server
- Package docs: [`ext/temporal/doc.go`](./doc.go)
- Root overview: [`README.md`](../../README.md)
