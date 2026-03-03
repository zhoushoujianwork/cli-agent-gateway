from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from core.contracts import utc_now


class InteractionLog:
    def __init__(self, path: Path):
        self.path = path

    def append(self, kind: str, **kwargs: Any) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        record = {"time": utc_now(), "kind": kind}
        record.update(kwargs)
        with self.path.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps(record, ensure_ascii=False) + "\n")
