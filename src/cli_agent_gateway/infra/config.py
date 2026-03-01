from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path


def load_dotenv(path: Path) -> None:
    if not path.exists():
        return
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key:
            os.environ.setdefault(key, value)


@dataclass
class AppConfig:
    workdir: Path
    fetch_cmd: str
    send_cmd: str
    remote_user_id: str
    allowed_from: set[str]
    poll_interval_sec: int
    timeout_sec: int
    report_dir: Path
    state_file: Path
    interaction_log_file: Path
    process_only_latest: bool
    progress_notify_interval_sec: int
    sms_reply_max_chars: int
    acp_agent_cmd: str
    permission_policy: str
    reply_style_enabled: bool
    reply_style_prompt: str

    @classmethod
    def from_env(cls, repo_root: Path, workdir_arg: str) -> "AppConfig":
        load_dotenv(repo_root / ".env")

        default_fetch_cmd = f"python3 {repo_root / 'src/cli_agent_gateway/channels/imessage_fetch.py'}"
        default_send_cmd = f"python3 {repo_root / 'src/cli_agent_gateway/channels/imessage_send.py'}"

        remote_user = os.getenv("REMOTE_USER_ID", "<USER_IMESSAGE_ID>").strip()
        allowlist = os.getenv("ALLOWED_FROM", remote_user).strip()

        return cls(
            workdir=Path(workdir_arg).expanduser().resolve(),
            fetch_cmd=os.getenv("SMS_FETCH_CMD", default_fetch_cmd).strip(),
            send_cmd=os.getenv("SMS_SEND_CMD", default_send_cmd).strip(),
            remote_user_id=remote_user,
            allowed_from={x.strip() for x in allowlist.split(",") if x.strip()},
            poll_interval_sec=int(os.getenv("POLL_INTERVAL_SEC", "5")),
            timeout_sec=int(os.getenv("AGENT_TIMEOUT_SEC", "1800")),
            report_dir=Path(os.getenv("REPORT_DIR", str(repo_root / ".agent_gateway_reports"))).expanduser().resolve(),
            state_file=Path(os.getenv("STATE_FILE", str(repo_root / ".agent_gateway_state.json"))).expanduser().resolve(),
            interaction_log_file=Path(
                os.getenv("INTERACTION_LOG_FILE", str(repo_root / ".agent_gateway_interactions.jsonl"))
            ).expanduser().resolve(),
            process_only_latest=os.getenv("PROCESS_ONLY_LATEST", "0").strip() != "0",
            progress_notify_interval_sec=int(os.getenv("PROGRESS_NOTIFY_INTERVAL_SEC", "60")),
            sms_reply_max_chars=int(os.getenv("SMS_REPLY_MAX_CHARS", "8000")),
            acp_agent_cmd=os.getenv("ACP_AGENT_CMD", "codex-acp").strip(),
            permission_policy=os.getenv("ACP_PERMISSION_POLICY", "auto_allow").strip(),
            reply_style_enabled=os.getenv("REPLY_STYLE_ENABLED", "1").strip() != "0",
            reply_style_prompt=os.getenv(
                "REPLY_STYLE_PROMPT",
                (
                    "请用 iMessage 友好格式回复：短句优先；先结论后细节；"
                    "尽量 3-6 行；避免长段落、复杂 Markdown、表格；"
                    "代码只给最小必要片段；状态词清晰（进行中/阻塞/完成）。"
                ),
            ).strip(),
        )
