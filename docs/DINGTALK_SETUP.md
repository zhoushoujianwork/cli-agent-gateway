# DingTalk Setup (MVP)

## 1. Enable DingTalk channel commands

In `.env` set:

```bash
SMS_FETCH_CMD="python3 /abs/path/to/src/cli_agent_gateway/channels/dingtalk_fetch.py"
SMS_SEND_CMD="python3 /abs/path/to/src/cli_agent_gateway/channels/dingtalk_send.py"
```

## 2. Inbound queue file

`dingtalk_fetch.py` reads JSONL events from `DINGTALK_QUEUE_FILE`.
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

## 3. Outbound webhook

Set webhook config:

```bash
DINGTALK_BOT_WEBHOOK=https://oapi.dingtalk.com/robot/send?access_token=...
DINGTALK_BOT_SECRET=SEC...
```

`dingtalk_send.py` sends plain text and treats `errcode=0` as success.

## 4. Access policy

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
PYTHONPATH=src python3 src/cli_agent_gateway/channels/dingtalk_fetch.py

# Send test
PYTHONPATH=src python3 src/cli_agent_gateway/channels/dingtalk_send.py 'gateway ping'
```
