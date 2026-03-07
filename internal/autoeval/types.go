// Package autoeval contains shared types for the autonomous evaluation harness.
package autoeval

// ExperimentResult is the structured output of each experiment cycle.
// The researcher agent must produce this after every eval.
type ExperimentResult struct {
	Commit     string  `json:"commit" jsonschema:"description=Git commit hash (7 chars)"`
	PassRate   float64 `json:"pass_rate" jsonschema:"description=Task pass rate 0.0-1.0"`
	TokensUsed int     `json:"tokens_used" jsonschema:"description=Total tokens consumed"`
	Status     string  `json:"status" jsonschema:"description=Experiment outcome,enum=keep,discard,crash"`
	Description string `json:"description" jsonschema:"description=What this experiment tried"`
	Hypothesis string  `json:"hypothesis" jsonschema:"description=Why you expected this to work"`
	NextIdea   string  `json:"next_idea" jsonschema:"description=What to try next based on results"`
}

// EvalOutput is parsed from Terminal Bench results.
type EvalOutput struct {
	PassRate     float64  `json:"pass_rate"`
	TasksPassed  int      `json:"tasks_passed"`
	TasksTotal   int      `json:"tasks_total"`
	TasksFailed  []string `json:"tasks_failed"`
	DurationSecs float64  `json:"duration_secs"`
	TokensUsed   int      `json:"tokens_used"`
}

// TraceAnalysis is what the agent produces after reading failure traces.
type TraceAnalysis struct {
	TaskID       string `json:"task_id"`
	FailureMode  string `json:"failure_mode"`
	RootCause    string `json:"root_cause"`
	SuggestedFix string `json:"suggested_fix"`
}

// Config holds configuration for the autoeval harness.
type Config struct {
	// Provider selects the model provider: "anthropic", "openai", or "ollama".
	Provider string `json:"provider"`

	// Model is the model name to use for the researcher agent.
	Model string `json:"model"`

	// EvalProvider selects the provider for running evaluations.
	// If empty, defaults to Provider.
	EvalProvider string `json:"eval_provider"`

	// EvalModel is the model used during evaluation.
	// If empty, defaults to Model.
	EvalModel string `json:"eval_model"`

	// SubjectDir is the directory containing the agent config to optimize.
	SubjectDir string `json:"subject_dir"`

	// TracesDir is where eval traces are stored.
	TracesDir string `json:"traces_dir"`

	// HarnessTracesDir is where harness (researcher) traces are stored.
	HarnessTracesDir string `json:"harness_traces_dir"`

	// ResultsFile is the path to the results.tsv log.
	ResultsFile string `json:"results_file"`

	// EvalCommand is the shell command to run the evaluation.
	// The harness substitutes {mode} with "fast" or "full".
	EvalCommand string `json:"eval_command"`

	// EvalResultsPath is where the eval writes its results JSON.
	EvalResultsPath string `json:"eval_results_path"`

	// MaxExperiments limits the number of experiments (0 = unlimited).
	MaxExperiments int `json:"max_experiments"`

	// OllamaBaseURL is the base URL for the ollama API.
	// Defaults to http://localhost:11434.
	OllamaBaseURL string `json:"ollama_base_url"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Provider:         "ollama",
		Model:            "qwen3:70b",
		EvalProvider:     "ollama",
		EvalModel:        "qwen3:70b",
		SubjectDir:       "autoeval-subject",
		TracesDir:        "autoeval-traces",
		HarnessTracesDir: "harness-traces",
		ResultsFile:      "autoeval-results.tsv",
		EvalCommand:      "echo 'eval not configured — set eval_command in config'",
		EvalResultsPath:  "autoeval-traces/results.json",
		OllamaBaseURL:    "http://localhost:11434",
	}
}
