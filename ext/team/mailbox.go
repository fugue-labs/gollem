package team

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

// MessageType classifies a team message.
type MessageType string

const (
	MessageText            MessageType = "text"
	MessageTaskAssignment  MessageType = "task_assignment"
	MessageShutdownRequest MessageType = "shutdown_request"
	MessageStatusUpdate    MessageType = "status_update"
)

// Message is a communication unit between teammates.
type Message struct {
	ID            string      `json:"id,omitempty"`
	CorrelationID string      `json:"correlation_id,omitempty"`
	From          string      `json:"from"`
	To            string      `json:"to"`
	Type          MessageType `json:"type"`
	Content       string      `json:"content"`
	Summary       string      `json:"summary,omitempty"`
	Timestamp     time.Time   `json:"timestamp"`
}

// ErrMailboxFull is returned when a non-blocking mailbox send cannot enqueue.
var ErrMailboxFull = errors.New("mailbox full")

// Mailbox is a buffered channel-based message queue for a teammate.
type Mailbox struct {
	ch chan Message
}

// NewMailbox creates a mailbox with the given buffer size.
func NewMailbox(bufferSize int) *Mailbox {
	return &Mailbox{ch: make(chan Message, bufferSize)}
}

// newMessage constructs a team message with stable identity fields populated.
func newMessage(from, to string, msgType MessageType, content, summary, correlationID string) Message {
	id := uuid.NewString()
	if correlationID == "" {
		correlationID = id
	}
	return Message{
		ID:            id,
		CorrelationID: correlationID,
		From:          from,
		To:            to,
		Type:          msgType,
		Content:       content,
		Summary:       summary,
		Timestamp:     time.Now(),
	}
}

func ensureMessageIdentity(msg Message) Message {
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.CorrelationID == "" {
		msg.CorrelationID = msg.ID
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	return msg
}

// TrySend enqueues a message without blocking.
// If the buffer is full, the message is not enqueued and ErrMailboxFull is returned.
func (m *Mailbox) TrySend(msg Message) error {
	msg = ensureMessageIdentity(msg)
	select {
	case m.ch <- msg:
		return nil
	default:
		fmt.Fprintf(os.Stderr, "[gollem] WARNING: mailbox full, dropping message from %s to %s (type: %s)\n",
			msg.From, msg.To, msg.Type)
		return ErrMailboxFull
	}
}

// Send preserves the historical fire-and-forget API for compatibility.
// New code that needs delivery feedback should use TrySend.
func (m *Mailbox) Send(msg Message) {
	_ = m.TrySend(msg)
}

// DrainAll reads all pending messages without blocking.
func (m *Mailbox) DrainAll() []Message {
	var msgs []Message
	for {
		select {
		case msg := <-m.ch:
			msgs = append(msgs, msg)
		default:
			return msgs
		}
	}
}

// Receive returns the underlying channel for select-based reads.
func (m *Mailbox) Receive() <-chan Message {
	return m.ch
}

// Len returns the number of pending messages.
func (m *Mailbox) Len() int {
	return len(m.ch)
}
