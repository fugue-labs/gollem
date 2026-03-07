// Package tools contains tool implementations for the terminal agent.
// These are the tools that the autoeval harness optimizes.
package tools

// BashToolConfig holds configuration for the bash execution tool.
type BashToolConfig struct {
	// Timeout is the maximum execution time per command in seconds.
	Timeout int `yaml:"timeout" json:"timeout"`
	// WorkDir is the working directory for command execution.
	WorkDir string `yaml:"work_dir" json:"work_dir"`
}
