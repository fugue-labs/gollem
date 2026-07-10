package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	ErrApprovalDenied  = errors.New("appserver/git: operation denied by approval policy")
	ErrPathOutsideRoot = errors.New("appserver/git: path escapes allowed root")
	ErrInvalidPathspec = errors.New("appserver/git: invalid pathspec")
	ErrInvalidRevision = errors.New("appserver/git: invalid revision")
	ErrInvalidMessage  = errors.New("appserver/git: commit message must not be empty")
)

type OperationKind string

const (
	OperationStatus         OperationKind = "status"
	OperationDiff           OperationKind = "diff"
	OperationCommit         OperationKind = "commit"
	OperationWorktreeList   OperationKind = "worktreeList"
	OperationWorktreeCreate OperationKind = "worktreeCreate"
)

type Operation struct {
	Kind      OperationKind
	Path      string
	Branch    string
	Base      string
	Message   string
	Pathspecs []string
	Mutating  bool
}

type AuditEvent struct {
	Operation Operation
	Allowed   bool
	Err       string
	At        time.Time
}

type ApprovalFunc func(context.Context, Operation) error
type AuditSink func(AuditEvent)
type Option func(*Service)

func WithApproval(fn ApprovalFunc) Option {
	return func(s *Service) {
		s.approve = fn
	}
}

func WithAuditSink(fn AuditSink) Option {
	return func(s *Service) {
		s.audit = fn
	}
}

func WithWorktreeRoot(root string) Option {
	return func(s *Service) {
		if root != "" {
			s.worktreeRoot = root
		}
	}
}

type Service struct {
	repoRoot     string
	repoRootEval string
	worktreeRoot string
	worktreeEval string
	approve      ApprovalFunc
	audit        AuditSink
}

type Status struct {
	BranchLine string
	Entries    []StatusEntry
	Clean      bool
	Raw        string
}

type StatusEntry struct {
	Code string
	Path string
	Raw  string
}

type DiffRequest struct {
	Ref       string
	Pathspecs []string
	Cached    bool
}

type Diff struct {
	Patch string
}

type CommitRequest struct {
	Message    string
	All        bool
	Pathspecs  []string
	AllowEmpty bool
}

type CommitResult struct {
	Hash    string
	Summary string
	Output  string
}

type Worktree struct {
	Path     string
	Head     string
	Branch   string
	Detached bool
	Bare     bool
}

type WorktreeCreateRequest struct {
	Path   string
	Branch string
	Base   string
	Force  bool
}

func NewService(repoRoot string, opts ...Option) (*Service, error) {
	if repoRoot == "" {
		return nil, errors.New("appserver/git: repo root must not be empty")
	}
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repo root: %w", err)
	}
	top, err := runGitOutput(context.Background(), abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("resolve git top-level: %w", err)
	}
	top = strings.TrimSpace(top)
	if top == "" {
		return nil, errors.New("appserver/git: git top-level was empty")
	}
	topAbs, err := filepath.Abs(top)
	if err != nil {
		return nil, fmt.Errorf("resolve git top-level path: %w", err)
	}
	topEval, err := filepath.EvalSymlinks(topAbs)
	if err != nil {
		return nil, fmt.Errorf("evaluate git top-level: %w", err)
	}
	s := &Service{
		repoRoot:     topAbs,
		repoRootEval: topEval,
		worktreeRoot: filepath.Dir(topAbs),
	}
	for _, opt := range opts {
		opt(s)
	}
	worktreeAbs, err := filepath.Abs(s.worktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree root: %w", err)
	}
	if err := os.MkdirAll(worktreeAbs, 0o755); err != nil {
		return nil, fmt.Errorf("create worktree root: %w", err)
	}
	worktreeEval, err := filepath.EvalSymlinks(worktreeAbs)
	if err != nil {
		return nil, fmt.Errorf("evaluate worktree root: %w", err)
	}
	s.worktreeRoot = worktreeAbs
	s.worktreeEval = worktreeEval
	return s, nil
}

func (s *Service) RepoRoot() string {
	if s == nil {
		return ""
	}
	return s.repoRoot
}

func (s *Service) WorktreeRoot() string {
	if s == nil {
		return ""
	}
	return s.worktreeRoot
}

func (s *Service) Status(ctx context.Context) (*Status, error) {
	op := Operation{Kind: OperationStatus}
	if err := checkContext(ctx); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	out, err := s.git(ctx, "status", "--porcelain=v1", "--branch")
	if err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	status := parseStatus(out)
	s.emit(op, true, nil)
	return status, nil
}

