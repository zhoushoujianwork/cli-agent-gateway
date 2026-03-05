package dingtalk

import (
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
}
