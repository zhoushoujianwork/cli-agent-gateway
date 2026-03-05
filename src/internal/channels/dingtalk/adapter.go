package dingtalk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"cli-agent-gateway/internal/core"

	dtchatbot "github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	dtclient "github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
)

type Options struct {
	RepoRoot              string
	FetchMaxEvents        int
	DMPolicy              string
	GroupPolicy           string
	AllowedFrom           map[string]struct{}
	GroupAllowlist        map[string]struct{}
	RequireMentionInGroup bool
	SendMode              string
	SendMsgType           string
	SendTimeoutSec        int
	MarkdownTitle         string
	PrettyStatus          bool
	BotWebhook            string
	BotSecret             string
	AppKey                string
	AppSecret             string
	AgentID               string
	DefaultToUser         string
	TokenURL              string
	SendURL               string
}

type Adapter struct {
	opts         Options
	httpClient   *http.Client
	inbox        chan map[string]any
	streamClient *dtclient.StreamClient
	startErr     error
	mu           sync.Mutex
}

func NewAdapter(opts Options) *Adapter {
	if opts.FetchMaxEvents <= 0 {
		opts.FetchMaxEvents = 30
	}
	if opts.SendTimeoutSec <= 0 {
		opts.SendTimeoutSec = 10
	}
	if strings.TrimSpace(opts.SendMode) == "" {
		opts.SendMode = "api"
	}
	if strings.TrimSpace(opts.SendMsgType) == "" {
		opts.SendMsgType = "markdown"
	}
	if strings.TrimSpace(opts.MarkdownTitle) == "" {
		opts.MarkdownTitle = "CLI Agent Gateway"
	}
	a := &Adapter{
		opts:       opts,
		httpClient: &http.Client{Timeout: time.Duration(opts.SendTimeoutSec) * time.Second},
		inbox:      make(chan map[string]any, max(128, opts.FetchMaxEvents*8)),
	}
	a.startStream()
	return a
}

func (a *Adapter) startStream() {
	appKey := strings.TrimSpace(a.opts.AppKey)
	appSecret := strings.TrimSpace(a.opts.AppSecret)
	a.logStartupSummary()
	if appKey == "" || appSecret == "" {
		a.startErr = fmt.Errorf("dingtalk stream requires DINGTALK_APP_KEY and DINGTALK_APP_SECRET")
		a.logf("stream init failed: %v", a.startErr)
		return
	}

	a.logf("stream connecting app_key=%s****", shortMask(appKey))
	cli := dtclient.NewStreamClient(
		dtclient.WithAppCredential(dtclient.NewAppCredentialConfig(appKey, appSecret)),
	)
	a.logf("stream register chatbot callback router")
	cli.RegisterChatBotCallbackRouter(func(ctx context.Context, data *dtchatbot.BotCallbackDataModel) ([]byte, error) {
		a.onStreamMessage(data)
		return []byte(""), nil
	})
	if err := cli.Start(context.Background()); err != nil {
		a.startErr = fmt.Errorf("dingtalk stream start failed: %w", err)
		a.logf("stream connect failed: %v", err)
		return
	}
	a.streamClient = cli
	a.logf("stream connected")
}

func (a *Adapter) logStartupSummary() {
	a.logf(
		"startup config channel=dingtalk fetch_max=%d dm_policy=%s group_policy=%s require_at=%v allowed_from=%d group_allowlist=%d send_mode=%s send_msgtype=%s send_timeout=%ds pretty_status=%v app_key=%s**** app_secret_set=%v agent_id_set=%v default_to_set=%v token_url=%s send_url=%s",
		a.opts.FetchMaxEvents,
		strings.TrimSpace(a.opts.DMPolicy),
		strings.TrimSpace(a.opts.GroupPolicy),
		a.opts.RequireMentionInGroup,
		len(a.opts.AllowedFrom),
		len(a.opts.GroupAllowlist),
		strings.TrimSpace(a.opts.SendMode),
		strings.TrimSpace(a.opts.SendMsgType),
		a.opts.SendTimeoutSec,
		a.opts.PrettyStatus,
		shortMask(strings.TrimSpace(a.opts.AppKey)),
		strings.TrimSpace(a.opts.AppSecret) != "",
		strings.TrimSpace(a.opts.AgentID) != "",
		strings.TrimSpace(a.opts.DefaultToUser) != "",
		shortText(strings.TrimSpace(a.opts.TokenURL), 80),
		shortText(strings.TrimSpace(a.opts.SendURL), 80),
	)
}

