package main

import (
	"testing"
	"time"
)

func TestDetectRecentDingTalkRuntimeWarningFromLines(t *testing.T) {
	now := time.Date(2026, 3, 6, 15, 0, 0, 0, time.UTC)
	lines := []string{
		"[2026-03-06T14:10:00Z] dingtalk-stream stream connected",
		"[2026-03-06T14:56:36Z] dingtalk-stream api send failed for message_id=ack-x target=u-1 err=api send failed: errcode=88 errmsg=ding talk error[subcode=60020,submsg=访问ip不在白名单之中] request_id=abc, fallback=session_webhook",
	}
	got := detectRecentDingTalkRuntimeWarningFromLines(lines, now, 20*time.Minute)
	if got == "" {
		t.Fatalf("expected runtime warning, got empty")
	}
}

func TestDetectRecentDingTalkRuntimeWarningFromLinesIgnoresOld(t *testing.T) {
	now := time.Date(2026, 3, 6, 15, 0, 0, 0, time.UTC)
	lines := []string{
		"[2026-03-06T10:00:00Z] dingtalk-stream api send failed for message_id=ack-x target=u-1 err=api send failed: errcode=88 errmsg=ding talk error[subcode=60020,submsg=访问ip不在白名单之中] request_id=abc, fallback=session_webhook",
	}
	got := detectRecentDingTalkRuntimeWarningFromLines(lines, now, 20*time.Minute)
	if got != "" {
		t.Fatalf("expected old warning ignored, got=%q", got)
	}
}

func TestIsDingTalkRuntimeErrorLine(t *testing.T) {
	if !isDingTalkRuntimeErrorLine("[2026-03-06T14:56:36Z] dingtalk-stream api send failed ... fallback=session_webhook") {
		t.Fatalf("expected fallback error line to match")
	}
	if !isDingTalkRuntimeErrorLine("[WARN] send startup greeting failed: api send failed: errcode=88 ... subcode=60020") {
		t.Fatalf("expected startup errcode line to match")
	}
	if isDingTalkRuntimeErrorLine("[INFO] send ack ok msg_id=x") {
		t.Fatalf("did not expect unrelated info line to match")
	}
}
