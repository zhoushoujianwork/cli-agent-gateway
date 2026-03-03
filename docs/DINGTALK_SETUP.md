# DingTalk Setup (Bi-directional)

## 1. Enable DingTalk channel commands

In `.env` set:

```bash
SMS_FETCH_CMD="python3 /abs/path/to/src/channels/dingtalk.py fetch"
SMS_SEND_CMD="python3 /abs/path/to/src/channels/dingtalk.py send"
DINGTALK_SEND_MODE=api
```

## 2. Inbound queue file

`dingtalk.py fetch` reads JSONL events from `DINGTALK_QUEUE_FILE`.
Each line should be a JSON object, example:

```json
{"messageId":"m-1001","conversationId":"cid123","senderStaffId":"staffA","text":"hello","ts":"2026-03-02T00:00:00Z","chatType":"group","isAtBot":true}
```

Supported inbound fields:

- `messageId` / `id`
- `conversationId` / `cid`
- `senderStaffId` / `senderId` / `from`
- `text` / `content` / `msg`
- `ts` / `createAt` / `timestamp`
- `chatType` / `conversationType` / `isGroup`
- `isAtBot` / `atBot`

## 3. Inbound callback server (required for two-way)

Start callback receiver:

```bash
PYTHONPATH=src python3 src/channels/dingtalk.py callback-server
```

Configure DingTalk event subscription callback URL to this server.
Incoming events are normalized and appended into `DINGTALK_QUEUE_FILE`.

## 4. Outbound send mode

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

## 5. Access policy

```bash
DINGTALK_DM_POLICY=allowlist|open|disabled
DINGTALK_GROUP_POLICY=allowlist|open|disabled
DINGTALK_ALLOWED_FROM=staffA,staffB
DINGTALK_GROUP_ALLOWLIST=cidA,cidB
DINGTALK_REQUIRE_MENTION_IN_GROUP=1
```

## 5. Local smoke test

```bash
# Fetch test
PYTHONPATH=src python3 src/channels/dingtalk.py fetch

# Send test (api/webhook depends on DINGTALK_SEND_MODE)
PYTHONPATH=src python3 src/channels/dingtalk.py send 'gateway ping'
```