func (a *Adapter) onStreamMessage(data *dtchatbot.BotCallbackDataModel) {
	if data == nil {
		return
	}
	node := map[string]any{
		"messageId":      sanitize(data.MsgId),
		"conversationId": sanitize(data.ConversationId),
		"threadId":       sanitize(data.ConversationId),
		"senderStaffId":  sanitize(data.SenderStaffId),
		"senderId":       sanitize(data.SenderId),
		"senderName":     sanitize(data.SenderNick),
		"chatType":       sanitize(data.ConversationType),
		"isAtBot":        data.IsInAtList,
		"isGroup":        sanitize(data.ConversationType) == "2",
		"text":           sanitize(data.Text.Content),
		"ts":             strconv.FormatInt(data.CreateAt, 10),
		"sessionWebhook": sanitize(data.SessionWebhook),
	}
	if strings.TrimSpace(anyString(node["text"])) == "" {
		a.logf("stream drop msg_id=%s reason=empty_text sender=%s chat_type=%s", sanitize(data.MsgId), sanitize(data.SenderStaffId), sanitize(data.ConversationType))
		return
	}
	a.logf("stream recv msg_id=%s sender=%s chat_type=%s text=%s", sanitize(data.MsgId), sanitize(data.SenderStaffId), sanitize(data.ConversationType), shortText(sanitize(data.Text.Content), 80))
	if reason := a.dropReason(node); reason != "" {
		a.logf("stream drop msg_id=%s reason=%s sender=%s chat_type=%s text=%s", sanitize(data.MsgId), reason, sanitize(data.SenderStaffId), sanitize(data.ConversationType), shortText(sanitize(data.Text.Content), 80))
		return
	}
	select {
	case a.inbox <- node:
		a.logf("stream enqueue msg_id=%s queue_len=%d", sanitize(data.MsgId), len(a.inbox))
	default:
		a.logf("stream queue full, dropping oldest and retry enqueue msg_id=%s", sanitize(data.MsgId))
		select {
		case <-a.inbox:
		default:
		}
		select {
		case a.inbox <- node:
			a.logf("stream enqueue msg_id=%s queue_len=%d", sanitize(data.MsgId), len(a.inbox))
		default:
			a.logf("stream enqueue failed msg_id=%s", sanitize(data.MsgId))
		}
	}
}

func (a *Adapter) Fetch() ([]core.InboundMessage, error) {
	a.mu.Lock()
	startErr := a.startErr
	a.mu.Unlock()
	if startErr != nil {
		return nil, startErr
	}
	out := make([]core.InboundMessage, 0, a.opts.FetchMaxEvents)
	for {
		select {
		case node := <-a.inbox:
			msg := a.normalize(node)
			if msg != nil {
				out = append(out, *msg)
			}
		default:
			if len(out) > a.opts.FetchMaxEvents {
				out = out[len(out)-a.opts.FetchMaxEvents:]
			}
			if len(out) > 0 {
				a.logf("fetch batch size=%d", len(out))
			}
			return out, nil
		}
	}
}

func (a *Adapter) Send(text, to, messageID, reportFile string) error {
	_ = reportFile
	mode := strings.ToLower(strings.TrimSpace(a.opts.SendMode))
	if mode == "webhook" {
		return a.sendWebhook(text, messageID)
	}
	return a.sendAPI(text, to, messageID)
}

func (a *Adapter) shouldKeep(node map[string]any) bool {
	return a.dropReason(node) == ""
}

