package dingtalk

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dtchatbot "github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
)

func TestOnStreamMessageToInbound(t *testing.T) {
	a := &Adapter{
		opts:  Options{FetchMaxEvents: 30},
		inbox: make(chan map[string]any, 16),
	}
	a.onStreamMessage(&dtchatbot.BotCallbackDataModel{
		MsgId:            "m-1",
		SenderStaffId:    "u-1",
		SenderNick:       "Alice",
		ConversationId:   "cid-1",
		ConversationType: "1",
		IsInAtList:       false,
		Text:             dtchatbot.BotCallbackDataTextModel{Content: "hello"},
		CreateAt:         1772690000,
	})

	msgs, err := a.Fetch()
	if err != nil {
		t.Fatalf("fetch error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ID != "m-1" {
		t.Fatalf("unexpected id: %s", msgs[0].ID)
	}
	if msgs[0].Sender != "u-1" {
		t.Fatalf("unexpected sender: %s", msgs[0].Sender)
	}
	if msgs[0].Text != "hello" {
		t.Fatalf("unexpected text: %s", msgs[0].Text)
	}
	if msgs[0].Metadata["sender_name"] != "Alice" {
		t.Fatalf("unexpected sender_name metadata: %v", msgs[0].Metadata["sender_name"])
	}
	if msgs[0].Metadata["sender_staff_id"] != "u-1" {
		t.Fatalf("unexpected sender_staff_id metadata: %v", msgs[0].Metadata["sender_staff_id"])
	}
}

func TestSendFallbackToSessionWebhookWhenAPIFails(t *testing.T) {
	hit := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	a := &Adapter{
		opts:         Options{SendMode: "api", SendTimeoutSec: 2},
		httpClient:   &http.Client{},
		inbox:        make(chan map[string]any, 1),
		webhookByMsg: map[string]string{"m-1": srv.URL},
		webhookOrder: []string{"m-1"},
		profileCache: map[string]cachedProfile{},
	}
	if err := a.Send("done", "u-1", "ack-m-1", ""); err != nil {
		t.Fatalf("send fallback failed: %v", err)
	}
	if hit != 1 {
		t.Fatalf("expected webhook fallback hit=1, got %d", hit)
	}
}

func TestOnStreamMessageUsesContentTextFallback(t *testing.T) {
	a := &Adapter{
		opts:         Options{FetchMaxEvents: 30},
		inbox:        make(chan map[string]any, 16),
		webhookByMsg: map[string]string{},
		webhookOrder: []string{},
		profileCache: map[string]cachedProfile{},
	}
	a.onStreamMessage(&dtchatbot.BotCallbackDataModel{
		MsgId:            "m-2",
		SenderStaffId:    "u-2",
		SenderNick:       "Bob",
		ConversationId:   "cid-2",
		ConversationType: "1",
		Content:          map[string]any{"text": "from content"},
	})
	msgs, err := a.Fetch()
	if err != nil {
		t.Fatalf("fetch error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if strings.TrimSpace(msgs[0].Text) != "from content" {
		t.Fatalf("unexpected text from content fallback: %q", msgs[0].Text)
	}
}
