package team

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// TeammateState represents the lifecycle state of a teammate.
type TeammateState int

const (
	TeammateStarting TeammateState = iota
	TeammateRunning
	TeammateIdle
	TeammateShuttingDown
	TeammateStopped
)

func (s TeammateState) String() string {
	switch s {
	case TeammateStarting:
		return "starting"
	case TeammateRunning:
		return "running"
	case TeammateIdle:
		return "idle"
	case TeammateShuttingDown:
		return "shutting_down"
	case TeammateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// TeammateInfo is a snapshot of teammate state for tools/prompts.
type TeammateInfo struct {
	Name  string        `json:"name"`
	State TeammateState `json:"state"`
}

// Teammate represents a worker agent running as a goroutine.
type Teammate struct {
	mu      sync.Mutex
	name    string
	state   TeammateState
	mailbox *Mailbox
	agent   *core.Agent[string]
	cancel  context.CancelFunc
	team    *Team
	wakeCh  chan struct{}
}

// Name returns the teammate's name.
func (tm *Teammate) Name() string {
	return tm.name
}

// State returns the current state.
func (tm *Teammate) State() TeammateState {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.state
}

func (tm *Teammate) setState(s TeammateState) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.state = s
}

// Wake signals the teammate to process new messages.
func (tm *Teammate) Wake() {
	select {
	case tm.wakeCh <- struct{}{}:
	default:
		// Already signaled.
	}
}

// run is the main goroutine loop for a teammate.
func (tm *Teammate) run(ctx context.Context, initialTask string) {
	defer func() {
		tm.setState(TeammateStopped)
		if tm.team.eventBus != nil {
			core.PublishAsync(tm.team.eventBus, TeammateTerminatedEvent{
				TeamName:     tm.team.name,
				TeammateName: tm.name,
				Reason:       "stopped",
			})
		}
		tm.team.wg.Done()
	}()

	prompt := initialTask
	consecutiveErrors := 0

	// Clear the parent's ToolCallID after the first run so that subsequent
	// runs (triggered by mailbox messages) don't nest under the original
	// spawn_teammate tool span. The first run correctly nests; later runs
	// create independent spans under the teammate's own trace context.
	firstRun := true

	for {
		tm.setState(TeammateRunning)
		fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s running\n", tm.team.name, tm.name)

		runCtx := ctx
		if !firstRun {
			runCtx = core.ContextWithToolCallID(ctx, "")
		}
		firstRun = false

		result, err := tm.agent.Run(runCtx, prompt)
		if err != nil {
			if ctx.Err() != nil {
				// Context cancelled — shut down.
				return
			}
			consecutiveErrors++
			fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s error (%d): %v\n",
				tm.team.name, tm.name, consecutiveErrors, err)

			// Notify leader of error.
			if tm.team.leader != "" {
				if leaderMB := tm.team.getMailbox(tm.team.leader); leaderMB != nil {
					leaderMB.Send(Message{
						From:      tm.name,
						To:        tm.team.leader,
						Type:      MessageStatusUpdate,
						Content:   fmt.Sprintf("Error on attempt %d: %v", consecutiveErrors, err),
						Summary:   tm.name + " encountered an error",
						Timestamp: time.Now(),
					})
				}
			}

			// Stop after 3 consecutive errors to prevent hot retry loops.
			if consecutiveErrors >= 3 {
				fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s stopping after %d consecutive errors\n",
					tm.team.name, tm.name, consecutiveErrors)
				return
			}
		} else {
			consecutiveErrors = 0
			outputPreview := previewForLog(result.Output, 320)
			summary := tm.name + " finished current task"
			if outputPreview != "<empty>" {
				summary = tm.name + ": " + outputPreview
			}
			// Notify leader of completion via their mailbox.
			if tm.team.leader != "" {
				leaderMB := tm.team.getMailbox(tm.team.leader)
				if leaderMB != nil {
					leaderMB.Send(Message{
						From:      tm.name,
						To:        tm.team.leader,
						Type:      MessageStatusUpdate,
						Content:   result.Output,
						Summary:   summary,
						Timestamp: time.Now(),
					})
				}
			}

			fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s completed (tokens: %d in, %d out)\n",
				tm.team.name, tm.name, result.Usage.InputTokens, result.Usage.OutputTokens)
			fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s output: %s\n",
				tm.team.name, tm.name, outputPreview)
		}

		// Go idle and wait for new work or shutdown.
		tm.setState(TeammateIdle)
		if tm.team.eventBus != nil {
			core.PublishAsync(tm.team.eventBus, TeammateIdleEvent{
				TeamName:     tm.team.name,
				TeammateName: tm.name,
			})
		}
		fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s idle, waiting for messages\n", tm.team.name, tm.name)

		// Inner loop: wait for messages with actual content. Spurious
		// wakes (empty mailbox, e.g. when the middleware already consumed
		// the message during the previous run) go back to waiting instead
		// of re-running the previous task with the stale prompt.
		gotWork := false
		for !gotWork {
			select {
			case <-ctx.Done():
				return
			case <-tm.team.done:
				return
			case <-tm.wakeCh:
				msgs := tm.mailbox.DrainAll()
				if len(msgs) == 0 {
					continue // spurious wake — go back to select
				}

				// Check for shutdown request.
				for _, msg := range msgs {
					if msg.Type == MessageShutdownRequest {
						fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s received shutdown request\n",
							tm.team.name, tm.name)
						return
					}
				}

				// Build prompt from messages.
				prompt = formatMessagesAsPrompt(msgs)
				gotWork = true
			}
		}
	}
}

func formatMessagesAsPrompt(msgs []Message) string {
	if len(msgs) == 1 {
		return "[Message from " + msgs[0].From + "]: " + msgs[0].Content
	}
	var b strings.Builder
	for i, msg := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("[Message from ")
		b.WriteString(msg.From)
		b.WriteString("]: ")
		b.WriteString(msg.Content)
	}
	return b.String()
}