func (a *Adapter) dropReason(node map[string]any) string {
	dmPolicy := strings.ToLower(strings.TrimSpace(a.opts.DMPolicy))
	groupPolicy := strings.ToLower(strings.TrimSpace(a.opts.GroupPolicy))
	if dmPolicy == "" {
		dmPolicy = "allowlist"
	}
	if groupPolicy == "" {
		groupPolicy = "allowlist"
	}

	chatType := strings.ToLower(sanitize(anyString(node["chatType"])))
	if chatType == "" {
		chatType = strings.ToLower(sanitize(anyString(node["conversationType"])))
	}
	conversationID := sanitize(anyString(node["conversationId"]))
	if conversationID == "" {
		conversationID = sanitize(anyString(node["cid"]))
	}
	sender := sanitize(anyString(node["senderStaffId"]))
	if sender == "" {
		sender = sanitize(anyString(node["senderId"]))
	}
	if sender == "" {
		sender = sanitize(anyString(node["from"]))
	}
	isGroup := strings.Contains(chatType, "group") || chatType == "2" || anyBool(node["isGroup"])

	if isGroup {
		if groupPolicy == "disabled" {
			return "group_policy_disabled"
		}
		if groupPolicy == "allowlist" && len(a.opts.GroupAllowlist) > 0 {
			if _, ok := a.opts.GroupAllowlist[conversationID]; !ok {
				return "group_not_in_allowlist"
			}
		}
		if a.opts.RequireMentionInGroup {
			isAtBot := anyBool(node["isAtBot"]) || anyBool(node["atBot"])
			if !isAtBot {
				return "group_requires_mention"
			}
		}
		return ""
	}

	if dmPolicy == "disabled" {
		return "dm_policy_disabled"
	}
	if dmPolicy == "allowlist" && len(a.opts.AllowedFrom) > 0 {
		_, ok := a.opts.AllowedFrom[sender]
		if !ok {
			return "sender_not_in_allowlist"
		}
	}
	return ""
}

func (a *Adapter) normalize(node map[string]any) *core.InboundMessage {
	text := sanitize(anyString(node["text"]))
	if text == "" {
		text = sanitize(anyString(node["content"]))
	}
	if text == "" {
		return nil
	}

	sender := sanitize(anyString(node["senderStaffId"]))
	if sender == "" {
		sender = sanitize(anyString(node["senderId"]))
	}
	if sender == "" {
		sender = sanitize(anyString(node["from"]))
	}
	ts := sanitize(anyString(node["ts"]))
	if ts == "" {
		ts = sanitize(anyString(node["createAt"]))
	}
	conversationID := sanitize(anyString(node["conversationId"]))
	if conversationID == "" {
		conversationID = sanitize(anyString(node["cid"]))
	}
	threadID := sanitize(anyString(node["threadId"]))
	threadKey := conversationID
	if threadID != "" {
		threadKey = conversationID + ":" + threadID
	}
	msgID := sanitize(anyString(node["messageId"]))
	if msgID == "" {
		msgID = sanitize(anyString(node["msgId"]))
	}
	if msgID == "" {
		msgID = sanitize(anyString(node["id"]))
	}
	if msgID == "" {
		h := sha256.Sum256([]byte(sender + "|" + text + "|" + ts + "|" + threadKey))
		msgID = hex.EncodeToString(h[:])[:24]
	}

	meta := map[string]any{
		"conversation_id": conversationID,
		"chat_type":       sanitize(anyString(node["chatType"])),
		"is_group":        anyBool(node["isGroup"]),
		"is_at_bot":       anyBool(node["isAtBot"]),
		"sender_name":     sanitize(anyString(node["senderName"])),
	}
	if webhook := sanitize(anyString(node["sessionWebhook"])); webhook != "" {
		meta["session_webhook"] = webhook
	}

	return &core.InboundMessage{
		ID:       msgID,
		Sender:   sender,
		Text:     text,
		TS:       ts,
		Channel:  "dingtalk",
		ThreadID: threadKey,
		Metadata: meta,
	}
}

