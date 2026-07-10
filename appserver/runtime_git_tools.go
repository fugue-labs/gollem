package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	toolgit "github.com/fugue-labs/gollem/appserver/tools/git"
	"github.com/fugue-labs/gollem/core"
)

const (
	runtimeGitToolNamespace     = "git"
	runtimeGitResultMaxBytes    = 64 * 1024
	runtimeGitStatusRawMaxBytes = 32 * 1024
	runtimeGitMetadataMaxBytes  = 1024
	runtimeGitMaxEntries        = 32
	runtimeGitMaxPathspecs      = 256
	runtimeGitMaxParameterBytes = 16 * 1024
	runtimeGitMaxWorktrees      = 16
	runtimeGitTruncationMarker  = "\n[git output truncated]\n"
)

type runtimeGitStatusParams struct{}

type runtimeGitStatusEntry struct {
	Code      string `json:"code"`
	Path      string `json:"path"`
	Raw       string `json:"raw"`
	Truncated bool   `json:"truncated,omitempty"`
}

type runtimeGitStatusResult struct {
	BranchLine       string                  `json:"branchLine"`
	Entries          []runtimeGitStatusEntry `json:"entries"`
	Clean            bool                    `json:"clean"`
	Raw              string                  `json:"raw,omitempty"`
	RawTruncated     bool                    `json:"rawTruncated,omitempty"`
	RawBytes         int                     `json:"rawBytes"`
	RawSHA256        string                  `json:"rawSha256"`
	EntryCount       int                     `json:"entryCount"`
	EntriesTruncated bool                    `json:"entriesTruncated,omitempty"`
}

type runtimeGitDiffParams struct {
	Ref       string   `json:"ref,omitempty" jsonschema:"description=Optional revision or range passed to git diff"`
	Pathspecs []string `json:"pathspecs,omitempty" jsonschema:"description=Repository-relative paths to include"`
	Cached    bool     `json:"cached,omitempty" jsonschema:"description=Inspect staged changes instead of unstaged changes"`
}

