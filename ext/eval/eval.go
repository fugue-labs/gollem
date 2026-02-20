package eval

import (
	"context"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// Case represents a single evaluation test case.
type Case[T any] struct {
	Name     string
	Prompt   string
	Expected T
	Metadata map[string]any
}

// Dataset is a collection of evaluation cases.
type Dataset[T any] struct {
	Name  string
	Cases []Case[T]
}

// Evaluator scores an agent's output against expected results.
type Evaluator[T any] interface {
	Evaluate(ctx context.Context, output T, expected T) (*Score, error)
}

// StepEvaluator scores individual steps of an agent run.
type StepEvaluator interface {
	EvaluateStep(ctx context.Context, step *core.ModelResponse, stepIndex int, state *StepState) (*Score, error)
}

// StepState provides context about the run so far.
type StepState struct {
	Messages   []core.ModelMessage
	TotalSteps int
	ToolCalls  int
}

// Score represents an evaluation result.
type Score struct {
	Value   float64        // 0.0 to 1.0
	Reason  string
	Details map[string]any
}

// CaseResult contains the result of a single case.
type CaseResult struct {
	CaseName   string
	Scores     []Score
	StepScores []Score
	Output     any
	Duration   time.Duration
	Usage      core.RunUsage
	Error      error
}

// Report contains evaluation results.
type Report struct {
	DatasetName    string
	TotalCases     int
	PassedCases    int
	FailedCases    int
	AvgScore       float64
	StepPassRate   float64
	StepFailRate   float64
	TotalStepEvals int
	Results        []CaseResult
}

// Runner executes evaluation datasets against agents.
type Runner[T any] struct {
	agent          *core.Agent[T]
	evaluators     []Evaluator[T]
	stepEvaluators []StepEvaluator
	passScore      float64
}

// NewRunner creates an evaluation runner.
func NewRunner[T any](agent *core.Agent[T], evaluators ...Evaluator[T]) *Runner[T] {
	return &Runner[T]{
		agent:      agent,
		evaluators: evaluators,
		passScore:  0.5,
	}
}

// WithPassScore sets the minimum average score to consider a case "passed" (default: 0.5).
func (r *Runner[T]) WithPassScore(score float64) *Runner[T] {
	r.passScore = score
	return r
}

// WithStepEvaluators adds step evaluators that score individual steps of each agent run.
func (r *Runner[T]) WithStepEvaluators(evaluators ...StepEvaluator) *Runner[T] {
	r.stepEvaluators = append(r.stepEvaluators, evaluators...)
	return r
}

// Run executes all cases and returns results.
func (r *Runner[T]) Run(ctx context.Context, dataset Dataset[T]) (*Report, error) {
	report := &Report{
		DatasetName: dataset.Name,
		TotalCases:  len(dataset.Cases),
		Results:     make([]CaseResult, 0, len(dataset.Cases)),
	}

	var totalScore float64
	var totalEvals int

	for _, tc := range dataset.Cases {
		start := time.Now()
		cr := CaseResult{
			CaseName: tc.Name,
		}

		result, err := r.agent.Run(ctx, tc.Prompt)
		cr.Duration = time.Since(start)
		if err != nil {
			cr.Error = err
			report.FailedCases++
			report.Results = append(report.Results, cr)
			continue
		}

		cr.Output = result.Output
		cr.Usage = result.Usage

		// Run output evaluators.
		var caseTotal float64
		for _, evaluator := range r.evaluators {
			score, evalErr := evaluator.Evaluate(ctx, result.Output, tc.Expected)
			if evalErr != nil {
				cr.Error = evalErr
				break
			}
			cr.Scores = append(cr.Scores, *score)
			caseTotal += score.Value
			totalScore += score.Value
			totalEvals++
		}

		// Run step evaluators on each ModelResponse in the conversation.
		if len(r.stepEvaluators) > 0 {
			cr.StepScores = r.evaluateSteps(ctx, result.Messages)
		}

		if len(cr.Scores) > 0 {
			avgCaseScore := caseTotal / float64(len(cr.Scores))
			if avgCaseScore >= r.passScore {
				report.PassedCases++
			} else {
				report.FailedCases++
			}
		} else if cr.Error != nil {
			report.FailedCases++
		}

		report.Results = append(report.Results, cr)
	}

	if totalEvals > 0 {
		report.AvgScore = totalScore / float64(totalEvals)
	}

	// Aggregate step-level pass/fail rates.
	var totalStepEvals int
	var passedStepEvals int
	for _, cr := range report.Results {
		for _, ss := range cr.StepScores {
			totalStepEvals++
			if ss.Value >= r.passScore {
				passedStepEvals++
			}
		}
	}
	report.TotalStepEvals = totalStepEvals
	if totalStepEvals > 0 {
		report.StepPassRate = float64(passedStepEvals) / float64(totalStepEvals)
		report.StepFailRate = float64(totalStepEvals-passedStepEvals) / float64(totalStepEvals)
	}

	return report, nil
}

// evaluateSteps runs step evaluators on each ModelResponse in the conversation history.
func (r *Runner[T]) evaluateSteps(ctx context.Context, messages []core.ModelMessage) []Score {
	var stepScores []Score

	// Count total responses and tool calls for StepState.
	var responses []*core.ModelResponse
	var totalToolCalls int
	for i := range messages {
		if resp, ok := messages[i].(core.ModelResponse); ok {
			responses = append(responses, &resp)
			totalToolCalls += len(resp.ToolCalls())
		}
	}

	for stepIdx, resp := range responses {
		state := &StepState{
			Messages:   messages,
			TotalSteps: len(responses),
			ToolCalls:  totalToolCalls,
		}
		for _, se := range r.stepEvaluators {
			score, err := se.EvaluateStep(ctx, resp, stepIdx, state)
			if err != nil {
				stepScores = append(stepScores, Score{
					Value:  0.0,
					Reason: "step evaluator error: " + err.Error(),
				})
				continue
			}
			stepScores = append(stepScores, *score)
		}
	}

	return stepScores
}
