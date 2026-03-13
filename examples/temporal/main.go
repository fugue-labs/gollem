// Example temporal runs a real Temporal-backed gollem agent against a Temporal
// server. It demonstrates the durable path end to end:
//   - start a worker
//   - execute a workflow-backed agent run
//   - query WorkflowStatus while the run is waiting
//   - signal approval for a tool that requires human confirmation
//   - decode the final durable result
//
// By default it connects to a local Temporal dev server on localhost:7233 and
// prints a workflow URL for the Temporal UI on localhost:8080. Override those
// with TEMPORAL_ADDRESS, TEMPORAL_NAMESPACE, and TEMPORAL_UI_URL if needed.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/temporal"
)

type releaseBrief struct {
	Service        string `json:"service" jsonschema:"description=The service covered by the release brief"`
	Summary        string `json:"summary" jsonschema:"description=The approved release summary"`
	PublishChannel string `json:"publish_channel" jsonschema:"description=Where the brief was published"`
	NextStep       string `json:"next_step" jsonschema:"description=What the operator should do next"`
}

type collectFactsParams struct {
	Service string `json:"service" jsonschema:"description=The service to inspect"`
}

type publishBriefParams struct {
	Channel string `json:"channel" jsonschema:"description=The destination channel"`
	Brief   string `json:"brief" jsonschema:"description=The release brief to publish"`
}

func main() {
	address := getenv("TEMPORAL_ADDRESS", "localhost:7233")
	namespace := getenv("TEMPORAL_NAMESPACE", "default")
	uiURL := getenv("TEMPORAL_UI_URL", "http://localhost:8080/namespaces/default/workflows")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	c, err := client.Dial(client.Options{
		HostPort:  address,
		Namespace: namespace,
	})
	if err != nil {
		log.Fatalf("dial Temporal at %s (%s): %v", address, namespace, err)
	}
	defer c.Close()

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	taskQueue := "gollem-release-brief-" + suffix
	workflowID := "gollem-release-brief-" + suffix

	agent := buildReleaseAgent()
	ta := temporal.NewTemporalAgent(agent,
		temporal.WithName("release-brief-demo-"+suffix),
		temporal.WithVersion("2026_03"),
		temporal.WithContinueAsNew(temporal.ContinueAsNewConfig{
			MaxTurns:         25,
			MaxHistoryLength: 10000,
			OnSuggested:      true,
		}),
		temporal.WithActivityConfig(temporal.ActivityConfig{
			StartToCloseTimeout: 60 * time.Second,
			MaxRetries:          2,
			InitialInterval:     time.Second,
		}),
	)

	w := worker.New(c, taskQueue, worker.Options{})
	if err := ta.Register(w); err != nil {
		log.Fatalf("register Temporal worker: %v", err)
	}
	if err := w.Start(); err != nil {
		log.Fatalf("start Temporal worker: %v", err)
	}
	defer w.Stop()

	fmt.Printf("Temporal address: %s\n", address)
	fmt.Printf("Namespace: %s\n", namespace)
	fmt.Printf("Task queue: %s\n", taskQueue)
	fmt.Printf("Workflow name: %s\n", ta.WorkflowName())
	fmt.Printf("Workflow URL: %s/%s\n\n", uiURL, workflowID)

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: taskQueue,
	}, ta.WorkflowName(), temporal.WorkflowInput{
		Prompt: "Prepare and publish a release brief for billing-api.",
	})
	if err != nil {
		log.Fatalf("execute workflow: %v", err)
	}

	status, err := waitForApprovalState(ctx, c, workflowID, ta)
	if err != nil {
		log.Fatalf("wait for approval state: %v", err)
	}

	fmt.Println("Workflow is waiting for approval.")
	fmt.Printf("Run step: %d\n", status.RunStep)
	fmt.Printf("Waiting reason: %s\n", status.WaitingReason)
	fmt.Printf("Pending approvals: %d\n", len(status.PendingApprovals))
	if len(status.PendingApprovals) > 0 {
		req := status.PendingApprovals[0]
		fmt.Printf("Tool awaiting approval: %s\n", req.ToolName)
		fmt.Printf("Tool call ID: %s\n", req.ToolCallID)
		fmt.Printf("Tool args: %s\n\n", req.ArgsJSON)

		if err := c.SignalWorkflow(ctx, workflowID, "", ta.ApprovalSignalName(), temporal.ApprovalSignal{
			ToolName:   req.ToolName,
			ToolCallID: req.ToolCallID,
			Approved:   true,
			Message:    "Approved automatically by the example runner.",
		}); err != nil {
			log.Fatalf("signal approval: %v", err)
		}
	}

	var output temporal.WorkflowOutput
	if err := run.Get(ctx, &output); err != nil {
		log.Fatalf("get workflow result: %v", err)
	}

	result, err := ta.DecodeWorkflowOutput(&output)
	if err != nil {
		log.Fatalf("decode workflow output: %v", err)
	}

	fmt.Println("Workflow completed.")
	fmt.Printf("Temporal workflow ID: %s\n", workflowID)
	fmt.Printf("Temporal run ID: %s\n", run.GetRunID())
	fmt.Printf("Gollem run ID: %s\n", result.RunID)
	fmt.Printf("Continue-as-new count: %d\n", output.ContinueAsNewCount)
	fmt.Printf("Requests: %d, Tool calls: %d\n", result.Usage.Requests, result.Usage.ToolCalls)
	fmt.Printf("Service: %s\n", result.Output.Service)
	fmt.Printf("Summary: %s\n", result.Output.Summary)
	fmt.Printf("Published to: %s\n", result.Output.PublishChannel)
	fmt.Printf("Next step: %s\n", result.Output.NextStep)
}

