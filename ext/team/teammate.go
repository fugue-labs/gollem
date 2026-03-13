package team

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

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
	mu                sync.Mutex
	name              string
	state             TeammateState
	mailbox           *Mailbox
	agent             *core.Agent[string]
	cancel            context.CancelFunc
	team              *Team
	wakeCh            chan struct{}
	shutdownRequested bool
	shutdownMessage   Message
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

func (tm *Teammate) requestShutdown(msg Message) Message {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	msg = ensureMessageIdentity(msg)
	if msg.Type == "" {
		msg.Type = MessageShutdownRequest
	}
	if !tm.shutdownRequested {
		tm.shutdownRequested = true
		tm.shutdownMessage = msg
		if tm.state == TeammateIdle {
			tm.state = TeammateShuttingDown
		}
	}
	return tm.shutdownMessage
}

func (tm *Teammate) shutdownRequest() (Message, bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if !tm.shutdownRequested {
		return Message{}, false
	}
	return tm.shutdownMessage, true
}

func (tm *Teammate) filterControlMessages(msgs []Message) []Message {
	if len(msgs) == 0 {
		return nil
	}

	filtered := make([]Message, 0, len(msgs))
	for _, msg := range msgs {
		msg = ensureMessageIdentity(msg)
		if msg.Type == MessageShutdownRequest {
			tm.requestShutdown(msg)
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
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
		reason := "stopped"
		if _, ok := tm.shutdownRequest(); ok {
			reason = "shutdown_requested"
		} else if ctx.Err() != nil {
			reason = "context_cancelled"
		}
		if tm.team.eventBus != nil {
			core.PublishAsync(tm.team.eventBus, TeammateTerminatedEvent{
				TeamName:     tm.team.name,
				TeammateName: tm.name,
				Reason:       reason,
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
		if msg, ok := tm.shutdownRequest(); ok {
			tm.setState(TeammateShuttingDown)
			fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s stopping after shutdown request from %s: %s\n",
				tm.team.name, tm.name, msg.From, previewForLog(msg.Content, 240))
			return
		}

		tm.setState(TeammateRunning)
		fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s running\n", tm.team.name, tm.name)

		runCtx := ctx
		if !firstRun {
			runCtx = core.ContextWithToolCallID(ctx, "")
		}
		firstRun = false

		result, err := tm.agent.Run(runCtx, prompt)
		if msg, ok := tm.shutdownRequest(); ok {
			tm.setState(TeammateShuttingDown)
			fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s stopping after current run due to shutdown request from %s: %s\n",
				tm.team.name, tm.name, msg.From, previewForLog(msg.Content, 240))
			return
		}
		if err != nil {
			if ctx.Err() != nil {
				// Context cancelled — shut down.
				return
			}
			consecutiveErrors++
			fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s error (%d): %v\n",
				tm.team.name, tm.name, consecutiveErrors, err)

			// Notify leader of error.
			if leaderName := tm.team.leaderName(); leaderName != "" {
				if leaderMB := tm.team.getMailbox(leaderName); leaderMB != nil {
					statusMsg := newMessage(
						tm.name,
						leaderName,
						MessageStatusUpdate,
						fmt.Sprintf("Error on attempt %d: %v", consecutiveErrors, err),
						tm.name+" encountered an error",
						"",
					)
					if sendErr := leaderMB.TrySend(statusMsg); sendErr != nil {
						fmt.Fprintf(os.Stderr, "[gollem] WARNING: failed to deliver error update from %s to leader %s: %v\n",
							tm.name, leaderName, sendErr)
					}
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
			if leaderName := tm.team.leaderName(); leaderName != "" {
				leaderMB := tm.team.getMailbox(leaderName)
				if leaderMB != nil {
					statusMsg := newMessage(
						tm.name,
						leaderName,
						MessageStatusUpdate,
						result.Output,
						summary,
						"",
					)
					if sendErr := leaderMB.TrySend(statusMsg); sendErr != nil {
						fmt.Fprintf(os.Stderr, "[gollem] WARNING: failed to deliver completion update from %s to leader %s: %v\n",
							tm.name, leaderName, sendErr)
					}
				}
			}

			fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s completed (tokens: %d in, %d out)\n",
				tm.team.name, tm.name, result.Usage.InputTokens, result.Usage.OutputTokens)
			fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s output: %s\n",
				tm.team.name, tm.name, outputPreview)
		}

		// Go idle and wait for new work or shutdown.
		tm.setState(TeammateIdle)
		if msg, ok := tm.shutdownRequest(); ok {
			tm.setState(TeammateShuttingDown)
			fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s stopping while idle due to shutdown request from %s: %s\n",
				tm.team.name, tm.name, msg.From, previewForLog(msg.Content, 240))
			return
		}
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
				msgs := tm.filterControlMessages(tm.mailbox.DrainAll())
				if msg, ok := tm.shutdownRequest(); ok {
					tm.setState(TeammateShuttingDown)
					fmt.Fprintf(os.Stderr, "[gollem] team:%s teammate:%s received shutdown request from %s: %s\n",
						tm.team.name, tm.name, msg.From, previewForLog(msg.Content, 240))
					return
				}
				if len(msgs) == 0 {
					continue // spurious wake — go back to select
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
