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

type RuntimePaths struct {
	BaseDir            string
	ReportDir          string
	StateFile          string
	InteractionLogFile string
	LockFile           string
	StorageSQLitePath  string
}

func Load(repoRoot, workdirArg string) (AppConfig, error) {
	if err := loadEnvDefaults(repoRoot); err != nil {
		return AppConfig{}, err
	}
	runtimeValues, err := loadRuntimeValues(repoRoot)
	if err != nil {
		return AppConfig{}, err
	}

	getString := func(key, fallback string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		if v := strings.TrimSpace(runtimeValues[key]); v != "" {
			return v
		}
		return fallback
	}
	getInt := func(key string, fallback int) int {
		raw := strings.TrimSpace(getString(key, ""))
		if raw == "" {
			return fallback
		}
		v, err := strconv.Atoi(raw)
		if err != nil {
			return fallback
		}
		return v
	}
	getFloat := func(key string, fallback float64) float64 {
		raw := strings.TrimSpace(getString(key, ""))
		if raw == "" {
			return fallback
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fallback
		}
		return v
	}
	getBool := func(key string, fallback bool) bool {
		raw := strings.TrimSpace(getString(key, ""))
		if raw == "" {
			return fallback
		}
		return raw != "0" && !strings.EqualFold(raw, "false")
	}

	channel := strings.ToLower(strings.TrimSpace(getString("CHANNEL_TYPE", defaultChannelType)))
	if channel == "" {
		channel = defaultChannelType
	}

	channelFetchCmdDefault := defaultFetchCmd
	channelSendCmdDefault := defaultSendCmd
	if channel == "dingtalk" {
		channelFetchCmdDefault = "builtin:dingtalk-stream"
		channelSendCmdDefault = defaultSendCmd
	}
	if channel == "imessage" {
		channelFetchCmdDefault = defaultIMessageFetchCmd
		channelSendCmdDefault = defaultIMessageSendCmd
	}

	workdir := workdirArg
	if strings.TrimSpace(workdir) == "" {
		workdir = "~/.cag"
	}
	workdir = resolvePath(repoRoot, workdir)
	runtimePaths := DefaultRuntimePaths(repoRoot)

	allowRaw := strings.TrimSpace(getString("ALLOWED_FROM", strings.TrimSpace(getString("REMOTE_USER_ID", ""))))
	allowedFrom := map[string]struct{}{}
	for _, item := range strings.Split(allowRaw, ",") {
		v := strings.TrimSpace(item)
		if v != "" {
			allowedFrom[v] = struct{}{}
		}
	}

	cfg := AppConfig{
		RepoRoot:                repoRoot,
		ChannelType:             channel,
		Workdir:                 workdir,
		FetchCmd:                strings.TrimSpace(getString("SMS_FETCH_CMD", channelFetchCmdDefault)),
		SendCmd:                 strings.TrimSpace(getString("SMS_SEND_CMD", channelSendCmdDefault)),
		RemoteUserID:            strings.TrimSpace(getString("REMOTE_USER_ID", "")),
		AllowedFrom:             allowedFrom,
		PollIntervalSec:         getInt("POLL_INTERVAL_SEC", 5),
		TimeoutSec:              getInt("AGENT_TIMEOUT_SEC", 1800),
		ReportDir:               runtimePaths.ReportDir,
		StateFile:               runtimePaths.StateFile,
		InteractionLogFile:      runtimePaths.InteractionLogFile,
		LockFile:                runtimePaths.LockFile,
		ProcessOnlyLatest:       getBool("PROCESS_ONLY_LATEST", false),
		ACPAgentCmd:             strings.TrimSpace(getString("ACP_AGENT_CMD", defaultACPAgentCmd)),
		PermissionPolicy:        strings.TrimSpace(getString("ACP_PERMISSION_POLICY", defaultPermissionPolicy)),
		InitializeTimeoutSec:    getInt("ACP_INITIALIZE_TIMEOUT_SEC", 30),
		SessionNewTimeoutSec:    getInt("ACP_SESSION_NEW_TIMEOUT_SEC", 90),
		SessionNewRetries:       getInt("ACP_SESSION_NEW_RETRIES", 2),
		SessionNewBackoffSec:    getFloat("ACP_SESSION_NEW_RETRY_BACKOFF_SEC", 1),
		ReplyStyleEnabled:       getBool("REPLY_STYLE_ENABLED", true),
		ReplyStylePrompt:        strings.TrimSpace(getString("REPLY_STYLE_PROMPT", defaultReplyStylePrompt)),
		ShowToolTrace:           getBool("SHOW_TOOL_TRACE", false),
		ToolProgressNotify:      getBool("TOOL_PROGRESS_NOTIFY_ENABLED", true),
		StorageBackend:          strings.TrimSpace(getString("STORAGE_BACKEND", defaultStorageBackend)),
		StorageSQLitePath:       runtimePaths.StorageSQLitePath,
		IMessageFetchCmd:        strings.TrimSpace(getString("IMESSAGE_FETCH_CMD", channelFetchCmdDefault)),
		IMessageSendCmd:         strings.TrimSpace(getString("IMESSAGE_SEND_CMD", channelSendCmdDefault)),
		IMessageFetchTimeoutSec: getInt("IMESSAGE_FETCH_TIMEOUT_SEC", 30),
		IMessageSendTimeoutSec:  getInt("IMESSAGE_SEND_TIMEOUT_SEC", 30),
		DingTalkFetchMax:        getInt("DINGTALK_FETCH_MAX_EVENTS", 30),
		DingTalkDMPolicy:        strings.TrimSpace(getString("DINGTALK_DM_POLICY", "allowlist")),
		DingTalkGroupPolicy:     strings.TrimSpace(getString("DINGTALK_GROUP_POLICY", "allowlist")),
		DingTalkAllowedFrom:     csvSet(getString("DINGTALK_ALLOWED_FROM", "")),
		DingTalkGroupAllowed:    csvSet(getString("DINGTALK_GROUP_ALLOWLIST", "")),
		DingTalkRequireAt:       getBool("DINGTALK_REQUIRE_MENTION_IN_GROUP", true),
		DingTalkSendMode:        strings.TrimSpace(getString("DINGTALK_SEND_MODE", "api")),
		DingTalkSendMsgType:     strings.TrimSpace(getString("DINGTALK_SEND_MSGTYPE", "markdown")),
		DingTalkSendTimeout:     getInt("DINGTALK_SEND_TIMEOUT_SEC", 10),
		DingTalkTitle:           strings.TrimSpace(getString("DINGTALK_MARKDOWN_TITLE", "CLI Agent Gateway")),
		DingTalkPrettyStatus:    getBool("DINGTALK_PRETTY_STATUS", true),
		DingTalkBotWebhook:      strings.TrimSpace(getString("DINGTALK_BOT_WEBHOOK", "")),
		DingTalkBotSecret:       strings.TrimSpace(getString("DINGTALK_BOT_SECRET", "")),
		DingTalkAppKey:          strings.TrimSpace(getString("DINGTALK_APP_KEY", "")),
		DingTalkAppSecret:       strings.TrimSpace(getString("DINGTALK_APP_SECRET", "")),
		DingTalkAgentID:         strings.TrimSpace(getString("DINGTALK_AGENT_ID", "")),
		DingTalkDefaultTo:       strings.TrimSpace(getString("DINGTALK_DEFAULT_TO_USER", "")),
		DingTalkTokenURL:        strings.TrimSpace(getString("DINGTALK_TOKEN_URL", defaultDingTalkTokenURL)),
		DingTalkSendURL:         strings.TrimSpace(getString("DINGTALK_SEND_URL", defaultDingTalkSendURL)),
	}
	if cfg.PollIntervalSec < 1 {
		cfg.PollIntervalSec = 1
	}
	if cfg.TimeoutSec < 1 {
		cfg.TimeoutSec = 1
	}
	return cfg, nil
}

func loadEnvDefaults(repoRoot string) error {
	if err := envfile.LoadDotEnvSetDefault(filepath.Join(repoRoot, ".env")); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	if err := envfile.LoadDotEnvSetDefault(filepath.Join(home, ".cag", ".env")); err != nil {
		return err
	}
	return nil
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

func CAGHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".cag")
	}
	return filepath.Join(os.TempDir(), ".cag")
}

func cagRuntimeDir() string {
	return filepath.Join(CAGHomeDir(), "runtime")
}

func DefaultRuntimePaths(repoRoot string) RuntimePaths {
	_ = repoRoot
	base := cagRuntimeDir()
	return RuntimePaths{
		BaseDir:            base,
		ReportDir:          filepath.Join(base, "reports"),
		StateFile:          filepath.Join(base, "state.json"),
		InteractionLogFile: filepath.Join(base, "interactions.jsonl"),
		LockFile:           filepath.Join(base, "gateway.lock"),
		StorageSQLitePath:  filepath.Join(base, "gateway.db"),
	}
}
