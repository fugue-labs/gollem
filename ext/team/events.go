package team

// TeammateSpawnedEvent is published when a new teammate is created.
type TeammateSpawnedEvent struct {
	TeamName     string
	TeammateName string
	Task         string
}

// TeammateIdleEvent is published when a teammate finishes its current run.
type TeammateIdleEvent struct {
	TeamName     string
	TeammateName string
}

// TeammateTerminatedEvent is published when a teammate shuts down.
type TeammateTerminatedEvent struct {
	TeamName     string
	TeammateName string
	Reason       string
}

// TaskCreatedEvent is published when a new task is added to the board.
type TaskCreatedEvent struct {
	TeamName string
	TaskID   string
	Subject  string
}

// TaskCompletedEvent is published when a task is marked completed.
type TaskCompletedEvent struct {
	TeamName string
	TaskID   string
	Owner    string
}

// MessageSentEvent is published when a message is sent between teammates.
type MessageSentEvent struct {
	TeamName      string
	MessageID     string
	CorrelationID string
	From          string
	To            string
	Type          MessageType
	Summary       string
}