func (s *Service) Diff(ctx context.Context, req DiffRequest) (*Diff, error) {
	ref := strings.TrimSpace(req.Ref)
	op := Operation{Kind: OperationDiff, Base: ref, Pathspecs: cloneStrings(req.Pathspecs)}
	if err := checkContext(ctx); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	if err := validatePathspecs(req.Pathspecs); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	if err := validateRevision(ref); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	args := []string{"diff", "--no-ext-diff", "--no-color"}
	if req.Cached {
		args = append(args, "--cached")
	}
	if ref != "" {
		args = append(args, ref)
	}
	if len(req.Pathspecs) > 0 {
		args = append(args, "--")
		args = append(args, req.Pathspecs...)
	}
	out, err := s.git(ctx, args...)
	if err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	s.emit(op, true, nil)
	return &Diff{Patch: out}, nil
}

func (s *Service) Commit(ctx context.Context, req CommitRequest) (*CommitResult, error) {
	message := strings.TrimSpace(req.Message)
	op := Operation{
		Kind:      OperationCommit,
		Message:   message,
		Pathspecs: cloneStrings(req.Pathspecs),
		Mutating:  true,
	}
	if err := checkContext(ctx); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	if message == "" {
		s.emit(op, false, ErrInvalidMessage)
		return nil, ErrInvalidMessage
	}
	if err := validatePathspecs(req.Pathspecs); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	if err := s.requireApproval(ctx, op); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	if req.All || len(req.Pathspecs) > 0 {
		args := []string{"add"}
		if req.All {
			args = append(args, "-A")
		}
		args = append(args, "--")
		if len(req.Pathspecs) == 0 {
			args = append(args, ".")
		} else {
			args = append(args, req.Pathspecs...)
		}
		if _, err := s.git(ctx, args...); err != nil {
			s.emit(op, false, err)
			return nil, err
		}
	}
	args := []string{"commit", "-m", message}
	if req.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	out, err := s.git(ctx, args...)
	if err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	hash, err := s.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	summary, err := s.git(ctx, "log", "-1", "--pretty=%s")
	if err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	s.emit(op, true, nil)
	return &CommitResult{
		Hash:    strings.TrimSpace(hash),
		Summary: strings.TrimSpace(summary),
		Output:  out,
	}, nil
}

func (s *Service) WorktreeList(ctx context.Context) ([]Worktree, error) {
	op := Operation{Kind: OperationWorktreeList}
	if err := checkContext(ctx); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	out, err := s.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	worktrees := parseWorktrees(out)
	s.emit(op, true, nil)
	return worktrees, nil
}

func (s *Service) WorktreeCreate(ctx context.Context, req WorktreeCreateRequest) (*Worktree, error) {
	base := strings.TrimSpace(req.Base)
	op := Operation{
		Kind:     OperationWorktreeCreate,
		Path:     req.Path,
		Branch:   req.Branch,
		Base:     base,
		Mutating: true,
	}
	if err := checkContext(ctx); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	path, err := s.resolveWorktreePath(req.Path)
	if err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	if req.Branch != "" {
		if _, err := s.git(ctx, "check-ref-format", "--branch", req.Branch); err != nil {
			s.emit(op, false, err)
			return nil, fmt.Errorf("invalid branch name: %w", err)
		}
	}
	if err := validateRevision(base); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	if err := s.requireApproval(ctx, op); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	args := []string{"worktree", "add"}
	if req.Force {
		args = append(args, "--force")
	}
	if req.Branch != "" {
		args = append(args, "-b", req.Branch)
	}
	args = append(args, path)
	if base != "" {
		args = append(args, base)
	}
	if _, err := s.git(ctx, args...); err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	worktrees, err := s.WorktreeList(ctx)
	if err != nil {
		s.emit(op, false, err)
		return nil, err
	}
	for _, wt := range worktrees {
		if samePath(wt.Path, path) {
			s.emit(op, true, nil)
			copy := wt
			return &copy, nil
		}
	}
	s.emit(op, true, nil)
	return &Worktree{Path: path, Branch: req.Branch}, nil
}

func (s *Service) git(ctx context.Context, args ...string) (string, error) {
	return runGitOutput(ctx, s.repoRoot, args...)
}

