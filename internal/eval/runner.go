package eval

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/internal/researcher"
)

// Run executes a Terminal Bench evaluation and returns parsed results.
// mode is "fast" (subset of tasks) or "full" (all tasks).
func Run(ctx context.Context, mode string, cfg researcher.Config) (*researcher.EvalOutput, error) {
	cmd := cfg.EvalCommand
	cmd = strings.ReplaceAll(cmd, "{mode}", mode)

	start := time.Now()

	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	out, err := c.CombinedOutput()
	elapsed := time.Since(start)

	if err != nil {
		return &researcher.EvalOutput{
			PassRate:     0,
			TasksPassed:  0,
			TasksTotal:   taskCountForMode(mode),
			DurationSecs: elapsed.Seconds(),
		}, fmt.Errorf("eval command failed: %w\noutput: %s", err, string(out))
	}

	result, parseErr := ParseResults(cfg.EvalResultsPath)
	if parseErr != nil {
		return &researcher.EvalOutput{
			PassRate:     0,
			TasksPassed:  0,
			TasksTotal:   taskCountForMode(mode),
			DurationSecs: elapsed.Seconds(),
		}, fmt.Errorf("failed to parse eval results: %w\neval output: %s", parseErr, string(out))
	}

	result.DurationSecs = elapsed.Seconds()
	return result, nil
}

func taskCountForMode(mode string) int {
	if mode == "full" {
		return FullTaskCount
	}
	return FastTaskCount
}
