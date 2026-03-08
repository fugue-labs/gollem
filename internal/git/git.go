// Package git provides git operations for the autoeval harness.
package git

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// CommitAll stages all changes and creates a commit. Returns the short hash.
func CommitAll(ctx context.Context, message string) (string, error) {
	if err := run(ctx, "git", "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}

	if err := run(ctx, "git", "commit", "-m", message); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}

	out, err := output(ctx, "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// ResetHard performs a hard reset to the given commit.
func ResetHard(ctx context.Context, commit string) (string, error) {
	if err := run(ctx, "git", "reset", "--hard", commit); err != nil {
		return "", fmt.Errorf("git reset: %w", err)
	}
	return "reset to " + commit, nil
}

// Log returns the last n commit log entries (oneline format).
func Log(ctx context.Context, n int) (string, error) {
	out, err := output(ctx, "git", "log", "--oneline", "-"+strconv.Itoa(n))
	if err != nil {
		return "", fmt.Errorf("git log: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// CurrentHash returns the short hash of HEAD.
func CurrentHash(ctx context.Context) (string, error) {
	out, err := output(ctx, "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
