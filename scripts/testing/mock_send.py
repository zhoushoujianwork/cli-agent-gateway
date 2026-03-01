#!/usr/bin/env python3
import json
import os
import sys
from pathlib import Path

text = sys.argv[1] if len(sys.argv) > 1 else ""
out_file = Path("tmp/test-runtime/sent_messages.jsonl")
out_file.parent.mkdir(parents=True, exist_ok=True)
record = {
    "to": os.getenv("SMS_TO", ""),
    "message_id": os.getenv("SMS_MESSAGE_ID", ""),
    "report_file": os.getenv("SMS_REPORT_FILE", ""),
    "text": text,
}
with out_file.open("a", encoding="utf-8") as f:
    f.write(json.dumps(record, ensure_ascii=False) + "\n")
