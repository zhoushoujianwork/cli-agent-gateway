from __future__ import annotations

import os
import shutil
import sys
import termios
import tty
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path


SKIP_TOKEN = "-"


@dataclass
class PromptField:
    key: str
    label: str
    default: str | None = None
    optional: bool = True


def _quote_env(value: str) -> str:
    if value == "":
        return '""'
    if any(ch.isspace() for ch in value) or "#" in value or '"' in value or "'" in value:
        escaped = value.replace("\\", "\\\\").replace('"', '\\"')
        return f'"{escaped}"'
    return value


def _input_with_default(label: str, default: str | None, optional: bool) -> str | None:
    suffix = " (可选)" if optional else " (必填)"
    if default:
        prompt = f"{label}{suffix} [默认: {default}]，回车确认，输入 {SKIP_TOKEN} 跳过: "
    else:
        prompt = f"{label}{suffix}，输入值，留空或输入 {SKIP_TOKEN} 跳过: "

    raw = input(prompt).strip()
    if raw == SKIP_TOKEN:
        return None
    if raw == "":
        if default is not None:
            return default
        return None
    return raw


def _choose_channel() -> str:
    return _select_menu(
        title="\n请选择通道类型（↑/↓ 或 ←/→ 选择，Enter 确认）",
        options=[("imessage", "iMessage"), ("dingtalk", "DingTalk")],
        default="imessage",
    )


def _read_key() -> str:
    fd = sys.stdin.fileno()
    old = termios.tcgetattr(fd)
    try:
        tty.setraw(fd)
        ch1 = sys.stdin.read(1)
        if ch1 == "\x1b":
            ch2 = sys.stdin.read(1)
            if ch2 == "[":
                ch3 = sys.stdin.read(1)
                return f"\x1b[{ch3}"
        return ch1
    finally:
        termios.tcsetattr(fd, termios.TCSADRAIN, old)


def _select_menu(title: str, options: list[tuple[str, str]], default: str | None = None) -> str:
    if not options:
        raise ValueError("menu options must not be empty")
    idx = 0
    if default is not None:
        for i, (value, _) in enumerate(options):
            if value == default:
                idx = i
                break

    print(title)
    while True:
        for i, (_, label) in enumerate(options):
            prefix = "➤" if i == idx else " "
            print(f"  {prefix} {label}")

        key = _read_key()
        if key in {"\r", "\n"}:
            selected = options[idx]
            print(f"\r已选择: {selected[1]}")
            return selected[0]
        if key in {"\x1b[A", "\x1b[D", "k", "K"}:
            idx = (idx - 1) % len(options)
        elif key in {"\x1b[B", "\x1b[C", "j", "J"}:
            idx = (idx + 1) % len(options)

        print(f"\r\033[{len(options)}A", end="")


def _detect_defaults(repo_root: Path, workdir_arg: str) -> dict[str, str]:
    defaults: dict[str, str] = {}
    defaults["CODEX_WORKDIR"] = str(Path(workdir_arg).expanduser().resolve())
    defaults["LOCK_FILE"] = str((repo_root / ".cli_agent_gateway.lock").resolve())

    imsg_path = shutil.which("imsg")
    defaults["IMSG_BIN"] = imsg_path or "imsg"

    acp_path = shutil.which("codex-acp")
    if acp_path:
        defaults["ACP_AGENT_CMD"] = acp_path
    else:
        defaults["ACP_AGENT_CMD"] = "codex-acp"

    defaults["IMSG_FETCH_LIMIT"] = "30"
    defaults["IMSG_INCLUDE_FROM_ME"] = "0"
    defaults["IMSG_SEND_SERVICE"] = "auto"
    defaults["POLL_INTERVAL_SEC"] = "5"
    defaults["AGENT_TIMEOUT_SEC"] = "1800"
    defaults["ACP_PERMISSION_POLICY"] = "auto_allow"
    defaults["PROCESS_ONLY_LATEST"] = "0"
    defaults["PROGRESS_NOTIFY_INTERVAL_SEC"] = "60"
    defaults["SMS_REPLY_MAX_CHARS"] = "8000"
    defaults["REPLY_STYLE_ENABLED"] = "1"

    dingtalk_fetch = repo_root / "src/cli_agent_gateway/channels/dingtalk_fetch.py"
    dingtalk_send = repo_root / "src/cli_agent_gateway/channels/dingtalk_send.py"
    defaults["SMS_FETCH_CMD"] = f"python3 {dingtalk_fetch}"
    defaults["SMS_SEND_CMD"] = f"python3 {dingtalk_send}"
    defaults["DINGTALK_QUEUE_FILE"] = ".dingtalk_inbox.jsonl"
    defaults["DINGTALK_FETCH_MAX_EVENTS"] = "30"
    defaults["DINGTALK_DM_POLICY"] = "allowlist"
    defaults["DINGTALK_GROUP_POLICY"] = "allowlist"
    defaults["DINGTALK_REQUIRE_MENTION_IN_GROUP"] = "1"
    defaults["DINGTALK_SEND_MODE"] = "api"

    return defaults


