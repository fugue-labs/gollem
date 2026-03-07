package researcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/codetool"
	"github.com/fugue-labs/gollem/internal/eval"
	"github.com/fugue-labs/gollem/internal/git"
)

// WriteFileParams defines the input for the write_file tool.
type WriteFileParams struct {
	Path    string `json:"path" jsonschema:"description=File path relative to repo root. Must be in the subject directory."`
	Content string `json:"content" jsonschema:"description=Full file content to write"`
}

func writeFileTool(subjectDir string) core.Tool {
	// Resolve the subject directory to an absolute path once for safe comparison.
	absSubjectDir, err := filepath.Abs(subjectDir)
	if err != nil {
		absSubjectDir = subjectDir
	}

	return core.FuncTool[WriteFileParams](
		"write_file",
		"Write content to a file. ONLY files in the subject directory can be modified.",
		func(_ context.Context, p WriteFileParams) (string, error) {
			// Resolve the requested path to absolute and clean it to prevent
			// path traversal attacks (e.g., "autoeval-subject/../internal/eval/constants.go").
			absPath, pathErr := filepath.Abs(filepath.Clean(p.Path))
			if pathErr != nil {
				return "", fmt.Errorf("resolve path: %w", pathErr)
			}
			if !strings.HasPrefix(absPath, absSubjectDir+string(filepath.Separator)) && absPath != absSubjectDir {
				return "", fmt.Errorf("can only modify files in %s/, got: %s (resolved: %s)", subjectDir, p.Path, absPath)
			}
			// Use the cleaned absolute path for actual I/O to prevent TOCTOU issues.
			if mkdirErr := os.MkdirAll(filepath.Dir(absPath), 0o755); mkdirErr != nil {
				return "", fmt.Errorf("mkdir: %w", mkdirErr)
			}
			if writeErr := os.WriteFile(absPath, []byte(p.Content), 0o644); writeErr != nil {
				return "", fmt.Errorf("write: %w", writeErr)
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(p.Content), p.Path), nil
		},
	)
}

// RunEvalParams defines the input for the run_eval tool.
type RunEvalParams struct {
	Mode string `json:"mode" jsonschema:"description=Eval mode: fast (subset ~15min) or full (all tasks ~60min),enum=fast,full"`
}

func runEvalTool(cfg Config) core.Tool {
	return core.FuncTool[RunEvalParams](
		"run_eval",
		"Run Terminal Bench evaluation against the current subject/ agent configuration. Returns structured eval results as JSON.",
		func(ctx context.Context, p RunEvalParams) (string, error) {
			output, err := eval.Run(ctx, p.Mode, cfg)
			if err != nil {
				// Return partial results with the error so the agent can see what happened.
				if output != nil {
					data, marshalErr := json.MarshalIndent(output, "", "  ")
					if marshalErr != nil {
						return fmt.Sprintf("eval error: %v (could not marshal partial results)", err), nil
					}
					return fmt.Sprintf("eval error: %v\npartial results: %s", err, string(data)), nil
				}
				return "", fmt.Errorf("eval failed: %w", err)
			}
			data, marshalErr := json.MarshalIndent(output, "", "  ")
			if marshalErr != nil {
				return "", fmt.Errorf("marshal eval results: %w", marshalErr)
			}
			return string(data), nil
		},
	)
}

// GitCommitParams defines the input for the git_commit tool.
type GitCommitParams struct {
	Message string `json:"message" jsonschema:"description=Commit message describing the experiment"`
}

func gitCommitTool() core.Tool {
	return core.FuncTool[GitCommitParams](
		"git_commit",
		"Commit all changes with a descriptive message. Returns the short commit hash.",
		func(_ context.Context, p GitCommitParams) (string, error) {
			hash, err := git.CommitAll(p.Message)
			if err != nil {
				return "", err
			}
			return "committed: " + hash, nil
		},
	)
}

// GitResetParams defines the input for the git_reset tool.
type GitResetParams struct {
	Commit string `json:"commit" jsonschema:"description=Commit hash to reset to (7 chars)"`
}

