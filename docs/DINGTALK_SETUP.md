# DingTalk Setup (Stream)

## 1. Enable DingTalk channel commands

In `.env` set:

```bash
SMS_FETCH_CMD="builtin:dingtalk-stream"
SMS_SEND_CMD="python3 /abs/path/to/src/channels/dingtalk.py send"
DINGTALK_SEND_MODE=api
DINGTALK_SEND_MSGTYPE=markdown|card|text
DINGTALK_HUMAN_REPLY_ONLY=1
DINGTALK_HUMAN_STYLE_PROMPT=Reply like a real human in chat: concise, natural, a little funny, no status labels, no task IDs, no markdown headers.
DINGTALK_PRETTY_STATUS=1
DINGTALK_SHOW_META=0
DINGTALK_MARKDOWN_TITLE=CLI Agent Gateway
DINGTALK_CARD_TEMPLATE_ID=StandardCard
DINGTALK_CARD_FALLBACK_MARKDOWN=1
DINGTALK_CARD_SINGLE_MESSAGE=1
DINGTALK_CARD_UPDATE_FALLBACK_SEND=1
DINGTALK_CARD_UPDATE_RETRY_SEC=1,2,4
DINGTALK_CARD_HISTORY_FILE=logs/dingtalk_card_state.json
DINGTALK_CARD_HISTORY_MAX=12
DINGTALK_CARD_API_LOG=logs/dingtalk_card_api.jsonl
RECOVER_UNFINISHED_TASKS_AS_FAILED=1
```

## 2. Inbound stream listener

Gateway embeds DingTalk Stream SDK when `CHANNEL_TYPE=dingtalk`.
No callback HTTP server and no queue file are required.

## 3. Outbound send mode

### API mode (recommended; two-way primary path)

```bash
DINGTALK_SEND_MODE=api
DINGTALK_APP_KEY=...
DINGTALK_APP_SECRET=...
DINGTALK_AGENT_ID=123456
```

Gateway will use `SMS_TO` as the target DingTalk user id (`senderStaffId`).

### Webhook mode (system notification only)

Set webhook config:

```bash
DINGTALK_BOT_WEBHOOK=https://oapi.dingtalk.com/robot/send?access_token=...
DINGTALK_BOT_SECRET=SEC...
DINGTALK_SEND_MODE=webhook
```

`dingtalk.py send` treats `errcode=0` as success.

## 4. Access policy

```bash
DINGTALK_DM_POLICY=allowlist|open|disabled
DINGTALK_GROUP_POLICY=allowlist|open|disabled
DINGTALK_ALLOWED_FROM=staffA,staffB
DINGTALK_GROUP_ALLOWLIST=cidA,cidB
DINGTALK_REQUIRE_MENTION_IN_GROUP=0
DINGTALK_DEBUG_USER_PROFILE=1
DINGTALK_STREAM_DEBUG=1
```

## 5. Local smoke test

```bash
# Send test (api/webhook depends on DINGTALK_SEND_MODE)
PYTHONPATH=src python3 src/channels/dingtalk.py send 'gateway ping'
```
