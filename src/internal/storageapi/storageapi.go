package storageapi

type StateData struct {
	ProcessedIDs   []string                      `json:"processed_ids"`
	SessionMap     map[string]string             `json:"session_map"`
	SessionMeta    map[string]SessionMetaRecord  `json:"session_meta,omitempty"`
	SessionDeleted map[string]string             `json:"session_deleted,omitempty"`
	Sessions       map[string]SessionRecord      `json:"sessions,omitempty"`
	Bindings       map[string]BindingRecord      `json:"bindings,omitempty"`
	RuntimeIndex   map[string]RuntimeRecord      `json:"runtime_index,omitempty"`
	Unassigned     map[string]ConversationRecord `json:"unassigned_conversations,omitempty"`
	UserAccess     map[string]UserAccessRecord   `json:"user_access,omitempty"`
	Inflight       map[string]any                `json:"inflight_tasks"`
}

type SessionRecord struct {
	Key        string `json:"key"`
	Workdir    string `json:"workdir,omitempty"`
	Status     string `json:"status,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	ArchivedAt string `json:"archived_at,omitempty"`
}

type SessionMetaRecord struct {
	Workdir   string `json:"workdir,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Status    string `json:"status,omitempty"`
}

type BindingRecord struct {
	ConversationKey string `json:"conversation_key"`
	Channel         string `json:"channel"`
	ConversationID  string `json:"conversation_id"`
	ThreadID        string `json:"thread_id,omitempty"`
	SessionKey      string `json:"session_key"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

type RuntimeRecord struct {
	SessionKey      string `json:"session_key"`
	Attached        bool   `json:"attached"`
	Status          string `json:"status,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	LastAttachedAt  string `json:"last_attached_at,omitempty"`
	LastDetachedAt  string `json:"last_detached_at,omitempty"`
	LastRecoveredAt string `json:"last_recovered_at,omitempty"`
}

type ConversationRecord struct {
	ConversationKey   string         `json:"conversation_key"`
	Channel           string         `json:"channel"`
	ConversationID    string         `json:"conversation_id"`
	ThreadID          string         `json:"thread_id,omitempty"`
	ConversationTitle string         `json:"conversation_title,omitempty"`
	LastMessageID     string         `json:"last_message_id,omitempty"`
	LastText          string         `json:"last_text,omitempty"`
	LastSender        string         `json:"last_sender,omitempty"`
	LastSeenAt        string         `json:"last_seen_at,omitempty"`
	UpdatedAt         string         `json:"updated_at,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
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
