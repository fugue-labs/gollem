package bench

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Output is parsed from Terminal Bench results.
type Output struct {
	PassRate     float64  `json:"pass_rate"`
	TasksPassed  int      `json:"tasks_passed"`
	TasksTotal   int      `json:"tasks_total"`
	TasksFailed  []string `json:"tasks_failed"`
	DurationSecs float64  `json:"duration_secs"`
	TokensUsed   int      `json:"tokens_used"`
}

// Run executes a Terminal Bench evaluation and returns parsed results.
// evalCommand is a shell command with {mode} placeholder. resultsPath
// is where the eval writes its JSON results. mode is "fast" or "full".
func Run(ctx context.Context, mode, evalCommand, resultsPath string) (*Output, error) {
	cmd := strings.ReplaceAll(evalCommand, "{mode}", mode)

	start := time.Now()

	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	out, err := c.CombinedOutput()
	elapsed := time.Since(start)

	if err != nil {
		return &Output{
			PassRate:     0,
			TasksPassed:  0,
			TasksTotal:   taskCountForMode(mode),
			DurationSecs: elapsed.Seconds(),
		}, fmt.Errorf("eval command failed: %w\noutput: %s", err, string(out))
	}

	result, parseErr := ParseResults(resultsPath)
	if parseErr != nil {
		return &Output{
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
