package middleware

// PlanningConfig configures the planning step before task execution.
type PlanningConfig struct {
	// Enabled controls whether a planning step is injected.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// MaxPlanSteps limits plan complexity.
	MaxPlanSteps int `yaml:"max_plan_steps" json:"max_plan_steps"`
}
