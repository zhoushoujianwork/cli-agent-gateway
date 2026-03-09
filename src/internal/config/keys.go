package config

import "strings"

const (
	ScopeRepoEnv   = "repo_env"
	ScopeUserEnv   = "user_env"
	ScopeRuntimeDB = "runtime_db"

	SourceProcessEnv = "process_env"
	SourceRepoEnv    = "repo_env"
	SourceUserEnv    = "user_env"
	SourceRuntimeDB  = "runtime_db"
	SourceDefault    = "default"
)

const (
	defaultChannelType       = "command"
	defaultFetchCmd          = "printf '[]'"
	defaultSendCmd           = "true"
	defaultIMessageFetchCmd  = "imsg fetch --json"
	defaultIMessageSendCmd   = "imsg send"
	defaultACPAgentCmd       = "codex-acp"
	defaultPermissionPolicy  = "auto_allow"
	defaultReplyStylePrompt  = "请用简洁聊天格式回复：短句优先；先结论后细节；尽量 3-6 行；避免长段落、复杂 Markdown、表格；代码只给最小必要片段；状态词清晰（进行中/阻塞/完成）。"
	defaultDingTalkSendURL   = "https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2"
	defaultDingTalkTokenURL  = "https://api.dingtalk.com/v1.0/oauth2/accessToken"
	defaultStorageBackend    = "sqlite"
	defaultStorageSQLitePath = ".agent_gateway.db"
	defaultLockFile          = ".cli_agent_gateway.lock"
	defaultStateFile         = ".agent_gateway_state.json"
	defaultReportDir         = ".agent_gateway_reports"
	defaultInteractionLog    = ".agent_gateway_interactions.jsonl"
)

type KeySpec struct {
	Key     string
	Scope   string
	Default string
}

type Entry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Scope  string `json:"scope"`
	Source string `json:"source"`
}

var orderedSpecs = []KeySpec{
	{Key: "CHANNEL_TYPE", Scope: ScopeRepoEnv, Default: defaultChannelType},
	{Key: "ACP_AGENT_CMD", Scope: ScopeRepoEnv, Default: defaultACPAgentCmd},
	{Key: "ACP_PERMISSION_POLICY", Scope: ScopeRepoEnv, Default: defaultPermissionPolicy},
	{Key: "SMS_FETCH_CMD", Scope: ScopeRepoEnv, Default: defaultFetchCmd},
	{Key: "SMS_SEND_CMD", Scope: ScopeRepoEnv, Default: defaultSendCmd},
	{Key: "REMOTE_USER_ID", Scope: ScopeRepoEnv, Default: "tester"},
	{Key: "ALLOWED_FROM", Scope: ScopeRepoEnv, Default: "tester"},
	{Key: "IMESSAGE_FETCH_CMD", Scope: ScopeRepoEnv, Default: defaultIMessageFetchCmd},
	{Key: "IMESSAGE_SEND_CMD", Scope: ScopeRepoEnv, Default: defaultIMessageSendCmd},
	{Key: "DINGTALK_APP_KEY", Scope: ScopeUserEnv},
	{Key: "DINGTALK_APP_SECRET", Scope: ScopeUserEnv},
	{Key: "DINGTALK_AGENT_ID", Scope: ScopeUserEnv},
	{Key: "DINGTALK_BOT_WEBHOOK", Scope: ScopeUserEnv},
	{Key: "DINGTALK_BOT_SECRET", Scope: ScopeUserEnv},
	{Key: "DINGTALK_TOKEN_URL", Scope: ScopeUserEnv, Default: defaultDingTalkTokenURL},
	{Key: "DINGTALK_SEND_URL", Scope: ScopeUserEnv, Default: defaultDingTalkSendURL},
	{Key: "CAG_GO_DEBUG", Scope: ScopeRepoEnv, Default: "1"},
	{Key: "STORAGE_BACKEND", Scope: ScopeRepoEnv, Default: defaultStorageBackend},
	{Key: "STORAGE_SQLITE_PATH", Scope: ScopeRepoEnv, Default: defaultStorageSQLitePath},
	{Key: "LOCK_FILE", Scope: ScopeRepoEnv, Default: defaultLockFile},
	{Key: "STATE_FILE", Scope: ScopeRepoEnv, Default: defaultStateFile},
	{Key: "REPORT_DIR", Scope: ScopeRepoEnv, Default: defaultReportDir},
	{Key: "INTERACTION_LOG_FILE", Scope: ScopeRepoEnv, Default: defaultInteractionLog},
	{Key: "GATEWAYD_ADDR", Scope: ScopeUserEnv, Default: defaultGatewaydAddr},
	{Key: "AGENT_TIMEOUT_SEC", Scope: ScopeRuntimeDB, Default: "1800"},
	{Key: "ACP_INITIALIZE_TIMEOUT_SEC", Scope: ScopeRuntimeDB, Default: "30"},
	{Key: "ACP_SESSION_NEW_TIMEOUT_SEC", Scope: ScopeRuntimeDB, Default: "90"},
	{Key: "ACP_SESSION_NEW_RETRIES", Scope: ScopeRuntimeDB, Default: "2"},
	{Key: "ACP_SESSION_NEW_RETRY_BACKOFF_SEC", Scope: ScopeRuntimeDB, Default: "1"},
	{Key: "PROCESS_ONLY_LATEST", Scope: ScopeRuntimeDB, Default: "0"},
	{Key: "POLL_INTERVAL_SEC", Scope: ScopeRuntimeDB, Default: "5"},
	{Key: "REPLY_STYLE_ENABLED", Scope: ScopeRuntimeDB, Default: "1"},
	{Key: "REPLY_STYLE_PROMPT", Scope: ScopeRuntimeDB, Default: defaultReplyStylePrompt},
	{Key: "SHOW_TOOL_TRACE", Scope: ScopeRuntimeDB, Default: "0"},
	{Key: "TOOL_PROGRESS_NOTIFY_ENABLED", Scope: ScopeRuntimeDB, Default: "1"},
	{Key: "IMESSAGE_FETCH_TIMEOUT_SEC", Scope: ScopeRuntimeDB, Default: "30"},
	{Key: "IMESSAGE_SEND_TIMEOUT_SEC", Scope: ScopeRuntimeDB, Default: "30"},
	{Key: "DINGTALK_FETCH_MAX_EVENTS", Scope: ScopeRuntimeDB, Default: "30"},
	{Key: "DINGTALK_DM_POLICY", Scope: ScopeRuntimeDB, Default: "allowlist"},
	{Key: "DINGTALK_GROUP_POLICY", Scope: ScopeRuntimeDB, Default: "allowlist"},
	{Key: "DINGTALK_ALLOWED_FROM", Scope: ScopeRuntimeDB},
	{Key: "DINGTALK_GROUP_ALLOWLIST", Scope: ScopeRuntimeDB},
	{Key: "DINGTALK_REQUIRE_MENTION_IN_GROUP", Scope: ScopeRuntimeDB, Default: "1"},
	{Key: "DINGTALK_SEND_MODE", Scope: ScopeUserEnv, Default: "api"},
	{Key: "DINGTALK_SEND_MSGTYPE", Scope: ScopeRuntimeDB, Default: "markdown"},
	{Key: "DINGTALK_SEND_TIMEOUT_SEC", Scope: ScopeRuntimeDB, Default: "10"},
	{Key: "DINGTALK_MARKDOWN_TITLE", Scope: ScopeRuntimeDB, Default: "CLI Agent Gateway"},
	{Key: "DINGTALK_PRETTY_STATUS", Scope: ScopeRuntimeDB, Default: "1"},
	{Key: "DINGTALK_DEFAULT_TO_USER", Scope: ScopeUserEnv},
}