func (a *Adapter) sendWebhook(text, messageID string) error {
	if strings.TrimSpace(a.opts.BotWebhook) == "" {
		return fmt.Errorf("DINGTALK_BOT_WEBHOOK is required in webhook mode")
	}
	u := a.opts.BotWebhook
	if strings.TrimSpace(a.opts.BotSecret) != "" {
		u = signedWebhookURL(u, a.opts.BotSecret)
	}
	payload := a.buildWebhookPayload(text, messageID)
	resp, err := a.requestJSON(http.MethodPost, u, payload, nil)
	if err != nil {
		return err
	}
	if toInt(resp["errcode"]) != 0 {
		return fmt.Errorf("webhook send failed: errcode=%v errmsg=%v", resp["errcode"], resp["errmsg"])
	}
	return nil
}

func (a *Adapter) sendAPI(text, to, messageID string) error {
	token, err := a.getAccessToken()
	if err != nil {
		return err
	}
	agentID := strings.TrimSpace(a.opts.AgentID)
	if agentID == "" {
		return fmt.Errorf("DINGTALK_AGENT_ID is required in api mode")
	}
	userID := strings.TrimSpace(to)
	if userID == "" {
		userID = strings.TrimSpace(a.opts.DefaultToUser)
	}
	if userID == "" {
		return fmt.Errorf("SMS_TO or DINGTALK_DEFAULT_TO_USER is required in api mode")
	}

	sendURL := strings.TrimSpace(a.opts.SendURL)
	if sendURL == "" {
		sendURL = "https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2"
	}
	u := sendURL + "?access_token=" + url.QueryEscape(token)
	agentIDInt, err := strconv.Atoi(agentID)
	if err != nil {
		return fmt.Errorf("invalid DINGTALK_AGENT_ID: %w", err)
	}
	payload := map[string]any{
		"agent_id":    agentIDInt,
		"userid_list": userID,
		"msg":         a.buildAPIMessage(text, messageID),
	}
	resp, err := a.requestJSON(http.MethodPost, u, payload, nil)
	if err != nil {
		return err
	}
	if toInt(resp["errcode"]) != 0 {
		return fmt.Errorf("api send failed: errcode=%v errmsg=%v", resp["errcode"], resp["errmsg"])
	}
	for _, key := range []string{"invaliduser", "invalid_user_id", "forbidden_user_id", "forbidden_userid", "invalidparty", "invalid_party_id"} {
		if v := strings.TrimSpace(anyString(resp[key])); v != "" {
			return fmt.Errorf("api send not delivered: %s=%s", key, v)
		}
	}
	return nil
}

func (a *Adapter) getAccessToken() (string, error) {
	appKey := strings.TrimSpace(a.opts.AppKey)
	appSecret := strings.TrimSpace(a.opts.AppSecret)
	if appKey == "" || appSecret == "" {
		return "", fmt.Errorf("DINGTALK_APP_KEY and DINGTALK_APP_SECRET are required")
	}
	if strings.TrimSpace(a.opts.TokenURL) != "" {
		resp, err := a.requestJSON(http.MethodPost, a.opts.TokenURL, map[string]any{"appKey": appKey, "appSecret": appSecret}, nil)
		if err == nil {
			tk := strings.TrimSpace(anyString(resp["accessToken"]))
			if tk == "" {
				tk = strings.TrimSpace(anyString(resp["access_token"]))
			}
			if tk != "" {
				return tk, nil
			}
		}
	}
	legacy := "https://oapi.dingtalk.com/gettoken?" + url.Values{"appkey": {appKey}, "appsecret": {appSecret}}.Encode()
	resp, err := a.requestJSON(http.MethodGet, legacy, nil, nil)
	if err != nil {
		return "", err
	}
	if toInt(resp["errcode"]) != 0 {
		return "", fmt.Errorf("gettoken failed: errcode=%v errmsg=%v", resp["errcode"], resp["errmsg"])
	}
	token := strings.TrimSpace(anyString(resp["access_token"]))
	if token == "" {
		return "", fmt.Errorf("empty access_token")
	}
	return token, nil
}

