// Package git provides git operations for the autoeval harness.
package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// CommitAll stages all changes and creates a commit. Returns the short hash.
func CommitAll(message string) (string, error) {
	if err := run("git", "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}

	if err := run("git", "commit", "-m", message); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}

	out, err := output("git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// ResetHard performs a hard reset to the given commit.
func ResetHard(commit string) (string, error) {
	if err := run("git", "reset", "--hard", commit); err != nil {
		return "", fmt.Errorf("git reset: %w", err)
	}
	return fmt.Sprintf("reset to %s", commit), nil
}

// Log returns the last n commit log entries (oneline format).
func Log(n int) (string, error) {
	out, err := output("git", "log", "--oneline", fmt.Sprintf("-%d", n))
	if err != nil {
		return "", fmt.Errorf("git log: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// CurrentHash returns the short hash of HEAD.
func CurrentHash() (string, error) {
	out, err := output("git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