func buildReleaseAgent() *core.Agent[releaseBrief] {
	model := core.NewTestModel(
		core.ToolCallResponseWithID("collect_release_facts", `{"service":"billing-api"}`, "call_collect"),
		core.ToolCallResponseWithID("publish_release_brief", `{"channel":"release-ops","brief":"billing-api is healthy, all release checks are green, and the release brief is ready to publish."}`, "call_publish"),
		core.ToolCallResponse("final_result", `{
			"service":"billing-api",
			"summary":"Release brief published after approval with healthy checks and no open blockers.",
			"publish_channel":"release-ops",
			"next_step":"Share the approved brief with the deployment lead and start the rollout window."
		}`),
	)

	collectFacts := core.FuncTool[collectFactsParams](
		"collect_release_facts",
		"Collect release health signals for a service",
		func(_ context.Context, params collectFactsParams) (string, error) {
			return fmt.Sprintf(
				"service=%s; build=green; integration_tests=128/128; canary=healthy; incidents=0; open_blockers=none",
				params.Service,
			), nil
		},
	)

	publishBrief := core.FuncTool[publishBriefParams](
		"publish_release_brief",
		"Publish the release brief to the operator channel",
		func(_ context.Context, params publishBriefParams) (string, error) {
			return fmt.Sprintf("Published to #%s: %s", params.Channel, params.Brief), nil
		},
		core.WithRequiresApproval(),
	)

	return core.NewAgent[releaseBrief](model,
		core.WithSystemPrompt[releaseBrief](
			"You are a release coordinator. Gather release facts, prepare a brief, and publish it only after approval.",
		),
		core.WithTools[releaseBrief](collectFacts, publishBrief),
	)
}

func waitForApprovalState(ctx context.Context, c client.Client, workflowID string, ta *temporal.TemporalAgent[releaseBrief]) (temporal.WorkflowStatus, error) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		value, err := c.QueryWorkflow(ctx, workflowID, "", ta.StatusQueryName())
		if err == nil {
			var status temporal.WorkflowStatus
			if err := value.Get(&status); err == nil && status.Waiting {
				return status, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return temporal.WorkflowStatus{}, fmt.Errorf("workflow %s never entered a waiting state", workflowID)
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
