package researcher

import "github.com/fugue-labs/gollem/internal/autoeval"

// ExperimentResult is the structured output of each experiment cycle.
type ExperimentResult = autoeval.ExperimentResult

// EvalOutput is parsed from Terminal Bench results.
type EvalOutput = autoeval.EvalOutput

// TraceAnalysis captures analysis of a failed task trace.
type TraceAnalysis = autoeval.TraceAnalysis

// Config holds configuration for the autoeval harness.
type Config = autoeval.Config

// DefaultConfig returns a Config with sensible defaults.
var DefaultConfig = autoeval.DefaultConfig
