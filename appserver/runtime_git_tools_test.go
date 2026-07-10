package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/appserver/store"
	toolgit "github.com/fugue-labs/gollem/appserver/tools/git"
	"github.com/fugue-labs/gollem/core"
)

func TestGitRuntimeToolsExposeScopedOperationsAndBoundResults(t *testing.T) {
	repo := initRepo(t)
	gitSvc, err := toolgit.NewService(repo, toolgit.WithWorktreeRoot(filepath.Join(t.TempDir(), "worktrees")))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	tools := GitRuntimeTools(gitSvc)
	want := map[string]struct {
		sequential      bool
		concurrencySafe bool
	}{
		"git_status":          {concurrencySafe: true},
		"git_diff":            {concurrencySafe: true},
		"git_commit":          {sequential: true},
		"git_list_worktrees":  {concurrencySafe: true},
		"git_create_worktree": {sequential: true},
	}
	if len(tools) != len(want) {
		t.Fatalf("runtime git tools = %d, want %d", len(tools), len(want))
	}
	for _, tool := range tools {
		expected, ok := want[tool.Definition.Name]
		if !ok {
			t.Fatalf("unexpected runtime git tool %q", tool.Definition.Name)
		}
		if tool.Definition.Namespace != runtimeGitToolNamespace {
			t.Fatalf("tool %q namespace = %q", tool.Definition.Name, tool.Definition.Namespace)
		}
		if tool.Definition.Sequential != expected.sequential || tool.Definition.ConcurrencySafe != expected.concurrencySafe {
			t.Fatalf("tool %q flags sequential=%v concurrencySafe=%v", tool.Definition.Name, tool.Definition.Sequential, tool.Definition.ConcurrencySafe)
		}
	}
	if GitRuntimeTools(nil) != nil {
		t.Fatal("nil git service should expose no tools")
	}

	large := strings.Repeat("changed line\n", runtimeGitResultMaxBytes)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(large), 0o644); err != nil {
		t.Fatalf("write large diff fixture: %v", err)
	}
	diffTool := findRuntimeToolByName(t, tools, "git_diff")
	result, err := diffTool.Handler(context.Background(), &core.RunContext{}, `{}`)
	if err != nil {
		t.Fatalf("git_diff: %v", err)
	}
	diff, ok := result.(runtimeGitDiffResult)
	if !ok || !diff.Truncated || len(diff.Patch) > runtimeGitResultMaxBytes || diff.SHA256 == "" || diff.Bytes <= runtimeGitResultMaxBytes {
		t.Fatalf("bounded git diff = %#v", result)
	}

	createTool := findRuntimeToolByName(t, tools, "git_create_worktree")
	_, err = createTool.Handler(context.Background(), &core.RunContext{}, `{"path":"../escape","branch":"escape","base":"HEAD"}`)
	if !errors.Is(err, toolgit.ErrPathOutsideRoot) {
		t.Fatalf("worktree escape error = %v, want %v", err, toolgit.ErrPathOutsideRoot)
	}
}

func TestServerRuntimeApprovedGitCommitUsesScopedService(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("runtime change\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	gitSvc, err := toolgit.NewService(repo, toolgit.WithApproval(approvals.GitApproval))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"git_commit",
			`{"message":"runtime commit","all":true}`,
			"call-git-commit",
		),
		core.TextResponse("commit complete"),
	)
	server := readyServer(
		WithStore(st),
		WithGit(gitSvc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(GitRuntimeTools(gitSvc)...),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "commit the change"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	approvalRequest := waitForServerRequest(t, server)
	if approvalRequest.Method != "item/permissions/requestApproval" {
		t.Fatalf("approval method = %q", approvalRequest.Method)
	}
	var approvalParams permissionsApprovalParams
	if err := json.Unmarshal(approvalRequest.Params, &approvalParams); err != nil {
		t.Fatalf("decode git approval: %v", err)
	}
	if approvalParams.ThreadID != started.Thread.ID || approvalParams.TurnID != started.Turn.ID || approvalParams.ItemID != "call-git-commit" || approvalParams.Operation != string(toolgit.OperationCommit) {
		t.Fatalf("git approval correlation = %#v", approvalParams)
	}
	requestID, _ := approvalRequest.ID.Value().(string)
	if approval := server.HandleRequest(ctx, request("approval/respond", map[string]any{"requestId": requestID, "approved": true})); approval.Error != nil {
		t.Fatalf("approval/respond error: %v", approval.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	status, err := gitSvc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Clean {
		t.Fatalf("git status after runtime commit = %+v", status)
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	toolItem := findRuntimeToolItem(t, items, "git_commit", "message", "runtime commit")
	if toolItem.Status != runtimeToolStatusCompleted || toolItem.Payload.Namespace == nil || *toolItem.Payload.Namespace != runtimeGitToolNamespace {
		t.Fatalf("git tool item = %#v", toolItem)
	}
}

func TestServerRuntimeDeniedGitCommitDoesNotMutateRepository(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	st := newRuntimeTestStore(t)
	approvals := NewApprovalService()
	gitSvc, err := toolgit.NewService(repo, toolgit.WithApproval(approvals.GitApproval))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	model := core.NewTestModel(
		core.ToolCallResponseWithID("git_commit", `{"message":"denied commit","all":true}`, "call-denied-git"),
		core.TextResponse("commit was denied"),
	)
	server := readyServer(
		WithStore(st),
		WithGit(gitSvc),
		WithApprovalService(approvals),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(GitRuntimeTools(gitSvc)...),
		)),
	)
	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "try a denied commit"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	approvalRequest := waitForServerRequest(t, server)
	requestID, _ := approvalRequest.ID.Value().(string)
	if approval := server.HandleRequest(ctx, request("approval/respond", map[string]any{
		"requestId": requestID,
		"approved":  false,
		"message":   "denied by test",
	})); approval.Error != nil {
		t.Fatalf("approval/respond error: %v", approval.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
	status, err := gitSvc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Clean || len(status.Entries) == 0 {
		t.Fatalf("denied commit unexpectedly changed status: %+v", status)
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	toolItem := findRuntimeToolItem(t, items, "git_commit", "message", "denied commit")
	if toolItem.Status != runtimeToolStatusFailed {
		t.Fatalf("denied git tool status = %q", toolItem.Status)
	}
}
