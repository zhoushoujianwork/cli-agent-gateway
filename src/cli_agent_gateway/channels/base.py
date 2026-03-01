from __future__ import annotations

from typing import Protocol

from cli_agent_gateway.core.contracts import InboundMessage


class ChannelAdapter(Protocol):
    channel_id: str

    def fetch(self) -> list[InboundMessage]:
        ...

    def send(self, text: str, *, to: str, message_id: str, report_file: str = "") -> None:
        ...
