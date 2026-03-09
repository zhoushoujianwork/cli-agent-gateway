package storageapi

type StateData struct {
	ProcessedIDs   []string                     `json:"processed_ids"`
	SessionMap     map[string]string            `json:"session_map"`
	SessionMeta    map[string]SessionMetaRecord `json:"session_meta,omitempty"`
	SessionDeleted map[string]string            `json:"session_deleted,omitempty"`
	UserAccess     map[string]UserAccessRecord  `json:"user_access,omitempty"`
	Inflight       map[string]any               `json:"inflight_tasks"`
}

type SessionMetaRecord struct {
	Workdir   string `json:"workdir,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Status    string `json:"status,omitempty"`
}

type UserAccessRecord struct {
	Channel           string         `json:"channel"`
	UserID            string         `json:"user_id"`
	SenderName        string         `json:"sender_name,omitempty"`
	Status            string         `json:"status"`
	FirstSeenAt       string         `json:"first_seen_at,omitempty"`
	LastSeenAt        string         `json:"last_seen_at,omitempty"`
	LastMessageID     string         `json:"last_message_id,omitempty"`
	LastText          string         `json:"last_text,omitempty"`
	ThreadID          string         `json:"thread_id,omitempty"`
	ConversationID    string         `json:"conversation_id,omitempty"`
	ConversationTitle string         `json:"conversation_title,omitempty"`
	Source            string         `json:"source,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

type Backend interface {
	LoadState() (StateData, error)
	SaveState(StateData) error
	AppendInteraction(map[string]any) error
	WriteReport(report map[string]any, messageID string) (string, error)
}
