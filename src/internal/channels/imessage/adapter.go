package imessage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"cli-agent-gateway/internal/core"
)

type Options struct {
	FetchCmd        string
	SendCmd         string
	FetchTimeoutSec int
	SendTimeoutSec  int
}

type Adapter struct {
	opts Options
}

func NewAdapter(opts Options) *Adapter {
	if opts.FetchTimeoutSec <= 0 {
		opts.FetchTimeoutSec = 30
	}
	if opts.SendTimeoutSec <= 0 {
		opts.SendTimeoutSec = 30
	}
	return &Adapter{opts: opts}
}

func (a *Adapter) Fetch() ([]core.InboundMessage, error) {
	cmd := strings.TrimSpace(a.opts.FetchCmd)
	if cmd == "" {
		return nil, errors.New("empty IMESSAGE_FETCH_CMD")
	}
	out, err := runShell(cmd, nil, a.opts.FetchTimeoutSec)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return []core.InboundMessage{}, nil
	}

	payload := make([]map[string]any, 0)
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		var single map[string]any
		if err2 := json.Unmarshal([]byte(raw), &single); err2 == nil {
			payload = append(payload, single)
		} else {
			for _, line := range strings.Split(raw, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var item map[string]any
				if err3 := json.Unmarshal([]byte(line), &item); err3 == nil {
					payload = append(payload, item)
				}
			}
		}
	}

	result := make([]core.InboundMessage, 0, len(payload))
	for _, node := range payload {
		text := sanitize(anyToString(first(node, "text", "content", "body")))
		if text == "" {
			continue
		}
		sender := sanitize(anyToString(first(node, "from", "sender", "phone", "handle")))
		ts := sanitize(anyToString(first(node, "ts", "time", "created_at")))
		threadID := sanitize(anyToString(first(node, "thread_id", "chat_id", "conversation_id")))
		id := sanitize(anyToString(first(node, "id", "message_id")))
		if id == "" {
			h := sha256.Sum256([]byte(sender + "|" + text + "|" + ts + "|" + threadID))
			id = hex.EncodeToString(h[:])[:24]
		}
		result = append(result, core.InboundMessage{
			ID:       id,
			Sender:   sender,
			Text:     text,
			TS:       ts,
			Channel:  "imessage",
			ThreadID: threadID,
			Metadata: map[string]any{},
		})
	}
	return result, nil
}

func (a *Adapter) Send(text, to, messageID, reportFile string) error {
	cmd := strings.TrimSpace(a.opts.SendCmd)
	if cmd == "" {
		return errors.New("empty IMESSAGE_SEND_CMD")
	}
	env := map[string]string{
		"SMS_TO":          to,
		"SMS_MESSAGE_ID":  messageID,
		"SMS_REPORT_FILE": reportFile,
	}
	fullCmd := cmd + " " + shellQuote(text)
	_, err := runShell(fullCmd, env, a.opts.SendTimeoutSec)
	return err
}

func runShell(command string, extraEnv map[string]string, timeoutSec int) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("empty command")
	}
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	cmd := exec.Command("/bin/sh", "-lc", command)
	env := os.Environ()
	for k, v := range extraEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	timer := time.AfterFunc(time.Duration(timeoutSec)*time.Second, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	defer timer.Stop()

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cmd failed: %w output=%s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func anyToString(v any) string {
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

func sanitize(v string) string {
	return strings.TrimSpace(strings.ReplaceAll(v, "\x00", ""))
}

func first(node map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := node[k]; ok {
			return v
		}
	}
	return nil
}