def _read_env_file(path: Path) -> dict[str, str]:
    if not path.exists():
        return {}
    values: dict[str, str] = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key:
            values[key] = value
    return values


def _required_fields(channel: str, defaults: dict[str, str]) -> list[PromptField]:
    if channel == "imessage":
        return [
            PromptField("IMSG_CHAT_ID", "iMessage 对话标识（手机号/AppleID/Chat ID）", defaults.get("IMSG_CHAT_ID"), optional=False),
            PromptField("REMOTE_USER_ID", "远端用户标识（手机号或 AppleID）", defaults.get("REMOTE_USER_ID"), optional=False),
        ]
    return [
        PromptField("DINGTALK_APP_KEY", "DingTalk App Key", defaults.get("DINGTALK_APP_KEY"), optional=False),
        PromptField("DINGTALK_APP_SECRET", "DingTalk App Secret", defaults.get("DINGTALK_APP_SECRET"), optional=False),
        PromptField("DINGTALK_AGENT_ID", "DingTalk Agent ID", defaults.get("DINGTALK_AGENT_ID"), optional=False),
        PromptField("REMOTE_USER_ID", "远端用户 ID", defaults.get("REMOTE_USER_ID"), optional=False),
    ]


def _advanced_fields(channel: str, defaults: dict[str, str]) -> list[PromptField]:
    base_fields = [
        PromptField("CODEX_WORKDIR", "工作目录", defaults.get("CODEX_WORKDIR")),
        PromptField("LOCK_FILE", "单实例锁文件", defaults.get("LOCK_FILE")),
        PromptField("ACP_AGENT_CMD", "ACP 命令", defaults.get("ACP_AGENT_CMD")),
        PromptField("ACP_PERMISSION_POLICY", "权限策略", defaults.get("ACP_PERMISSION_POLICY")),
        PromptField("AGENT_TIMEOUT_SEC", "Agent 超时秒数", defaults.get("AGENT_TIMEOUT_SEC")),
        PromptField("POLL_INTERVAL_SEC", "轮询间隔秒数", defaults.get("POLL_INTERVAL_SEC")),
    ]

    if channel == "imessage":
        return base_fields + [
            PromptField("IMSG_BIN", "imsg 可执行文件", defaults.get("IMSG_BIN")),
            PromptField("IMSG_SEND_CHAT_ID", "iMessage 发送 Chat ID", defaults.get("IMSG_SEND_CHAT_ID")),
            PromptField("IMSG_SEND_SERVICE", "发送服务", defaults.get("IMSG_SEND_SERVICE")),
            PromptField("IMSG_FETCH_LIMIT", "每次抓取上限", defaults.get("IMSG_FETCH_LIMIT")),
            PromptField("IMSG_INCLUDE_FROM_ME", "包含自己发送的消息(0/1)", defaults.get("IMSG_INCLUDE_FROM_ME")),
            PromptField("ALLOWED_FROM", "允许来源 ID 列表(逗号分隔)", defaults.get("ALLOWED_FROM")),
        ]

    return base_fields + [
        PromptField("SMS_FETCH_CMD", "自定义 fetch 命令", defaults.get("SMS_FETCH_CMD")),
        PromptField("SMS_SEND_CMD", "自定义 send 命令", defaults.get("SMS_SEND_CMD")),
        PromptField("DINGTALK_QUEUE_FILE", "DingTalk 入站队列文件", defaults.get("DINGTALK_QUEUE_FILE")),
        PromptField("DINGTALK_FETCH_MAX_EVENTS", "DingTalk 每次抓取上限", defaults.get("DINGTALK_FETCH_MAX_EVENTS")),
        PromptField("DINGTALK_DM_POLICY", "DingTalk 私聊策略", defaults.get("DINGTALK_DM_POLICY")),
        PromptField("DINGTALK_GROUP_POLICY", "DingTalk 群聊策略", defaults.get("DINGTALK_GROUP_POLICY")),
        PromptField(
            "DINGTALK_REQUIRE_MENTION_IN_GROUP",
            "群聊是否要求 @ 机器人 (0/1)",
            defaults.get("DINGTALK_REQUIRE_MENTION_IN_GROUP"),
        ),
        PromptField("DINGTALK_SEND_MODE", "DingTalk 发送模式", defaults.get("DINGTALK_SEND_MODE")),
        PromptField("ALLOWED_FROM", "允许来源 ID 列表(逗号分隔)", defaults.get("ALLOWED_FROM")),
    ]


