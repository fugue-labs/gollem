// Package strategy contains execution strategy definitions for the terminal agent.
// These are optimized by the autoeval harness.
package strategy

// TerminalStrategy defines the overall execution approach for terminal tasks.
type TerminalStrategy struct {
	// Name identifies this strategy.
	Name string `yaml:"name" json:"name"`
	// PlanFirst determines whether to generate a plan before execution.
	PlanFirst bool `yaml:"plan_first" json:"plan_first"`
	// VerifyAfter determines whether to verify results after execution.
	VerifyAfter bool `yaml:"verify_after" json:"verify_after"`
	// MaxAttempts is the total attempts per task.
	MaxAttempts int `yaml:"max_attempts" json:"max_attempts"`
}
