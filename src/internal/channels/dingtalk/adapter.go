package dingtalk

import (
	"bufio"
	"bytes"
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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cli-agent-gateway/internal/core"
)

type Options struct {
	RepoRoot              string
	QueueFile             string
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
	opts       Options
	mu         sync.Mutex
	offset     int64
	httpClient *http.Client
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
	if strings.TrimSpace(opts.QueueFile) == "" {
		opts.QueueFile = ".dingtalk_inbox.jsonl"
	}
	return &Adapter{
		opts:       opts,
		httpClient: &http.Client{Timeout: time.Duration(opts.SendTimeoutSec) * time.Second},
	}
}

func (a *Adapter) Fetch() ([]core.InboundMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	queuePath := resolvePath(a.opts.RepoRoot, a.opts.QueueFile)
	f, err := os.Open(queuePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []core.InboundMessage{}, nil
		}
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err == nil && st.Size() < a.offset {
		a.offset = 0
	}
	if _, err := f.Seek(a.offset, io.SeekStart); err != nil {
		return nil, err
	}

	sc := bufio.NewScanner(f)
	out := make([]core.InboundMessage, 0)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var node map[string]any
		if err := json.Unmarshal([]byte(line), &node); err != nil {
			continue
		}
		if !a.shouldKeep(node) {
			continue
		}
		msg := a.normalize(node)
		if msg != nil {
			out = append(out, *msg)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	pos, _ := f.Seek(0, io.SeekCurrent)
	a.offset = pos

	if len(out) > a.opts.FetchMaxEvents {
		out = out[len(out)-a.opts.FetchMaxEvents:]
	}
	return out, nil
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
			return false
		}
		if groupPolicy == "allowlist" && len(a.opts.GroupAllowlist) > 0 {
			if _, ok := a.opts.GroupAllowlist[conversationID]; !ok {
				return false
			}
		}
		if a.opts.RequireMentionInGroup {
			isAtBot := anyBool(node["isAtBot"]) || anyBool(node["atBot"])
			if !isAtBot {
				return false
			}
		}
		return true
	}

	if dmPolicy == "disabled" {
		return false
	}
	if dmPolicy == "allowlist" && len(a.opts.AllowedFrom) > 0 {
		_, ok := a.opts.AllowedFrom[sender]
		return ok
	}
	return true
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

func resolvePath(repoRoot, p string) string {
	if strings.TrimSpace(p) == "" {
		return filepath.Join(repoRoot, ".dingtalk_inbox.jsonl")
	}
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(h, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(repoRoot, p)
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
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
