package researcher

// ExperimentResult is the structured output of each experiment cycle.
// The researcher agent must produce this after every eval.
type ExperimentResult struct {
	Commit      string  `json:"commit" jsonschema:"description=Git commit hash (7 chars)"`
	PassRate    float64 `json:"pass_rate" jsonschema:"description=Task pass rate 0.0-1.0"`
	TokensUsed  int     `json:"tokens_used" jsonschema:"description=Total tokens consumed"`
	Status      string  `json:"status" jsonschema:"description=Experiment outcome,enum=keep,discard,crash"`
	Description string  `json:"description" jsonschema:"description=What this experiment tried"`
	Hypothesis  string  `json:"hypothesis" jsonschema:"description=Why you expected this to work"`
	NextIdea    string  `json:"next_idea" jsonschema:"description=What to try next based on results"`
}

// Config holds configuration for the autoeval harness.
type Config struct {
	// Provider selects the model provider: "anthropic", "openai", "ollama",
	// "vertexai", "vertexai-anthropic", "xai".
	Provider string `json:"provider"`

	// Model is the model name to use for the researcher agent.
	Model string `json:"model"`

	// Location is the GCP region for vertexai providers.
	Location string `json:"location"`

	// Project is the GCP project ID for vertexai providers.
	Project string `json:"project"`

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

	// ExperimentTimeout is the maximum duration for a single experiment cycle.
	ExperimentTimeout string `json:"experiment_timeout"`

	// ThinkingBudget is the thinking/reasoning token budget (0 = provider default).
	ThinkingBudget int `json:"thinking_budget"`

	// ReasoningEffort is the reasoning effort level for OpenAI o-series models.
	ReasoningEffort string `json:"reasoning_effort"`

	// OllamaBaseURL is the base URL for the ollama API.
	OllamaBaseURL string `json:"ollama_base_url"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Provider:          "anthropic",
		Model:             "claude-sonnet-4-5-20250929",
		SubjectDir:        "autoeval-subject",
		TracesDir:         "autoeval-traces",
		HarnessTracesDir:  "harness-traces",
		ResultsFile:       "autoeval-results.tsv",
		EvalCommand:       "echo 'eval not configured — set AUTOEVAL_EVAL_COMMAND'",
		EvalResultsPath:   "autoeval-traces/results.json",
		ExperimentTimeout: "30m",
		OllamaBaseURL:     "http://localhost:11434",
	}
}
