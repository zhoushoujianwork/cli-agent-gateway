package storageapi

type StateData struct {
	ProcessedIDs []string          `json:"processed_ids"`
	SessionMap   map[string]string `json:"session_map"`
	Inflight     map[string]any    `json:"inflight_tasks"`
}

type Backend interface {
	LoadState() (StateData, error)
	SaveState(StateData) error
	AppendInteraction(map[string]any) error
	WriteReport(report map[string]any, messageID string) (string, error)
}
