# DingTalk Channel Plan

## Goal

Implement DingTalk as the next production channel after iMessage, reusing the same ACP runtime and session model.

## Scope (MVP)

- `dingtalk_fetch.py`: pull inbound messages and normalize to `{id, from, text, ts, thread_id}`.
- `dingtalk_send.py`: send ack/progress/final replies.
- Signature verification and sender allowlist.
- Basic thread/session mapping by `conversationId`.

## Config draft

```bash
DINGTALK_APP_KEY=...
DINGTALK_APP_SECRET=...
DINGTALK_AGENT_ID=...
DINGTALK_WEBHOOK_MODE=poll|callback
DINGTALK_ALLOWED_FROM=userA,userB
```

## Risks

- DingTalk bot permission and enterprise app approval flow.
- Message rate limits and callback retry behavior.
- Rich text compatibility vs plain text fallback.
