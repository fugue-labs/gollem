package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

const (
	runtimeUpdatePlanToolName      = "update_plan"
	runtimePlanItemKind            = "plan"
	runtimePlanUpdatedResult       = "Plan updated"
	runtimePlanMaxSteps            = 64
	runtimePlanMaxStepBytes        = 4 * 1024
	runtimePlanMaxExplanationBytes = 8 * 1024
	runtimePlanMaxArgsBytes        = 512 * 1024
	runtimePlanMaxJSONDepth        = 8
)

type runtimeUpdatePlanStep struct {
	Step   string `json:"step"`
	Status string `json:"status" jsonschema:"enum=pending|inProgress|completed"`
}

type runtimeUpdatePlanParams struct {
	Explanation *string                 `json:"explanation,omitempty"`
	Plan        []runtimeUpdatePlanStep `json:"plan" jsonschema:"nonnullable=true"`
}

type runtimeToolDependencies struct {
	store    store.Store
	notifier runtimeNotifier
	turn     *store.Turn
}

type runtimeToolDependenciesContextKey struct{}

func withRuntimeToolDependencies(ctx context.Context, deps runtimeToolDependencies) context.Context {
	return context.WithValue(ctx, runtimeToolDependenciesContextKey{}, deps)
}

func runtimeDependencies(ctx context.Context, rc *core.RunContext) (runtimeToolDependencies, bool) {
	if ctx != nil {
		if deps, ok := ctx.Value(runtimeToolDependenciesContextKey{}).(runtimeToolDependencies); ok {
			return deps, runtimeDependenciesReady(deps)
		}
	}
	if rc != nil {
		if deps, ok := rc.Deps.(runtimeToolDependencies); ok {
			return deps, runtimeDependenciesReady(deps)
		}
	}
	return runtimeToolDependencies{}, false
}

func runtimeDependenciesReady(deps runtimeToolDependencies) bool {
	return deps.store != nil && deps.notifier != nil && deps.turn != nil &&
		deps.turn.ThreadID != "" && deps.turn.ID != ""
}

// PlanRuntimeTools exposes the exact main-owned plan snapshot producer.
func PlanRuntimeTools() []core.Tool {
	tool := core.FuncTool[runtimeUpdatePlanParams](
		runtimeUpdatePlanToolName,
		"Update the visible plan for the current turn. Provide the complete ordered plan on every call.",
		func(ctx context.Context, rc *core.RunContext, params runtimeUpdatePlanParams) (string, error) {
			return executeRuntimePlanUpdate(ctx, rc, params)
		},
		core.WithToolSequential(true),
		core.WithToolStrict(true),
	)
	closeRuntimePlanToolSchema(tool.Definition.ParametersSchema)
	generatedHandler := tool.Handler
	tool.Handler = func(ctx context.Context, rc *core.RunContext, argsJSON string) (any, error) {
		if _, err := decodeRuntimeUpdatePlanParams(argsJSON); err != nil {
			return nil, err
		}
		return generatedHandler(ctx, rc, argsJSON)
	}
	return []core.Tool{tool}
}

func closeRuntimePlanToolSchema(schema core.Schema) {
	schema["additionalProperties"] = false
	properties, _ := schema["properties"].(core.Schema)
	plan, _ := properties["plan"].(core.Schema)
	items, _ := plan["items"].(core.Schema)
	if items != nil {
		items["additionalProperties"] = false
	}
}

func decodeRuntimeUpdatePlanParams(argsJSON string) (runtimeUpdatePlanParams, error) {
	if strings.TrimSpace(argsJSON) == "" {
		return runtimeUpdatePlanParams{}, errors.New("update_plan requires a JSON object")
	}
	if len(argsJSON) > runtimePlanMaxArgsBytes {
		return runtimeUpdatePlanParams{}, fmt.Errorf("update_plan arguments exceed %d bytes", runtimePlanMaxArgsBytes)
	}
	if err := rejectRuntimePlanDuplicateFields(argsJSON); err != nil {
		return runtimeUpdatePlanParams{}, err
	}
	decoder := json.NewDecoder(bytes.NewBufferString(argsJSON))
	decoder.DisallowUnknownFields()
	var params runtimeUpdatePlanParams
	if err := decoder.Decode(&params); err != nil {
		return runtimeUpdatePlanParams{}, fmt.Errorf("decode update_plan arguments: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return runtimeUpdatePlanParams{}, errors.New("decode update_plan arguments: trailing JSON value")
		}
		return runtimeUpdatePlanParams{}, fmt.Errorf("decode update_plan arguments: %w", err)
	}
	if params.Plan == nil {
		return runtimeUpdatePlanParams{}, errors.New("update_plan plan is required and cannot be null")
	}
	return params, nil
}