type runtimeGitDiffResult struct {
	Patch     string `json:"patch,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
}

type runtimeGitCommitParams struct {
	Message    string   `json:"message" jsonschema:"description=Commit message"`
	All        bool     `json:"all,omitempty" jsonschema:"description=Stage all repository changes before committing"`
	Pathspecs  []string `json:"pathspecs,omitempty" jsonschema:"description=Repository-relative paths to stage before committing"`
	AllowEmpty bool     `json:"allowEmpty,omitempty" jsonschema:"description=Allow a commit with no staged changes"`
}

type runtimeGitCommitResult struct {
	Hash            string `json:"hash"`
	Summary         string `json:"summary"`
	Output          string `json:"output,omitempty"`
	OutputTruncated bool   `json:"outputTruncated,omitempty"`
	OutputBytes     int    `json:"outputBytes"`
	OutputSHA256    string `json:"outputSha256"`
}

type runtimeGitWorktreeResult struct {
	Path              string `json:"path"`
	Head              string `json:"head,omitempty"`
	Branch            string `json:"branch,omitempty"`
	Detached          bool   `json:"detached,omitempty"`
	Bare              bool   `json:"bare,omitempty"`
	MetadataTruncated bool   `json:"metadataTruncated,omitempty"`
}

type runtimeGitWorktreeListResult struct {
	Worktrees []runtimeGitWorktreeResult `json:"worktrees"`
	Total     int                        `json:"total"`
	Truncated bool                       `json:"truncated,omitempty"`
}

type runtimeGitWorktreeCreateParams struct {
	Path   string `json:"path" jsonschema:"description=Path under the configured worktree root"`
	Branch string `json:"branch,omitempty" jsonschema:"description=Optional new branch name"`
	Base   string `json:"base,omitempty" jsonschema:"description=Optional base revision"`
	Force  bool   `json:"force,omitempty" jsonschema:"description=Permit Git's force worktree-add behavior"`
}

// GitRuntimeTools adapts the repository-scoped Git service into
// provider-neutral model tools. Read operations are bounded; commit and
// worktree creation retain the service's approval, audit, and path guards.
func GitRuntimeTools(service *toolgit.Service) []core.Tool {
	if service == nil {
		return nil
	}
	tools := []core.Tool{
		core.FuncTool[runtimeGitStatusParams](
			"git_status",
			"Read bounded repository status from the configured Git root.",
			func(ctx context.Context, _ runtimeGitStatusParams) (runtimeGitStatusResult, error) {
				status, err := service.Status(ctx)
				if err != nil {
					return runtimeGitStatusResult{}, err
				}
				return newRuntimeGitStatusResult(status), nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(30*time.Second),
		),
		core.FuncTool[runtimeGitDiffParams](
			"git_diff",
			"Read a bounded repository diff for optional refs, staged state, and repository-relative pathspecs.",
			func(ctx context.Context, params runtimeGitDiffParams) (runtimeGitDiffResult, error) {
				if err := validateRuntimeGitParams(params.Ref, "", params.Pathspecs); err != nil {
					return runtimeGitDiffResult{}, err
				}
				diff, err := service.Diff(ctx, toolgit.DiffRequest{
					Ref:       strings.TrimSpace(params.Ref),
					Pathspecs: append([]string(nil), params.Pathspecs...),
					Cached:    params.Cached,
				})
				if err != nil {
					return runtimeGitDiffResult{}, err
				}
				text := boundedRuntimeGitText(diff.Patch)
				return runtimeGitDiffResult{Patch: text.Value, Truncated: text.Truncated, Bytes: text.Bytes, SHA256: text.SHA256}, nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(30*time.Second),
		),
		core.FuncTool[runtimeGitCommitParams](
			"git_commit",
			"Stage selected repository changes and create a commit through the configured Git mutation approval policy.",
			func(ctx context.Context, params runtimeGitCommitParams) (runtimeGitCommitResult, error) {
				message := strings.TrimSpace(params.Message)
				if message == "" {
					return runtimeGitCommitResult{}, errors.New("message is required")
				}
				if err := validateRuntimeGitParams("", message, params.Pathspecs); err != nil {
					return runtimeGitCommitResult{}, err
				}
				result, err := service.Commit(ctx, toolgit.CommitRequest{
					Message:    message,
					All:        params.All,
					Pathspecs:  append([]string(nil), params.Pathspecs...),
					AllowEmpty: params.AllowEmpty,
				})
				if err != nil {
					return runtimeGitCommitResult{}, err
				}
				output := boundedRuntimeGitText(result.Output)
				return runtimeGitCommitResult{
					Hash:            result.Hash,
					Summary:         result.Summary,
					Output:          output.Value,
					OutputTruncated: output.Truncated,
					OutputBytes:     output.Bytes,
					OutputSHA256:    output.SHA256,
				}, nil
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(2*time.Minute),
		),
		core.FuncTool[struct{}](
			"git_list_worktrees",
			"List bounded metadata for Git worktrees attached to the configured repository.",
			func(ctx context.Context, _ struct{}) (runtimeGitWorktreeListResult, error) {
				worktrees, err := service.WorktreeList(ctx)
				if err != nil {
					return runtimeGitWorktreeListResult{}, err
				}
				total := len(worktrees)
				if len(worktrees) > runtimeGitMaxWorktrees {
					worktrees = worktrees[:runtimeGitMaxWorktrees]
				}
				result := make([]runtimeGitWorktreeResult, 0, len(worktrees))
				for _, worktree := range worktrees {
					result = append(result, newRuntimeGitWorktreeResult(&worktree))
				}
				return runtimeGitWorktreeListResult{Worktrees: result, Total: total, Truncated: total > len(result)}, nil
			},
			core.WithToolConcurrencySafe(true),
			core.WithToolTimeout(30*time.Second),
		),
		core.FuncTool[runtimeGitWorktreeCreateParams](
			"git_create_worktree",
			"Create a Git worktree under the configured worktree root through the mutation approval policy.",
			func(ctx context.Context, params runtimeGitWorktreeCreateParams) (runtimeGitWorktreeResult, error) {
				if strings.TrimSpace(params.Path) == "" {
					return runtimeGitWorktreeResult{}, errors.New("path is required")
				}
				if err := validateRuntimeGitParams(params.Base, params.Path+params.Branch, nil); err != nil {
					return runtimeGitWorktreeResult{}, err
				}
				worktree, err := service.WorktreeCreate(ctx, toolgit.WorktreeCreateRequest{
					Path:   params.Path,
					Branch: strings.TrimSpace(params.Branch),
					Base:   strings.TrimSpace(params.Base),
					Force:  params.Force,
				})
				if err != nil {
					return runtimeGitWorktreeResult{}, err
				}
				return newRuntimeGitWorktreeResult(worktree), nil
			},
			core.WithToolSequential(true),
			core.WithToolTimeout(2*time.Minute),
		),
	}
	for i := range tools {
		tools[i].Definition.Namespace = runtimeGitToolNamespace
	}
	return tools
}

type runtimeGitBoundedText struct {
	Value     string
	Truncated bool
	Bytes     int
	SHA256    string
}

func boundedRuntimeGitText(value string) runtimeGitBoundedText {
	return boundedRuntimeGitTextLimit(value, runtimeGitResultMaxBytes)
}

func boundedRuntimeGitTextLimit(value string, limit int) runtimeGitBoundedText {
	valid := strings.ToValidUTF8(value, "\uFFFD")
	result := runtimeGitBoundedText{Bytes: len(value), SHA256: runtimeSHA256([]byte(value))}
	if len(valid) <= limit {
		result.Value = valid
		return result
	}
	result.Truncated = true
	available := limit - len(runtimeGitTruncationMarker)
	if available < 0 {
		available = 0
	}
	prefixBytes := available / 2
	suffixBytes := available - prefixBytes
	prefix := validRuntimeUTF8Prefix(valid, prefixBytes)
	suffixStart := len(valid) - suffixBytes
	for suffixStart < len(valid) && suffixStart > 0 && !utf8StartByte(valid[suffixStart]) {
		suffixStart++
	}
	result.Value = prefix + runtimeGitTruncationMarker + valid[suffixStart:]
	return result
}

func utf8StartByte(value byte) bool {
	return value&0xC0 != 0x80
}

func newRuntimeGitStatusResult(status *toolgit.Status) runtimeGitStatusResult {
	if status == nil {
		return runtimeGitStatusResult{Clean: true, RawSHA256: runtimeSHA256(nil)}
	}
	raw := boundedRuntimeGitTextLimit(status.Raw, runtimeGitStatusRawMaxBytes)
	branch := boundedRuntimeGitTextLimit(status.BranchLine, runtimeGitMetadataMaxBytes)
	entries := status.Entries
	if len(entries) > runtimeGitMaxEntries {
		entries = entries[:runtimeGitMaxEntries]
	}
	resultEntries := make([]runtimeGitStatusEntry, 0, len(entries))
	for _, entry := range entries {
		path := boundedRuntimeGitTextLimit(entry.Path, runtimeGitMetadataMaxBytes)
		rawEntry := boundedRuntimeGitTextLimit(entry.Raw, runtimeGitMetadataMaxBytes)
		resultEntries = append(resultEntries, runtimeGitStatusEntry{Code: entry.Code, Path: path.Value, Raw: rawEntry.Value, Truncated: path.Truncated || rawEntry.Truncated})
	}
	return runtimeGitStatusResult{
		BranchLine:       branch.Value,
		Entries:          resultEntries,
		Clean:            status.Clean,
		Raw:              raw.Value,
		RawTruncated:     raw.Truncated,
		RawBytes:         raw.Bytes,
		RawSHA256:        raw.SHA256,
		EntryCount:       len(status.Entries),
		EntriesTruncated: len(status.Entries) > len(resultEntries),
	}
}

func newRuntimeGitWorktreeResult(worktree *toolgit.Worktree) runtimeGitWorktreeResult {
	if worktree == nil {
		return runtimeGitWorktreeResult{}
	}
	path := boundedRuntimeGitTextLimit(worktree.Path, runtimeGitMetadataMaxBytes)
	head := boundedRuntimeGitTextLimit(worktree.Head, runtimeGitMetadataMaxBytes)
	branch := boundedRuntimeGitTextLimit(worktree.Branch, runtimeGitMetadataMaxBytes)
	return runtimeGitWorktreeResult{
		Path:              path.Value,
		Head:              head.Value,
		Branch:            branch.Value,
		Detached:          worktree.Detached,
		Bare:              worktree.Bare,
		MetadataTruncated: path.Truncated || head.Truncated || branch.Truncated,
	}
}

func validateRuntimeGitParams(ref, text string, pathspecs []string) error {
	if len(pathspecs) > runtimeGitMaxPathspecs {
		return fmt.Errorf("pathspecs exceeds %d entries", runtimeGitMaxPathspecs)
	}
	total := len(ref) + len(text)
	for _, pathspec := range pathspecs {
		total += len(pathspec)
	}
	if total > runtimeGitMaxParameterBytes {
		return fmt.Errorf("git tool parameters exceed %d bytes", runtimeGitMaxParameterBytes)
	}
	return nil
}
