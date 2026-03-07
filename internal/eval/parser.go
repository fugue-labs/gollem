package eval

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/fugue-labs/gollem/internal/autoeval"
)

// ParseResults reads and parses the eval results JSON file.
func ParseResults(path string) (*autoeval.EvalOutput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read results file %s: %w", path, err)
	}

	var result autoeval.EvalOutput
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse results JSON: %w", err)
	}

	// Compute pass rate if not already set.
	if result.PassRate == 0 && result.TasksTotal > 0 {
		result.PassRate = float64(result.TasksPassed) / float64(result.TasksTotal)
	}

	return &result, nil
}
