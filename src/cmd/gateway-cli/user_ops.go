package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"cli-agent-gateway/internal/config"
	"cli-agent-gateway/internal/storage"
)

type UserAccessItem struct {
	Key               string `json:"key"`
	Channel           string `json:"channel"`
	UserID            string `json:"user_id"`
	SenderName        string `json:"sender_name,omitempty"`
	Status            string `json:"status"`
	FirstSeenAt       string `json:"first_seen_at,omitempty"`
	LastSeenAt        string `json:"last_seen_at,omitempty"`
	LastMessageID     string `json:"last_message_id,omitempty"`
	LastText          string `json:"last_text,omitempty"`
	ThreadID          string `json:"thread_id,omitempty"`
	ConversationID    string `json:"conversation_id,omitempty"`
	ConversationTitle string `json:"conversation_title,omitempty"`
}

type UsersPayload struct {
	OK     bool             `json:"ok"`
	Action string           `json:"action"`
	Items  []UserAccessItem `json:"items"`
}

func runUsers(repoRoot string, args []string) int {
	fs := flag.NewFlagSet("users", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	statusFilter := fs.String("status", "", "filter by status")
	channelFilter := fs.String("channel", "", "filter by channel")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	items, err := loadUserAccessItems(repoRoot)
	if err != nil {
		if *jsonOut {
			printJSONActionError("users", "users_load_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "users failed: %v\n", err)
		return 1
	}
	filtered := make([]UserAccessItem, 0, len(items))
	for _, item := range items {
		if v := strings.TrimSpace(*statusFilter); v != "" && !strings.EqualFold(item.Status, v) {
			continue
		}
		if v := strings.TrimSpace(*channelFilter); v != "" && !strings.EqualFold(item.Channel, v) {
			continue
		}
		filtered = append(filtered, item)
	}
	payload := UsersPayload{OK: true, Action: "users", Items: filtered}
	if *jsonOut {
		printJSON(payload)
		return 0
	}
	for _, item := range filtered {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", item.Channel, item.UserID, item.Status, nonEmpty(item.SenderName, "-"), nonEmpty(item.LastSeenAt, "-"))
	}
	return 0
}

func runUserAllow(repoRoot string, args []string) int {
	return runUserStatusUpdate(repoRoot, args, "allowed", "user-allow")
}

func runUserBlock(repoRoot string, args []string) int {
	return runUserStatusUpdate(repoRoot, args, "blocked", "user-block")
}

func runUserStatusUpdate(repoRoot string, args []string, status string, action string) int {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	channel := fs.String("channel", "", "channel id")
	userID := fs.String("user-id", "", "user id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ch := strings.TrimSpace(*channel)
	id := strings.TrimSpace(*userID)
	if ch == "" || id == "" {
		if *jsonOut {
			printJSONActionError(action, "user_identity_required", "--channel and --user-id are required")
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s requires --channel and --user-id\n", action)
		return 1
	}
	item, err := mutateUserAccess(repoRoot, ch, id, status)
	if err != nil {
		if *jsonOut {
			printJSONActionError(action, action+"_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", action, err)
		return 1
	}
	if *jsonOut {
		printJSON(map[string]any{
			"ok":     true,
			"action": action,
			"item":   item,
		})
		return 0
	}
	fmt.Printf("%s ok: %s %s -> %s\n", action, item.Channel, item.UserID, item.Status)
	return 0
}

func loadUserAccessItems(repoRoot string) ([]UserAccessItem, error) {
	cfg, err := config.Load(repoRoot, "")
	if err != nil {
		return nil, err
	}
	store, err := storage.NewBackend(
		cfg.StorageBackend,
		cfg.StateFile,
		cfg.InteractionLogFile,
		cfg.ReportDir,
		cfg.StorageSQLitePath,
	)
	if err != nil {
		return nil, err
	}
	st, err := store.LoadState()
	if err != nil {
		return nil, err
	}
	items := make([]UserAccessItem, 0, len(st.UserAccess))
	for key, rec := range st.UserAccess {
		items = append(items, UserAccessItem{
			Key:               key,
			Channel:           strings.TrimSpace(rec.Channel),
			UserID:            strings.TrimSpace(rec.UserID),
			SenderName:        strings.TrimSpace(rec.SenderName),
			Status:            strings.TrimSpace(rec.Status),
			FirstSeenAt:       strings.TrimSpace(rec.FirstSeenAt),
			LastSeenAt:        strings.TrimSpace(rec.LastSeenAt),
			LastMessageID:     strings.TrimSpace(rec.LastMessageID),
			LastText:          strings.TrimSpace(rec.LastText),
			ThreadID:          strings.TrimSpace(rec.ThreadID),
			ConversationID:    strings.TrimSpace(rec.ConversationID),
			ConversationTitle: strings.TrimSpace(rec.ConversationTitle),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Status != items[j].Status {
			if items[i].Status == "pending" {
				return true
			}
			if items[j].Status == "pending" {
				return false
			}
		}
		return items[i].LastSeenAt > items[j].LastSeenAt
	})
	return items, nil
}

func mutateUserAccess(repoRoot, channel, userID, status string) (UserAccessItem, error) {
	cfg, err := config.Load(repoRoot, "")
	if err != nil {
		return UserAccessItem{}, err
	}
	store, err := storage.NewBackend(
		cfg.StorageBackend,
		cfg.StateFile,
		cfg.InteractionLogFile,
		cfg.ReportDir,
		cfg.StorageSQLitePath,
	)
	if err != nil {
		return UserAccessItem{}, err
	}
	st, err := store.LoadState()
	if err != nil {
		return UserAccessItem{}, err
	}
	if st.UserAccess == nil {
		st.UserAccess = map[string]storage.UserAccessRecord{}
	}
	key := strings.TrimSpace(channel) + "|" + strings.TrimSpace(userID)
	rec := st.UserAccess[key]
	if strings.TrimSpace(rec.Channel) == "" {
		rec.Channel = strings.TrimSpace(channel)
	}
	if strings.TrimSpace(rec.UserID) == "" {
		rec.UserID = strings.TrimSpace(userID)
	}
	if strings.TrimSpace(rec.FirstSeenAt) == "" {
		rec.FirstSeenAt = time.Now().UTC().Format(time.RFC3339)
	}
	rec.Status = strings.TrimSpace(status)
	rec.LastSeenAt = time.Now().UTC().Format(time.RFC3339)
	st.UserAccess[key] = rec
	if err := store.SaveState(st); err != nil {
		return UserAccessItem{}, err
	}
	return UserAccessItem{
		Key:               key,
		Channel:           rec.Channel,
		UserID:            rec.UserID,
		SenderName:        rec.SenderName,
		Status:            rec.Status,
		FirstSeenAt:       rec.FirstSeenAt,
		LastSeenAt:        rec.LastSeenAt,
		LastMessageID:     rec.LastMessageID,
		LastText:          rec.LastText,
		ThreadID:          rec.ThreadID,
		ConversationID:    rec.ConversationID,
		ConversationTitle: rec.ConversationTitle,
	}, nil
}
