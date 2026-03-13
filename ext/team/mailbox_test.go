package team

import (
	"errors"
	"testing"
	"time"
)

func TestMailbox_SendAndDrain(t *testing.T) {
	mb := NewMailbox(10)

	mb.Send(Message{From: "alice", Content: "hello", Timestamp: time.Now()})
	mb.Send(Message{From: "bob", Content: "world", Timestamp: time.Now()})

	msgs := mb.DrainAll()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].From != "alice" {
		t.Errorf("expected first message from alice, got %q", msgs[0].From)
	}
	if msgs[0].ID == "" {
		t.Error("expected first message to have a generated ID")
	}
	if msgs[0].CorrelationID == "" {
		t.Error("expected first message to have a generated correlation ID")
	}
	if msgs[1].From != "bob" {
		t.Errorf("expected second message from bob, got %q", msgs[1].From)
	}

	// Drain again — should be empty.
	msgs = mb.DrainAll()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after drain, got %d", len(msgs))
	}
}

func TestMailbox_DrainEmpty(t *testing.T) {
	mb := NewMailbox(10)
	msgs := mb.DrainAll()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestMailbox_BufferFull(t *testing.T) {
	mb := NewMailbox(2)

	if err := mb.TrySend(Message{Content: "1"}); err != nil {
		t.Fatalf("first send failed: %v", err)
	}
	if err := mb.TrySend(Message{Content: "2"}); err != nil {
		t.Fatalf("second send failed: %v", err)
	}
	if err := mb.TrySend(Message{Content: "3"}); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("expected ErrMailboxFull, got %v", err)
	}

	msgs := mb.DrainAll()
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages (buffer=2), got %d", len(msgs))
	}
}

func TestMailbox_Len(t *testing.T) {
	mb := NewMailbox(10)

	if mb.Len() != 0 {
		t.Errorf("expected len 0, got %d", mb.Len())
	}

	mb.Send(Message{Content: "1"})
	mb.Send(Message{Content: "2"})

	if mb.Len() != 2 {
		t.Errorf("expected len 2, got %d", mb.Len())
	}
}

func TestMailbox_Receive(t *testing.T) {
	mb := NewMailbox(10)

	go func() {
		time.Sleep(10 * time.Millisecond)
		mb.Send(Message{From: "sender", Content: "async"})
	}()

	select {
	case msg := <-mb.Receive():
		if msg.From != "sender" {
			t.Errorf("expected from 'sender', got %q", msg.From)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}
}