def _allowed_keys_for_channel(channel: str) -> set[str]:
    common = {
        "CODEX_WORKDIR",
        "ACP_AGENT_CMD",
        "ACP_PERMISSION_POLICY",
        "AGENT_TIMEOUT_SEC",
        "POLL_INTERVAL_SEC",
        "PROCESS_ONLY_LATEST",
        "PROGRESS_NOTIFY_INTERVAL_SEC",
        "SMS_REPLY_MAX_CHARS",
        "REPLY_STYLE_ENABLED",
        "REPLY_STYLE_PROMPT",
        "LOCK_FILE",
        "REMOTE_USER_ID",
        "ALLOWED_FROM",
        "CHANNEL_TYPE",
    }
    if channel == "imessage":
        return common | {
            "IMSG_BIN",
            "IMSG_CHAT_ID",
            "IMSG_SEND_CHAT_ID",
            "IMSG_SEND_SERVICE",
            "IMSG_FETCH_LIMIT",
            "IMSG_INCLUDE_FROM_ME",
        }
    return common | {
        "SMS_FETCH_CMD",
        "SMS_SEND_CMD",
        "DINGTALK_QUEUE_FILE",
        "DINGTALK_FETCH_MAX_EVENTS",
        "DINGTALK_DM_POLICY",
        "DINGTALK_GROUP_POLICY",
        "DINGTALK_REQUIRE_MENTION_IN_GROUP",
        "DINGTALK_SEND_MODE",
        "DINGTALK_APP_KEY",
        "DINGTALK_APP_SECRET",
        "DINGTALK_AGENT_ID",
    }


def _soft_delete_env(env_path: Path) -> Path | None:
    if not env_path.exists():
        return None
    backup_dir = env_path.parent / ".env.backups"
    backup_dir.mkdir(parents=True, exist_ok=True)
    ts = datetime.now().strftime("%Y%m%d_%H%M%S")
    backup_path = backup_dir / f".env.{ts}.bak"
    env_path.replace(backup_path)
    return backup_path


def run_setup_wizard(repo_root: Path, workdir_arg: str, force: bool = False) -> None:
    env_path = repo_root / ".env"
    env_exists = env_path.exists()
    if env_exists and not force:
        return

    if not os.isatty(0):
        reason = ".env 不存在" if not env_exists else "当前为强制配置模式"
        raise RuntimeError(f"{reason}且当前非交互终端，无法进行初始化：{env_path}")

    existing = _read_env_file(env_path)
    if env_exists:
        print(f"[SETUP] 检测到已有配置文件：{env_path}")
    else:
        print(f"[SETUP] 检测到配置文件缺失：{env_path}")
    print("[SETUP] 进入交互式配置。默认只询问必要字段，其余自动填充。")
    print("[SETUP] 文本项可回车保留当前值/默认值，输入 - 暂时跳过当前项。")

    defaults = _detect_defaults(repo_root=repo_root, workdir_arg=workdir_arg)
    merged_defaults = {**defaults, **existing}

    existing_channel = existing.get("CHANNEL_TYPE", "").strip()
    if existing_channel in {"imessage", "dingtalk"}:
        channel = _select_menu(
            title=f"\n当前通道: {existing_channel}（↑/↓ 或 ←/→ 选择，Enter 确认）",
            options=[("imessage", "iMessage"), ("dingtalk", "DingTalk")],
            default=existing_channel,
        )
    else:
        channel = _choose_channel()

    values: dict[str, str] = dict(defaults)
    values.update(existing)
    values["CHANNEL_TYPE"] = channel
    for field in _required_fields(channel=channel, defaults=merged_defaults):
        value = _input_with_default(field.label, field.default, field.optional)
        if value is None:
            continue
        values[field.key] = value

    if channel == "imessage" and values.get("IMSG_CHAT_ID") and not values.get("IMSG_SEND_CHAT_ID"):
        values["IMSG_SEND_CHAT_ID"] = values["IMSG_CHAT_ID"]

    if "ALLOWED_FROM" not in values and values.get("REMOTE_USER_ID"):
        values["ALLOWED_FROM"] = values["REMOTE_USER_ID"]

    advanced = _select_menu(
        title="\n是否进入高级配置（↑/↓ 或 ←/→ 选择，Enter 确认）",
        options=[("no", "跳过（推荐）"), ("yes", "进入高级配置")],
        default="no",
    )
    if advanced == "yes":
        for field in _advanced_fields(channel=channel, defaults=values):
            value = _input_with_default(field.label, values.get(field.key), optional=True)
            if value is None:
                continue
            values[field.key] = value

    allowed_keys = _allowed_keys_for_channel(channel)
    values = {k: v for k, v in values.items() if k in allowed_keys}

    lines: list[str] = [
        f"# Auto-generated by setup wizard at {datetime.now().isoformat(timespec='seconds')}",
        f"# Channel: {channel}",
        "",
    ]
    for key, value in values.items():
        lines.append(f"{key}={_quote_env(value)}")

    lines.append("")
    backup_path = _soft_delete_env(env_path)
    env_path.write_text("\n".join(lines), encoding="utf-8")
    if backup_path is not None:
        print(f"[SETUP] 已归档旧配置到: {backup_path}")
    print(f"[SETUP] 已写入 {env_path}，继续启动服务。")


def bootstrap_env_if_missing(repo_root: Path, workdir_arg: str) -> None:
    run_setup_wizard(repo_root=repo_root, workdir_arg=workdir_arg, force=False)