func gitResetTool() core.Tool {
	return core.FuncTool[GitResetParams](
		"git_reset",
		"Hard reset to a previous commit. Use when an experiment didn't improve pass_rate.",
		func(_ context.Context, p GitResetParams) (string, error) {
			return git.ResetHard(p.Commit)
		},
	)
}

// GitLogParams defines the input for the git_log tool.
type GitLogParams struct {
	Count int `json:"count" jsonschema:"description=Number of log entries to show (default 20)"`
}

func gitLogTool() core.Tool {
	return core.FuncTool[GitLogParams](
		"git_log",
		"Show recent git log entries (oneline format). Use to review experiment history.",
		func(_ context.Context, p GitLogParams) (string, error) {
			n := p.Count
			if n <= 0 {
				n = 20
			}
			return git.Log(n)
		},
	)
}

// ReadTracesParams defines the input for the read_traces tool.
type ReadTracesParams struct {
	TaskID string `json:"task_id" jsonschema:"description=Terminal Bench task ID to read traces for"`
}

func readTracesTool(tracesDir string) core.Tool {
	return core.FuncTool[ReadTracesParams](
		"read_traces",
		"Read the execution trace for a specific failed task. Shows all tool calls, model responses, and errors. Use this to understand WHY a task failed.",
		func(_ context.Context, p ReadTracesParams) (string, error) {
			// Look for trace files matching the task ID.
			pattern := filepath.Join(tracesDir, "*"+p.TaskID+"*")
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return "", fmt.Errorf("glob traces: %w", err)
			}
			if len(matches) == 0 {
				return fmt.Sprintf("no traces found for task %s in %s", p.TaskID, tracesDir), nil
			}

			var sb strings.Builder
			for _, match := range matches {
				data, readErr := os.ReadFile(match)
				if readErr != nil {
					fmt.Fprintf(&sb, "error reading %s: %v\n", match, readErr)
					continue
				}
				fmt.Fprintf(&sb, "=== %s ===\n%s\n\n", filepath.Base(match), string(data))
			}
			return sb.String(), nil
		},
	)
}

func readResultsTool(resultsFile string) core.Tool {
	return core.FuncTool[struct{}](
		"read_results",
		"Read the full results.tsv experiment history. Use to see what's been tried and what worked.",
		func(_ context.Context, _ struct{}) (string, error) {
			data, err := os.ReadFile(resultsFile)
			if err != nil {
				return "no results yet", nil
			}
			return string(data), nil
		},
	)
}

// AppendResultParams defines the input for the append_result tool.
type AppendResultParams struct {
	Line string `json:"line" jsonschema:"description=Tab-separated line to append to results.tsv"`
}

func appendResultTool(resultsFile string) core.Tool {
	return core.FuncTool[AppendResultParams](
		"append_result",
		"Append a line to the results.tsv experiment log. Format: experiment_num\\tcommit\\tpass_rate\\tstatus\\tdescription",
		func(_ context.Context, p AppendResultParams) (string, error) {
			f, err := os.OpenFile(resultsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return "", fmt.Errorf("open results file: %w", err)
			}
			defer f.Close()

			line := p.Line
			if !strings.HasSuffix(line, "\n") {
				line += "\n"
			}
			if _, writeErr := f.WriteString(line); writeErr != nil {
				return "", fmt.Errorf("write result: %w", writeErr)
			}
			return "appended to " + resultsFile, nil
		},
	)
}

// BuildTools creates all tools for the researcher agent.
// General-purpose file/shell tools come from ext/codetool; domain-specific tools
// (eval, git, traces, results) are defined in this package.
func BuildTools(cfg Config) []core.Tool {
	return []core.Tool{
		// General-purpose tools from ext/codetool.
		codetool.View(),
		codetool.Bash(),
		codetool.Ls(),
		codetool.Grep(),
		codetool.Glob(),
		codetool.Edit(),

		// Scoped write — only allows writes inside the subject directory.
		writeFileTool(cfg.SubjectDir),

		// Domain-specific tools.
		runEvalTool(cfg),
		gitCommitTool(),
		gitResetTool(),
		gitLogTool(),
		readTracesTool(cfg.TracesDir),
		readResultsTool(cfg.ResultsFile),
		appendResultTool(cfg.ResultsFile),
	}
}
