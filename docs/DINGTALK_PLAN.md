# DingTalk Channel Plan (Clawbot-inspired)

## Goal

Implement DingTalk as the next production channel after iMessage, while keeping the ACP runtime unchanged.

Design baseline follows Clawbot/OpenClaw channel ideas:

- Deterministic routing by host, not model-driven channel switching.
- Unified normalized message contract.
- DM/group access policy and mention-gating as host-level controls.
- Stable session key shape for concurrency and replay safety.

## Reference design points from Clawbot/OpenClaw

- Channel routing is deterministic and host-configured.
- Session key is derived from channel + peer/group/thread shape.
- DM policy and group policy are explicit config, not implicit defaults.
- Mention-gating is used for group chats to reduce noisy auto-replies.

## Scope (MVP)

- Add `src/channels/dingtalk_fetch.py`.
- Add `src/channels/dingtalk_send.py`.
- Add DingTalk config fields in `.env.example` and `infra/config.py`.
- Keep existing gateway loop unchanged by preserving normalized contract:
  - `{id, from, text, ts, thread_id}`
- Verify signature / timestamp and apply sender allowlist.
- Session mapping by `conversationId` (and optional `threadId`).

## Non-goals (MVP)

- Rich card message rendering.
- Multi-tenant DingTalk app routing.
- Full callback server framework with queue workers.

## Message and routing model

- Channel id: `dingtalk`.
- Sender id: DingTalk `senderStaffId` (fallback `senderId`).
- Thread key:
  - DM: `conversationId`
  - Group: `conversationId[:threadId]` when topic/thread exists
- Session key formula (reuse existing router):
  - `build_session_key(channel="dingtalk", sender, thread_id)`

## Access policy (Clawbot-style controls)

- `DINGTALK_DM_POLICY=allowlist|open|disabled` (default `allowlist`).
- `DINGTALK_GROUP_POLICY=allowlist|open|disabled` (default `allowlist`).
- `DINGTALK_ALLOWED_FROM=staffA,staffB`.
- `DINGTALK_GROUP_ALLOWLIST=cidA,cidB`.
- `DINGTALK_REQUIRE_MENTION_IN_GROUP=1` (default on).

## API strategy

MVP uses pull-style command adapters first (compatible with current gateway):

- `dingtalk_fetch.py`:
  - Reads pending inbound messages from local queue file (fed by callback receiver or poller).
  - Emits normalized JSON array.
- `dingtalk_send.py`:
  - Sends text reply via DingTalk robot/API.

Phase-2 adds optional callback receiver:

- `dingtalk_callback_server.py`:
  - Verifies signatures.
  - Writes normalized inbound events into queue file.

## Config draft

```bash
DINGTALK_APP_KEY=...
DINGTALK_APP_SECRET=...
DINGTALK_AGENT_ID=...
DINGTALK_BOT_WEBHOOK=...
DINGTALK_BOT_SECRET=...

DINGTALK_DM_POLICY=allowlist
DINGTALK_GROUP_POLICY=allowlist
DINGTALK_ALLOWED_FROM=staffA,staffB
DINGTALK_GROUP_ALLOWLIST=cidA,cidB
DINGTALK_REQUIRE_MENTION_IN_GROUP=1

DINGTALK_QUEUE_FILE=.dingtalk_inbox.jsonl
```

## Milestones

1. `M1`: Adapter skeleton + config wiring
2. `M2`: Real send path verified to DingTalk chat
3. `M3`: Real inbound path verified (queue-backed fetch)
4. `M4`: Policy enforcement (allowlist + mention gate)
5. `M5`: End-to-end ACP flow in DingTalk (`ack -> progress -> final`)

## Acceptance criteria

- New inbound DM in DingTalk triggers one gateway task.
- Group message only triggers when policy passes (allowlist + mention gate).
- `ack/progress/final` are delivered to the same DingTalk conversation.
- Session continuity works across multiple messages in one conversation.
- Duplicate inbound events do not trigger duplicate execution.

## Risks and mitigations

- DingTalk permission onboarding complexity:
  - Mitigation: provide a dedicated setup checklist doc with screenshots.
- Callback retries / duplicate events:
  - Mitigation: strict message id dedupe in queue and state store.
- Rate limits:
  - Mitigation: outbound retry with backoff and short-circuit on permanent errors.
- Rich text incompatibility:
  - Mitigation: default plain text output, optional rich mode later.
