package researcher

import "github.com/fugue-labs/gollem/internal/autoeval"

// Re-export shared types for convenience within this package.
type ExperimentResult = autoeval.ExperimentResult
type EvalOutput = autoeval.EvalOutput
type TraceAnalysis = autoeval.TraceAnalysis
type Config = autoeval.Config

// DefaultConfig returns a Config with sensible defaults.
var DefaultConfig = autoeval.DefaultConfig