func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv(os.Environ())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg != "" {
			return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return stdout.String(), fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

func cleanGitEnv(env []string) []string {
	blocked := map[string]struct{}{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
		"GIT_COMMON_DIR":                   {},
		"GIT_DIR":                          {},
		"GIT_INDEX_FILE":                   {},
		"GIT_NAMESPACE":                    {},
		"GIT_OBJECT_DIRECTORY":             {},
		"GIT_PREFIX":                       {},
		"GIT_QUARANTINE_PATH":              {},
		"GIT_WORK_TREE":                    {},
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := blocked[key]; ok {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (s *Service) resolveWorktreePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("appserver/git: worktree path must not be empty")
	}
	var candidate string
	if filepath.IsAbs(path) {
		candidate = filepath.Clean(path)
	} else {
		candidate = filepath.Join(s.worktreeRoot, path)
	}
	if err := ensureInside(s.worktreeRoot, candidate); err != nil {
		return "", err
	}
	eval, err := evalExistingOrNearestParent(candidate)
	if err != nil {
		return "", err
	}
	if err := ensureInside(s.worktreeEval, eval); err != nil {
		return "", err
	}
	if samePath(candidate, s.repoRoot) || samePath(eval, s.repoRootEval) {
		return "", errors.New("appserver/git: worktree path must not be the repository root")
	}
	return candidate, nil
}

func validatePathspecs(pathspecs []string) error {
	for _, path := range pathspecs {
		clean := filepath.Clean(path)
		if path == "" || filepath.IsAbs(path) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: %s", ErrInvalidPathspec, path)
		}
	}
	return nil
}

func validateRevision(revision string) error {
	if revision == "" {
		return nil
	}
	if strings.HasPrefix(revision, "-") || strings.ContainsAny(revision, "\x00\r\n") {
		return fmt.Errorf("%w: %s", ErrInvalidRevision, revision)
	}
	return nil
}

func ensureInside(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("relativize path: %w", err)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ErrPathOutsideRoot
	}
	return nil
}

func evalExistingOrNearestParent(path string) (string, error) {
	if eval, err := filepath.EvalSymlinks(path); err == nil {
		return eval, nil
	}
	var missing []string
	current := filepath.Clean(path)
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("evaluate existing ancestor: %w", os.ErrNotExist)
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		evalParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			eval := evalParent
			for _, part := range missing {
				eval = filepath.Join(eval, part)
			}
			return eval, nil
		}
		current = parent
	}
}

func (s *Service) requireApproval(ctx context.Context, op Operation) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if s.approve == nil {
		return nil
	}
	if err := s.approve(ctx, op); err != nil {
		return fmt.Errorf("%w: %w", ErrApprovalDenied, err)
	}
	return nil
}

func (s *Service) emit(op Operation, allowed bool, err error) {
	if s == nil || s.audit == nil {
		return
	}
	event := AuditEvent{
		Operation: op,
		Allowed:   allowed,
		At:        time.Now().UTC(),
	}
	if err != nil {
		event.Err = err.Error()
	}
	s.audit(event)
}

func parseStatus(out string) *Status {
	status := &Status{Raw: out}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			status.BranchLine = strings.TrimPrefix(line, "## ")
			continue
		}
		entry := StatusEntry{Raw: line}
		if len(line) >= 3 {
			entry.Code = line[:2]
			entry.Path = line[3:]
		} else {
			entry.Path = line
		}
		status.Entries = append(status.Entries, entry)
	}
	status.Clean = len(status.Entries) == 0
	return status
}

func parseWorktrees(out string) []Worktree {
	var worktrees []Worktree
	var current *Worktree
	flush := func() {
		if current != nil {
			worktrees = append(worktrees, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			key = line
		}
		switch key {
		case "worktree":
			flush()
			current = &Worktree{Path: value}
		case "HEAD":
			if current != nil {
				current.Head = value
			}
		case "branch":
			if current != nil {
				current.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "detached":
			if current != nil {
				current.Detached = true
			}
		case "bare":
			if current != nil {
				current.Bare = true
			}
		}
	}
	flush()
	sort.Slice(worktrees, func(i, j int) bool {
		return worktrees[i].Path < worktrees[j].Path
	})
	return worktrees
}

func samePath(a, b string) bool {
	left := filepath.Clean(a)
	if eval, err := filepath.EvalSymlinks(left); err == nil {
		left = eval
	}
	right := filepath.Clean(b)
	if eval, err := filepath.EvalSymlinks(right); err == nil {
		right = eval
	}
	return left == right
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}