func (a *Adapter) buildAPIMessage(text, messageID string) map[string]any {
	msgType := strings.ToLower(strings.TrimSpace(a.opts.SendMsgType))
	if msgType == "text" {
		return map[string]any{"msgtype": "text", "text": map[string]any{"content": text}}
	}
	title, body := a.buildMarkdownBody(text, messageID)
	return map[string]any{"msgtype": "markdown", "markdown": map[string]any{"title": title, "text": body}}
}

func (a *Adapter) buildWebhookPayload(text, messageID string) map[string]any {
	msgType := strings.ToLower(strings.TrimSpace(a.opts.SendMsgType))
	if msgType == "text" {
		return map[string]any{"msgtype": "text", "text": map[string]any{"content": text}}
	}
	title, body := a.buildMarkdownBody(text, messageID)
	return map[string]any{"msgtype": "markdown", "markdown": map[string]any{"title": title, "text": body}}
}

func (a *Adapter) buildMarkdownBody(text, messageID string) (string, string) {
	phase := messagePhase(messageID)
	title := strings.TrimSpace(a.opts.MarkdownTitle)
	if title == "" {
		title = "CLI Agent Gateway"
	}
	title = fmt.Sprintf("%s - %s", title, phaseLabelCN(phase))
	if !a.opts.PrettyStatus {
		return title, text
	}
	return title, fmt.Sprintf("%s\n\n%s", phaseLabelCN(phase), text)
}

func (a *Adapter) requestJSON(method, rawURL string, payload map[string]any, headers map[string]string) (map[string]any, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http status=%d body=%s", resp.StatusCode, string(raw))
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}, nil
	}
	return out, nil
}

func signedWebhookURL(baseURL, secret string) string {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	stringToSign := ts + "\n" + secret
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(stringToSign))
	sign := url.QueryEscape(base64.StdEncoding.EncodeToString(h.Sum(nil)))
	sep := "?"
	if strings.Contains(baseURL, "?") {
		sep = "&"
	}
	return baseURL + sep + "timestamp=" + ts + "&sign=" + sign
}

func messagePhase(messageID string) string {
	mid := strings.ToLower(strings.TrimSpace(messageID))
	if strings.HasPrefix(mid, "ack-") {
		return "ack"
	}
	if strings.HasPrefix(mid, "progress-") {
		return "progress"
	}
	if strings.HasPrefix(mid, "error-") {
		return "error"
	}
	return "final"
}

func phaseLabelCN(phase string) string {
	switch phase {
	case "ack":
		return "已接收"
	case "progress":
		return "处理中"
	case "error":
		return "处理失败"
	default:
		return "处理完成"
	}
}

func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func sanitize(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
}

func anyBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		vv := strings.ToLower(strings.TrimSpace(t))
		return vv == "1" || vv == "true" || vv == "yes"
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return false
	}
}

func toInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	default:
		return 0
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *Adapter) logf(format string, args ...any) {
	if !streamDebugEnabled() {
		return
	}
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(os.Stderr, "[%s] dingtalk-stream %s\n", time.Now().UTC().Format(time.RFC3339), msg)
}

func streamDebugEnabled() bool {
	v := strings.TrimSpace(os.Getenv("DINGTALK_STREAM_DEBUG"))
	if v == "" {
		return true
	}
	v = strings.ToLower(v)
	return v != "0" && v != "false" && v != "off"
}

func shortText(s string, n int) string {
	t := strings.TrimSpace(s)
	if n <= 0 || len(t) <= n {
		return t
	}
	return t[:n-3] + "..."
}

func shortMask(s string) string {
	v := strings.TrimSpace(s)
	if len(v) <= 4 {
		return v
	}
	return v[:4]
}
