#!/usr/bin/env python3
import json
from pathlib import Path

state_file = Path("tmp/test-runtime/mock_fetch_state.json")
if state_file.exists():
    print("[]")
else:
    state_file.parent.mkdir(parents=True, exist_ok=True)
    state_file.write_text('{"sent": true}', encoding="utf-8")
    print(
        json.dumps(
            [
                {
                    "id": "msg-test-001",
                    "from": "tester",
                    "text": "请创建一个 hello.txt，内容是 hello acp",
                    "ts": "2026-03-01T12:00:00Z",
                    "thread_id": "thread-1",
                }
            ],
            ensure_ascii=False,
        )
    )
