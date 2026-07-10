package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceStatusAndDiff(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)
	svc := newTestService(t, repo)
	writeFile(t, repo, "tracked.txt", "two\n")
	writeFile(t, repo, "new.txt", "new\n")

	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Clean {
		t.Fatalf("status unexpectedly clean: %+v", status)
	}
	if !statusHas(status, " M", "tracked.txt") || !statusHas(status, "??", "new.txt") {
		t.Fatalf("status entries = %+v", status.Entries)
	}
	diff, err := svc.Diff(ctx, DiffRequest{Pathspecs: []string{"tracked.txt"}})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff.Patch, "-one") || !strings.Contains(diff.Patch, "+two") {
		t.Fatalf("diff = %s", diff.Patch)
	}
	if _, err := svc.Diff(ctx, DiffRequest{Pathspecs: []string{"../outside"}}); !errors.Is(err, ErrInvalidPathspec) {
		t.Fatalf("invalid pathspec error = %v, want ErrInvalidPathspec", err)
	}
	if _, err := svc.Diff(ctx, DiffRequest{Ref: "--output=outside.patch"}); !errors.Is(err, ErrInvalidRevision) {
		t.Fatalf("invalid revision error = %v, want ErrInvalidRevision", err)
	}
}

func TestServiceCommitApprovalAndAudit(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)
	var events []AuditEvent
	denyCommit := func(_ context.Context, op Operation) error {
		if op.Kind == OperationCommit {
			return errors.New("commit disabled")
		}
		return nil
	}
	denied := newTestService(t, repo, WithApproval(denyCommit), WithAuditSink(func(ev AuditEvent) {
		events = append(events, ev)
	}))
	writeFile(t, repo, "tracked.txt", "denied\n")
	if _, err := denied.Commit(ctx, CommitRequest{Message: "denied", All: true}); !errors.Is(err, ErrApprovalDenied) {
		t.Fatalf("denied Commit error = %v, want ErrApprovalDenied", err)
	}
	if len(events) != 1 || events[0].Allowed || events[0].Err == "" {
		t.Fatalf("audit events = %+v", events)
	}

	svc := newTestService(t, repo)
	result, err := svc.Commit(ctx, CommitRequest{Message: "update tracked", All: true})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Hash == "" || result.Summary != "update tracked" {
		t.Fatalf("commit result = %+v", result)
	}
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status after commit: %v", err)
	}
	if !status.Clean {
		t.Fatalf("status after commit = %+v", status)
	}
}

func TestServiceWorktreeCreateListAndScope(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	repo := newTestRepoIn(t, filepath.Join(tmp, "repo"))
	worktreeRoot := filepath.Join(tmp, "worktrees")
	svc := newTestService(t, repo, WithWorktreeRoot(worktreeRoot))

	created, err := svc.WorktreeCreate(ctx, WorktreeCreateRequest{
		Path:   "feature",
		Branch: "feature/test",
		Base:   "HEAD",
	})
	if err != nil {
		t.Fatalf("WorktreeCreate: %v", err)
	}
	if !samePath(created.Path, filepath.Join(worktreeRoot, "feature")) || created.Branch != "feature/test" {
		t.Fatalf("created worktree = %+v", created)
	}
	worktrees, err := svc.WorktreeList(ctx)
	if err != nil {
		t.Fatalf("WorktreeList: %v", err)
	}
	if !worktreeContains(worktrees, filepath.Join(worktreeRoot, "feature")) {
		t.Fatalf("worktrees = %+v", worktrees)
	}
	if _, err := svc.WorktreeCreate(ctx, WorktreeCreateRequest{Path: "../escape", Branch: "feature/escape", Base: "HEAD"}); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("outside worktree error = %v, want ErrPathOutsideRoot", err)
	}
	if _, err := svc.WorktreeCreate(ctx, WorktreeCreateRequest{Path: "invalid-base", Branch: "feature/invalid-base", Base: "--detach"}); !errors.Is(err, ErrInvalidRevision) {
		t.Fatalf("invalid worktree base error = %v, want ErrInvalidRevision", err)
	}
}

func TestServiceIgnoresAmbientGitHookEnv(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), ".git"))
	t.Setenv("GIT_WORK_TREE", filepath.Join(t.TempDir(), "elsewhere"))
	svc := newTestService(t, repo)
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status with ambient git env: %v", err)
	}
	if !status.Clean {
		t.Fatalf("status = %+v, want clean", status)
	}
}

func newTestService(t *testing.T, repo string, opts ...Option) *Service {
	t.Helper()
	svc, err := NewService(repo, opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	return newTestRepoIn(t, t.TempDir())
}

func newTestRepoIn(t *testing.T, repo string) string {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, repo, "tracked.txt", "one\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	if _, err := runGitOutput(context.Background(), repo, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func statusHas(status *Status, code, path string) bool {
	for _, entry := range status.Entries {
		if entry.Code == code && entry.Path == path {
			return true
		}
	}
	return false
}

func worktreeContains(worktrees []Worktree, path string) bool {
	for _, wt := range worktrees {
		if samePath(wt.Path, path) {
			return true
		}
	}
	return false
}
