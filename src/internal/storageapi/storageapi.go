package storageapi

type StateData struct {
	ProcessedIDs   []string                     `json:"processed_ids"`
	SessionMap     map[string]string            `json:"session_map"`
	SessionMeta    map[string]SessionMetaRecord `json:"session_meta,omitempty"`
	SessionDeleted map[string]string            `json:"session_deleted,omitempty"`
	Inflight       map[string]any               `json:"inflight_tasks"`
}

type SessionMetaRecord struct {
	Workdir   string `json:"workdir,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Status    string `json:"status,omitempty"`
}

type Backend interface {
	LoadState() (StateData, error)
	SaveState(StateData) error
	AppendInteraction(map[string]any) error
	WriteReport(report map[string]any, messageID string) (string, error)
}
