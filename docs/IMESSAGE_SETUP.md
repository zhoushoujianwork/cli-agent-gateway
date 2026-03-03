# iMessage Channel Setup

## 1. Prerequisites

- macOS with iMessage enabled and synced with iPhone.
- `imsg` installed and available in PATH.
- Terminal app granted Full Disk Access (System Settings -> Privacy & Security).

## 2. Environment

Configure `.env`:

```bash
IMSG_BIN=imsg
IMSG_CHAT_ID=<chat_id_for_fetch>
IMSG_SEND_CHAT_ID=<chat_id_for_send>
IMSG_SEND_SERVICE=auto
IMSG_FETCH_LIMIT=30
IMSG_INCLUDE_FROM_ME=0

REMOTE_USER_ID=<trusted_sender_id>
ALLOWED_FROM=<trusted_sender_id>
ACP_AGENT_CMD=codex-acp
```

If `IMSG_SEND_CHAT_ID` is empty, send adapter will use `SMS_TO`.

## 3. Adapter smoke tests

```bash
# Fetch latest messages as unified JSON
PYTHONPATH=src python3 src/channels/imessage.py fetch

# Send one message to configured chat
PYTHONPATH=src SMS_TO='<fallback-id>' \
  python3 src/channels/imessage.py send 'gateway test message'
```

## 4. Run gateway

```bash
PYTHONPATH=src python3 -m app.main /path/to/agent-workdir
```

## 5. Troubleshooting

- `imsg history failed`:
  - Verify Full Disk Access for terminal and `imsg`.
  - Verify `IMSG_CHAT_ID` is correct.
- No outbound messages:
  - Check `IMSG_SEND_CHAT_ID` or fallback `SMS_TO`.
  - Ensure `IMSG_SEND_SERVICE` matches the chat (or keep `auto`).
