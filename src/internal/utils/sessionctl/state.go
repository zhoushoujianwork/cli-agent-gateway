package sessionctl

import (
	"fmt"
	"strings"
	"time"

	"cli-agent-gateway/internal/config"
	"cli-agent-gateway/internal/storage"
)

type Store struct {
	cfg     config.AppConfig
	backend storage.Backend
}

func OpenStore(cfg config.AppConfig) (*Store, error) {
	backend, err := storage.NewBackend(
		cfg.StorageBackend,
		cfg.StateFile,
		cfg.InteractionLogFile,
		cfg.ReportDir,
		cfg.StorageSQLitePath,
	)
	if err != nil {
		return nil, err
	}
	return &Store{cfg: cfg, backend: backend}, nil
}

func (s *Store) Config() config.AppConfig {
	return s.cfg
}

func (s *Store) Backend() storage.Backend {
	return s.backend
}

func (s *Store) Load() (storage.StateData, error) {
	st, err := s.backend.LoadState()
	if err != nil {
		return storage.StateData{}, err
	}
	ensureStateMaps(&st)
	hydrateLegacySessions(&st)
	return st, nil
}

func (s *Store) Save(st storage.StateData) error {
	ensureStateMaps(&st)
	return s.backend.SaveState(st)
}

func (s *Store) Mutate(fn func(*storage.StateData) error) (storage.StateData, error) {
	st, err := s.Load()
	if err != nil {
		return storage.StateData{}, err
	}
	if err := fn(&st); err != nil {
		return storage.StateData{}, err
	}
	if err := s.Save(st); err != nil {
		return storage.StateData{}, err
	}
	return st, nil
}

func CanonicalConversationKey(channel, conversationID, threadID string) string {
	channel = strings.ToLower(strings.TrimSpace(channel))
	conversationID = strings.TrimSpace(conversationID)
	threadID = strings.TrimSpace(threadID)
	if channel == "" || conversationID == "" {
		return ""
	}
	if threadID == "" {
		return channel + "/" + conversationID
	}
	return channel + "/" + conversationID + "/" + threadID
}

func SplitConversationKey(key string) (channel, conversationID, threadID string, err error) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(key), "/"), "/")
	switch len(parts) {
	case 2:
		return parts[0], parts[1], "", nil
	case 3:
		return parts[0], parts[1], parts[2], nil
	default:
		return "", "", "", fmt.Errorf("invalid conversation key: %s", key)
	}
}

func CoalesceTimestamp(ts, fallback string) string {
	if strings.TrimSpace(ts) != "" {
		return strings.TrimSpace(ts)
	}
	return strings.TrimSpace(fallback)
}

func ensureStateMaps(st *storage.StateData) {
	if st.Sessions == nil {
		st.Sessions = map[string]storage.SessionRecord{}
	}
	if st.Bindings == nil {
		st.Bindings = map[string]storage.BindingRecord{}
	}
	if st.RuntimeIndex == nil {
		st.RuntimeIndex = map[string]storage.RuntimeRecord{}
	}
	if st.Unassigned == nil {
		st.Unassigned = map[string]storage.ConversationRecord{}
	}
}

func hydrateLegacySessions(st *storage.StateData) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for key, meta := range st.SessionMeta {
		key = NormalizeSessionKey(key)
		if key == "" {
			continue
		}
		if _, ok := st.Sessions[key]; ok {
			continue
		}
		status := strings.TrimSpace(meta.Status)
		if status == "" {
			status = "detached"
		}
		st.Sessions[key] = storage.SessionRecord{
			Key:       key,
			Workdir:   strings.TrimSpace(meta.Workdir),
			Status:    status,
			CreatedAt: CoalesceTimestamp(meta.UpdatedAt, now),
			UpdatedAt: CoalesceTimestamp(meta.UpdatedAt, now),
		}
	}
	for key, deletedAt := range st.SessionDeleted {
		key = NormalizeSessionKey(key)
		if key == "" {
			continue
		}
		rec := st.Sessions[key]
		if rec.Key == "" {
			rec.Key = key
			rec.CreatedAt = CoalesceTimestamp(deletedAt, now)
		}
		rec.Status = "archived"
		rec.ArchivedAt = CoalesceTimestamp(deletedAt, now)
		rec.UpdatedAt = CoalesceTimestamp(deletedAt, now)
		st.Sessions[key] = rec
	}
}

func NormalizeSessionKey(v string) string {
	raw := strings.TrimSpace(v)
	if raw == "" {
		return ""
	}
	if idx := strings.Index(raw, "#"); idx > 0 {
		raw = raw[:idx]
	}
	return strings.TrimSpace(raw)
}