var specByKey = func() map[string]KeySpec {
	out := make(map[string]KeySpec, len(orderedSpecs))
	for _, spec := range orderedSpecs {
		out[spec.Key] = spec
	}
	return out
}()

var removedEnvKeys = map[string]struct{}{
	"CODEX_WORKDIR":                      {},
	"DINGTALK_CALLBACK_AES_KEY":          {},
	"DINGTALK_CALLBACK_SECRET":           {},
	"DINGTALK_CARD_FALLBACK_MARKDOWN":    {},
	"DINGTALK_CARD_HISTORY_FILE":         {},
	"DINGTALK_CARD_HISTORY_MAX":          {},
	"DINGTALK_CARD_SINGLE_MESSAGE":       {},
	"DINGTALK_CARD_TEMPLATE_ID":          {},
	"DINGTALK_CARD_UPDATE_FALLBACK_SEND": {},
	"DINGTALK_CARD_UPDATE_RETRY_SEC":     {},
	"DINGTALK_DEBUG_USER_PROFILE":        {},
	"DINGTALK_HUMAN_REPLY_ONLY":          {},
	"DINGTALK_HUMAN_STYLE_PROMPT":        {},
	"DINGTALK_QUEUE_FILE":                {},
	"DINGTALK_ROBOT_CODE":                {},
	"DINGTALK_SHOW_META":                 {},
	"DEBUG_ACP_EVENT_PAYLOAD_CHARS":      {},
	"DEBUG_ACP_EVENTS":                   {},
	"DEBUG_ACP_LOG_CHUNKS":               {},
	"DEBUG_TRACE_CHAIN":                  {},
	"PROGRESS_NOTIFY_INTERVAL_SEC":       {},
	"RECOVER_UNFINISHED_TASKS_AS_FAILED": {},
	"SMS_REPLY_MAX_CHARS":                {},
}

func KnownKeys() []string {
	out := make([]string, 0, len(orderedSpecs))
	for _, spec := range orderedSpecs {
		out = append(out, spec.Key)
	}
	return out
}

func LookupSpec(key string) (KeySpec, bool) {
	spec, ok := specByKey[normalizeKey(key)]
	return spec, ok
}

func normalizeKey(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
}
