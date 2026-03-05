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
	SessionID  string
	Metadata   map[string]any
}

type TaskResult struct {
	TraceID    string
	Status     string
	Summary    string
	ElapsedSec int
	SessionID  string
	OutputText string
	ErrorText  string
	RawEvents  []map[string]any
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