func rejectRuntimePlanDuplicateFields(argsJSON string) error {
	decoder := json.NewDecoder(bytes.NewBufferString(argsJSON))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > runtimePlanMaxJSONDepth {
			return fmt.Errorf("JSON nesting exceeds %d levels", runtimePlanMaxJSONDepth)
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("object field name is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate field %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delim)
		}
	}
	if err := walk(0); err != nil {
		return fmt.Errorf("decode update_plan arguments: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode update_plan arguments: trailing JSON value")
		}
		return fmt.Errorf("decode update_plan arguments: %w", err)
	}
	return nil
}

func executeRuntimePlanUpdate(ctx context.Context, rc *core.RunContext, params runtimeUpdatePlanParams) (string, error) {
	deps, ok := runtimeDependencies(ctx, rc)
	if !ok {
		return "", errors.New("update_plan requires an active runtime turn")
	}
	notification, err := normalizeRuntimePlanUpdate(deps.turn, params)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		return "", fmt.Errorf("encode update_plan snapshot: %w", err)
	}
	items, err := deps.store.ListItems(ctx, store.ItemFilter{
		ThreadID: deps.turn.ThreadID,
		TurnID:   deps.turn.ID,
	})
	if err != nil {
		return "", fmt.Errorf("read durable update_plan snapshot: %w", err)
	}
	status := runtimePlanItemStatus(notification.Plan)
	var existing *store.Item
	for i := len(items) - 1; i >= 0; i-- {
		if items[i] != nil && items[i].Kind == runtimePlanItemKind {
			existing = items[i]
			break
		}
	}
	if existing == nil {
		_, err = deps.store.AppendItem(ctx, store.AppendItemRequest{
			ThreadID: deps.turn.ThreadID,
			TurnID:   deps.turn.ID,
			Kind:     runtimePlanItemKind,
			Status:   status,
			Payload:  payload,
		})
	} else {
		_, err = deps.store.UpdateItem(ctx, store.UpdateItemRequest{
			ID:      existing.ID,
			Status:  status,
			Payload: payload,
		})
	}
	if err != nil {
		return "", fmt.Errorf("persist update_plan snapshot: %w", err)
	}
	deps.notifier.PublishNotification("turn/plan/updated", notification)
	return runtimePlanUpdatedResult, nil
}

func normalizeRuntimePlanUpdate(turn *store.Turn, params runtimeUpdatePlanParams) (protocol.TurnPlanUpdatedNotification, error) {
	if turn == nil || turn.ThreadID == "" || turn.ID == "" {
		return protocol.TurnPlanUpdatedNotification{}, errors.New("update_plan requires an active runtime turn")
	}
	if params.Plan == nil {
		return protocol.TurnPlanUpdatedNotification{}, errors.New("update_plan plan is required and cannot be null")
	}
	if len(params.Plan) > runtimePlanMaxSteps {
		return protocol.TurnPlanUpdatedNotification{}, fmt.Errorf("update_plan plan exceeds %d steps", runtimePlanMaxSteps)
	}
	plan := make([]protocol.TurnPlanStep, len(params.Plan))
	for i, value := range params.Plan {
		step := strings.TrimSpace(value.Step)
		if step == "" {
			return protocol.TurnPlanUpdatedNotification{}, fmt.Errorf("update_plan plan[%d].step is required", i)
		}
		if len(step) > runtimePlanMaxStepBytes {
			return protocol.TurnPlanUpdatedNotification{}, fmt.Errorf("update_plan plan[%d].step exceeds %d bytes", i, runtimePlanMaxStepBytes)
		}
		status := protocol.TurnPlanStepStatus(value.Status)
		switch status {
		case protocol.TurnPlanStepStatusPending,
			protocol.TurnPlanStepStatusInProgress,
			protocol.TurnPlanStepStatusCompleted:
		default:
			return protocol.TurnPlanUpdatedNotification{}, fmt.Errorf("update_plan plan[%d].status is invalid", i)
		}
		plan[i] = protocol.TurnPlanStep{Step: step, Status: status}
	}
	var explanation *string
	if params.Explanation != nil {
		value := strings.TrimSpace(*params.Explanation)
		if len(value) > runtimePlanMaxExplanationBytes {
			return protocol.TurnPlanUpdatedNotification{}, fmt.Errorf("update_plan explanation exceeds %d bytes", runtimePlanMaxExplanationBytes)
		}
		explanation = &value
	}
	return protocol.TurnPlanUpdatedNotification{
		ThreadID:    turn.ThreadID,
		TurnID:      turn.ID,
		Explanation: explanation,
		Plan:        plan,
	}, nil
}

func runtimePlanItemStatus(plan []protocol.TurnPlanStep) string {
	for _, step := range plan {
		if step.Status != protocol.TurnPlanStepStatusCompleted {
			return protocol.ItemStatusInProgress
		}
	}
	return protocol.ItemStatusCompleted
}
