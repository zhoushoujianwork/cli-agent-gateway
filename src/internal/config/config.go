package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cli-agent-gateway/internal/infra/envfile"
)

type AppConfig struct {
	RepoRoot                string
	ChannelType             string
	Workdir                 string
	FetchCmd                string
	SendCmd                 string
	RemoteUserID            string
	AllowedFrom             map[string]struct{}
	PollIntervalSec         int
	TimeoutSec              int
	ReportDir               string
	StateFile               string
	InteractionLogFile      string
	LockFile                string
	ProcessOnlyLatest       bool
	ACPAgentCmd             string
	PermissionPolicy        string
	InitializeTimeoutSec    int
	SessionNewTimeoutSec    int
	SessionNewRetries       int
	SessionNewBackoffSec    float64
	ReplyStyleEnabled       bool
	ReplyStylePrompt        string
	ShowToolTrace           bool
	ToolProgressNotify      bool
	StorageBackend          string
	StorageSQLitePath       string
	IMessageFetchCmd        string
	IMessageSendCmd         string
	IMessageFetchTimeoutSec int
	IMessageSendTimeoutSec  int
	DingTalkQueueFile       string
	DingTalkFetchMax        int
	DingTalkDMPolicy        string
	DingTalkGroupPolicy     string
	DingTalkAllowedFrom     map[string]struct{}
	DingTalkGroupAllowed    map[string]struct{}
	DingTalkRequireAt       bool
	DingTalkSendMode        string
	DingTalkSendMsgType     string
	DingTalkSendTimeout     int
	DingTalkTitle           string
	DingTalkPrettyStatus    bool
	DingTalkBotWebhook      string
	DingTalkBotSecret       string
	DingTalkAppKey          string
	DingTalkAppSecret       string
	DingTalkAgentID         string
	DingTalkDefaultTo       string
	DingTalkTokenURL        string
	DingTalkSendURL         string
}

