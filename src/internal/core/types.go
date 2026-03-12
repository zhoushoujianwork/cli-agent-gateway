package core

type InboundMessage struct {
	ID       string
	Sender   string
	Text     string
	TS       string
	Channel  string
	ThreadID string
	Metadata map[string]any
}

type TaskRequest struct {
	TraceID    string
	SessionKey string
	UserText   string
	Sender     string
	Channel    string
	ThreadID   string
	Metadata   map[string]any
	EventSink  func(TaskEvent)
}

type TaskResult struct {
	TraceID        string
	Status         string
	Summary        string
	TerminalReason string
	ElapsedSec     int
	OutputText     string
	ErrorText      string
	RawEvents      []map[string]any
}

type TaskEvent struct {
	ID             string
	Method         string
	Kind           string
	Stage          string
	Status         string
	Title          string
	Detail         string
	Text           string
	ActivityKey    string
	PayloadPreview string
}

// ChannelAdapter handles ingress/egress for a channel.
type ChannelAdapter interface {
	Fetch() ([]InboundMessage, error)
	Send(text, to, messageID, reportFile string) error
}

// AgentAdapter executes a task request.
type AgentAdapter interface {
	Execute(req TaskRequest) (TaskResult, error)
	Close() error
}
