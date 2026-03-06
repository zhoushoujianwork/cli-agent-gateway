package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cli-agent-gateway/internal/infra/envfile"
)

const defaultGatewaydAddr = "127.0.0.1:58473"

var deprecatedEnvKeys = map[string]struct{}{
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
	"RECOVER_UNFINISHED_TASKS_AS_FAILED": {},
}

func WriteDefaultEnv(repoRoot, workdir string) (string, error) {
	envPath := filepath.Join(repoRoot, ".env")
	existing, err := envfile.Parse(envPath)
	if err != nil {
		return "", err
	}

	values := map[string]string{
		"CODEX_WORKDIR":                     workdir,
		"CHANNEL_TYPE":                      "command",
		"SMS_FETCH_CMD":                     "printf '[]'",
		"SMS_SEND_CMD":                      "true",
		"REMOTE_USER_ID":                    "tester",
		"ALLOWED_FROM":                      "tester",
		"ACP_AGENT_CMD":                     "codex-acp",
		"AGENT_TIMEOUT_SEC":                 "1800",
		"ACP_PERMISSION_POLICY":             "auto_allow",
		"CAG_GO_DEBUG":                      "1",
		"ACP_INITIALIZE_TIMEOUT_SEC":        "30",
		"ACP_SESSION_NEW_TIMEOUT_SEC":       "90",
		"ACP_SESSION_NEW_RETRIES":           "2",
		"ACP_SESSION_NEW_RETRY_BACKOFF_SEC": "1",
		"PROCESS_ONLY_LATEST":               "0",
		"POLL_INTERVAL_SEC":                 "5",
		"PROGRESS_NOTIFY_INTERVAL_SEC":      "60",
		"SMS_REPLY_MAX_CHARS":               "8000",
		"REPLY_STYLE_ENABLED":               "1",
		"REPLY_STYLE_PROMPT":                "请用简洁聊天格式回复：短句优先；先结论后细节；尽量 3-6 行；避免长段落、复杂 Markdown、表格；代码只给最小必要片段；状态词清晰（进行中/阻塞/完成）。",
		"SHOW_TOOL_TRACE":                   "0",
		"DEBUG_TRACE_CHAIN":                 "0",
		"DEBUG_ACP_EVENTS":                  "0",
		"DEBUG_ACP_LOG_CHUNKS":              "0",
		"DEBUG_ACP_EVENT_PAYLOAD_CHARS":     "280",
		"TOOL_PROGRESS_NOTIFY_ENABLED":      "1",
		"STORAGE_BACKEND":                   "sqlite",
		"STORAGE_SQLITE_PATH":               ".agent_gateway.db",
		"IMESSAGE_FETCH_CMD":                "imsg fetch --json",
		"IMESSAGE_SEND_CMD":                 "imsg send",
		"IMESSAGE_FETCH_TIMEOUT_SEC":        "30",
		"IMESSAGE_SEND_TIMEOUT_SEC":         "30",
		"DINGTALK_FETCH_MAX_EVENTS":         "30",
		"DINGTALK_DM_POLICY":                "allowlist",
		"DINGTALK_GROUP_POLICY":             "allowlist",
		"DINGTALK_ALLOWED_FROM":             "",
		"DINGTALK_GROUP_ALLOWLIST":          "",
		"DINGTALK_REQUIRE_MENTION_IN_GROUP": "0",
		"DINGTALK_SEND_MODE":                "api",
		"DINGTALK_SEND_MSGTYPE":             "markdown",
		"DINGTALK_SEND_TIMEOUT_SEC":         "10",
		"DINGTALK_MARKDOWN_TITLE":           "CLI Agent Gateway",
		"DINGTALK_PRETTY_STATUS":            "1",
		"DINGTALK_BOT_WEBHOOK":              "",
		"DINGTALK_BOT_SECRET":               "",
		"DINGTALK_APP_KEY":                  "",
		"DINGTALK_APP_SECRET":               "",
		"DINGTALK_AGENT_ID":                 "",
		"DINGTALK_DEFAULT_TO_USER":          "",
		"DINGTALK_TOKEN_URL":                "https://api.dingtalk.com/v1.0/oauth2/accessToken",
		"DINGTALK_SEND_URL":                 "https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2",
		"LOCK_FILE":                         ".cli_agent_gateway.lock",
		"STATE_FILE":                        ".agent_gateway_state.json",
		"REPORT_DIR":                        ".agent_gateway_reports",
		"INTERACTION_LOG_FILE":              ".agent_gateway_interactions.jsonl",
	}

	for k, v := range existing {
		if _, deprecated := deprecatedEnvKeys[k]; deprecated {
			continue
		}
		if _, known := values[k]; !known {
			values[k] = v
		}
	}

	ordered := []string{
		"CODEX_WORKDIR",
		"CHANNEL_TYPE",
		"SMS_FETCH_CMD",
		"SMS_SEND_CMD",
		"REMOTE_USER_ID",
		"ALLOWED_FROM",
		"ACP_AGENT_CMD",
		"AGENT_TIMEOUT_SEC",
		"ACP_PERMISSION_POLICY",
		"CAG_GO_DEBUG",
		"ACP_INITIALIZE_TIMEOUT_SEC",
		"ACP_SESSION_NEW_TIMEOUT_SEC",
		"ACP_SESSION_NEW_RETRIES",
		"ACP_SESSION_NEW_RETRY_BACKOFF_SEC",
		"PROCESS_ONLY_LATEST",
		"POLL_INTERVAL_SEC",
		"PROGRESS_NOTIFY_INTERVAL_SEC",
		"SMS_REPLY_MAX_CHARS",
		"REPLY_STYLE_ENABLED",
		"REPLY_STYLE_PROMPT",
		"SHOW_TOOL_TRACE",
		"DEBUG_TRACE_CHAIN",
		"DEBUG_ACP_EVENTS",
		"DEBUG_ACP_LOG_CHUNKS",
		"DEBUG_ACP_EVENT_PAYLOAD_CHARS",
		"TOOL_PROGRESS_NOTIFY_ENABLED",
		"STORAGE_BACKEND",
		"STORAGE_SQLITE_PATH",
		"IMESSAGE_FETCH_CMD",
		"IMESSAGE_SEND_CMD",
		"IMESSAGE_FETCH_TIMEOUT_SEC",
		"IMESSAGE_SEND_TIMEOUT_SEC",
		"DINGTALK_FETCH_MAX_EVENTS",
		"DINGTALK_DM_POLICY",
		"DINGTALK_GROUP_POLICY",
		"DINGTALK_ALLOWED_FROM",
		"DINGTALK_GROUP_ALLOWLIST",
		"DINGTALK_REQUIRE_MENTION_IN_GROUP",
		"DINGTALK_SEND_MODE",
		"DINGTALK_SEND_MSGTYPE",
		"DINGTALK_SEND_TIMEOUT_SEC",
		"DINGTALK_MARKDOWN_TITLE",
		"DINGTALK_PRETTY_STATUS",
		"DINGTALK_BOT_WEBHOOK",
		"DINGTALK_BOT_SECRET",
		"DINGTALK_APP_KEY",
		"DINGTALK_APP_SECRET",
		"DINGTALK_AGENT_ID",
		"DINGTALK_DEFAULT_TO_USER",
		"DINGTALK_TOKEN_URL",
		"DINGTALK_SEND_URL",
		"LOCK_FILE",
		"STATE_FILE",
		"REPORT_DIR",
		"INTERACTION_LOG_FILE",
	}
	headers := []string{
		fmt.Sprintf("# Auto-generated by gateway-cli config at %s", time.Now().Format(time.RFC3339)),
		"# Go-native config (no Python bridge)",
	}
	if err := envfile.Write(envPath, values, ordered, headers); err != nil {
		return "", err
	}
	return envPath, nil
}

func WriteUserEnv(gatewaydAddr string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	envPath := filepath.Join(home, ".cag", ".env")
	existing, err := envfile.Parse(envPath)
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(gatewaydAddr)
	if addr == "" {
		addr = defaultGatewaydAddr
	}
	values := map[string]string{
		"GATEWAYD_ADDR": addr,
	}
	for k, v := range existing {
		if _, known := values[k]; !known {
			values[k] = v
		}
	}
	ordered := []string{
		"GATEWAYD_ADDR",
	}
	headers := []string{
		fmt.Sprintf("# Auto-generated by gateway-cli config --global at %s", time.Now().Format(time.RFC3339)),
		"# User-level defaults loaded by cag from ~/.cag/.env",
	}
	if err := envfile.Write(envPath, values, ordered, headers); err != nil {
		return "", err
	}
	return envPath, nil
}