func Load(repoRoot, workdirArg string) (AppConfig, error) {
	if err := envfile.LoadDotEnvSetDefault(filepath.Join(repoRoot, ".env")); err != nil {
		return AppConfig{}, err
	}

	channel := strings.ToLower(strings.TrimSpace(getEnv("CHANNEL_TYPE", "command")))
	if channel == "" {
		channel = "command"
	}

	defaultFetchCmd := "printf '[]'"
	defaultSendCmd := "true"
	if channel == "dingtalk" {
		defaultFetchCmd = "builtin:dingtalk-stream"
		defaultSendCmd = "true"
	}
	if channel == "imessage" {
		defaultFetchCmd = "imsg fetch --json"
		defaultSendCmd = "imsg send"
	}

	workdir := workdirArg
	if strings.TrimSpace(workdir) == "" {
		workdir = getEnv("CODEX_WORKDIR", repoRoot)
	}
	workdir = resolvePath(repoRoot, workdir)

	allowRaw := strings.TrimSpace(getEnv("ALLOWED_FROM", strings.TrimSpace(getEnv("REMOTE_USER_ID", ""))))
	allowedFrom := map[string]struct{}{}
	for _, item := range strings.Split(allowRaw, ",") {
		v := strings.TrimSpace(item)
		if v != "" {
			allowedFrom[v] = struct{}{}
		}
	}

	cfg := AppConfig{
		RepoRoot:             repoRoot,
		ChannelType:          channel,
		Workdir:              workdir,
		FetchCmd:             strings.TrimSpace(getEnv("SMS_FETCH_CMD", defaultFetchCmd)),
		SendCmd:              strings.TrimSpace(getEnv("SMS_SEND_CMD", defaultSendCmd)),
		RemoteUserID:         strings.TrimSpace(getEnv("REMOTE_USER_ID", "")),
		AllowedFrom:          allowedFrom,
		PollIntervalSec:      getEnvInt("POLL_INTERVAL_SEC", 5),
		TimeoutSec:           getEnvInt("AGENT_TIMEOUT_SEC", 1800),
		ReportDir:            resolvePath(repoRoot, getEnv("REPORT_DIR", filepath.Join(repoRoot, ".agent_gateway_reports"))),
		StateFile:            resolvePath(repoRoot, getEnv("STATE_FILE", filepath.Join(repoRoot, ".agent_gateway_state.json"))),
		InteractionLogFile:   resolvePath(repoRoot, getEnv("INTERACTION_LOG_FILE", filepath.Join(repoRoot, ".agent_gateway_interactions.jsonl"))),
		LockFile:             resolvePath(repoRoot, getEnv("LOCK_FILE", filepath.Join(repoRoot, ".cli_agent_gateway.lock"))),
		ProcessOnlyLatest:    getEnvBool("PROCESS_ONLY_LATEST", false),
		ACPAgentCmd:          strings.TrimSpace(getEnv("ACP_AGENT_CMD", "codex-acp")),
		PermissionPolicy:     strings.TrimSpace(getEnv("ACP_PERMISSION_POLICY", "auto_allow")),
		InitializeTimeoutSec: getEnvInt("ACP_INITIALIZE_TIMEOUT_SEC", 30),
		SessionNewTimeoutSec: getEnvInt("ACP_SESSION_NEW_TIMEOUT_SEC", 90),
		SessionNewRetries:    getEnvInt("ACP_SESSION_NEW_RETRIES", 2),
		SessionNewBackoffSec: getEnvFloat("ACP_SESSION_NEW_RETRY_BACKOFF_SEC", 1),
		ReplyStyleEnabled:    getEnvBool("REPLY_STYLE_ENABLED", true),
		ReplyStylePrompt: strings.TrimSpace(getEnv("REPLY_STYLE_PROMPT",
			"请用简洁聊天格式回复：短句优先；先结论后细节；尽量 3-6 行；避免长段落、复杂 Markdown、表格；代码只给最小必要片段；状态词清晰（进行中/阻塞/完成）。")),
		ShowToolTrace:           getEnvBool("SHOW_TOOL_TRACE", false),
		ToolProgressNotify:      getEnvBool("TOOL_PROGRESS_NOTIFY_ENABLED", true),
		StorageBackend:          strings.TrimSpace(getEnv("STORAGE_BACKEND", "sqlite")),
		StorageSQLitePath:       resolvePath(repoRoot, getEnv("STORAGE_SQLITE_PATH", filepath.Join(repoRoot, ".agent_gateway.db"))),
		IMessageFetchCmd:        strings.TrimSpace(getEnv("IMESSAGE_FETCH_CMD", defaultFetchCmd)),
		IMessageSendCmd:         strings.TrimSpace(getEnv("IMESSAGE_SEND_CMD", defaultSendCmd)),
		IMessageFetchTimeoutSec: getEnvInt("IMESSAGE_FETCH_TIMEOUT_SEC", 30),
		IMessageSendTimeoutSec:  getEnvInt("IMESSAGE_SEND_TIMEOUT_SEC", 30),
		DingTalkQueueFile:       resolvePath(repoRoot, getEnv("DINGTALK_QUEUE_FILE", ".dingtalk_inbox.jsonl")),
		DingTalkFetchMax:        getEnvInt("DINGTALK_FETCH_MAX_EVENTS", 30),
		DingTalkDMPolicy:        strings.TrimSpace(getEnv("DINGTALK_DM_POLICY", "allowlist")),
		DingTalkGroupPolicy:     strings.TrimSpace(getEnv("DINGTALK_GROUP_POLICY", "allowlist")),
		DingTalkAllowedFrom:     csvSet(getEnv("DINGTALK_ALLOWED_FROM", "")),
		DingTalkGroupAllowed:    csvSet(getEnv("DINGTALK_GROUP_ALLOWLIST", "")),
		DingTalkRequireAt:       getEnvBool("DINGTALK_REQUIRE_MENTION_IN_GROUP", true),
		DingTalkSendMode:        strings.TrimSpace(getEnv("DINGTALK_SEND_MODE", "api")),
		DingTalkSendMsgType:     strings.TrimSpace(getEnv("DINGTALK_SEND_MSGTYPE", "markdown")),
		DingTalkSendTimeout:     getEnvInt("DINGTALK_SEND_TIMEOUT_SEC", 10),
		DingTalkTitle:           strings.TrimSpace(getEnv("DINGTALK_MARKDOWN_TITLE", "CLI Agent Gateway")),
		DingTalkPrettyStatus:    getEnvBool("DINGTALK_PRETTY_STATUS", true),
		DingTalkBotWebhook:      strings.TrimSpace(getEnv("DINGTALK_BOT_WEBHOOK", "")),
		DingTalkBotSecret:       strings.TrimSpace(getEnv("DINGTALK_BOT_SECRET", "")),
		DingTalkAppKey:          strings.TrimSpace(getEnv("DINGTALK_APP_KEY", "")),
		DingTalkAppSecret:       strings.TrimSpace(getEnv("DINGTALK_APP_SECRET", "")),
		DingTalkAgentID:         strings.TrimSpace(getEnv("DINGTALK_AGENT_ID", "")),
		DingTalkDefaultTo:       strings.TrimSpace(getEnv("DINGTALK_DEFAULT_TO_USER", "")),
		DingTalkTokenURL:        strings.TrimSpace(getEnv("DINGTALK_TOKEN_URL", "")),
		DingTalkSendURL:         strings.TrimSpace(getEnv("DINGTALK_SEND_URL", "https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2")),
	}
	if cfg.PollIntervalSec < 1 {
		cfg.PollIntervalSec = 1
	}
	if cfg.TimeoutSec < 1 {
		cfg.TimeoutSec = 1
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(getEnv(key, ""))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func getEnvFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(getEnv(key, ""))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

func getEnvBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(getEnv(key, ""))
	if raw == "" {
		return fallback
	}
	return raw != "0" && !strings.EqualFold(raw, "false")
}

func csvSet(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range strings.Split(strings.TrimSpace(raw), ",") {
		v := strings.TrimSpace(item)
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}

func resolvePath(repoRoot, path string) string {
	if path == "~" {
		if h, err := os.UserHomeDir(); err == nil {
			path = h
		}
	}
	if strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(h, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
