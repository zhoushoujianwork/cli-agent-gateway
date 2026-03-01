from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class GatewayState:
    processed_ids: list[str] = field(default_factory=list)
    session_map: dict[str, str] = field(default_factory=dict)


class JsonStateStore:
    def __init__(self, path: Path, max_processed: int = 2000):
        self.path = path
        self.max_processed = max_processed

    def load(self) -> GatewayState:
        if not self.path.exists():
            return GatewayState()
        try:
            node = json.loads(self.path.read_text(encoding="utf-8"))
        except Exception:
            return GatewayState()
        ids = node.get("processed_ids", [])
        mapping = node.get("session_map", {})
        if not isinstance(ids, list):
            ids = []
        if not isinstance(mapping, dict):
            mapping = {}
        return GatewayState(processed_ids=[str(x) for x in ids], session_map={str(k): str(v) for k, v in mapping.items()})

    def save(self, state: GatewayState) -> None:
        if len(state.processed_ids) > self.max_processed:
            state.processed_ids = state.processed_ids[-self.max_processed :]
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.path.write_text(
            json.dumps(
                {
                    "processed_ids": state.processed_ids,
                    "session_map": state.session_map,
                },
                ensure_ascii=False,
                indent=2,
            ),
            encoding="utf-8",
        )
