SHELL := /bin/zsh
ENV_FILE ?= .env
PYTHONPATH ?= src
WORKDIR ?= $(shell if [ -f $(ENV_FILE) ]; then awk -F= '/^CODEX_WORKDIR=/{print $$2; exit}' $(ENV_FILE); fi)
DEFAULT_WORKDIR := $(shell pwd)

.PHONY: run config status
run:
	@APP_WORKDIR="$(WORKDIR)"; \
	if [ -z "$$APP_WORKDIR" ]; then APP_WORKDIR="$(DEFAULT_WORKDIR)"; fi; \
	PYTHONPATH=$(PYTHONPATH) python3 -m cli_agent_gateway.app.main "$$APP_WORKDIR"

config:
	@APP_WORKDIR="$(WORKDIR)"; \
	if [ -z "$$APP_WORKDIR" ]; then APP_WORKDIR="$(DEFAULT_WORKDIR)"; fi; \
	PYTHONPATH=$(PYTHONPATH) python3 -c 'from pathlib import Path; import os, sys; from cli_agent_gateway.infra.setup_wizard import run_setup_wizard; run_setup_wizard(Path(os.getcwd()), sys.argv[1], force=True)' "$$APP_WORKDIR"

status:
	@PYTHONPATH=$(PYTHONPATH) python3 -c 'import os; from pathlib import Path; from cli_agent_gateway.infra.config import load_dotenv; from cli_agent_gateway.infra.process_lock import inspect_lock; root = Path(os.getcwd()); load_dotenv(root / ".env"); lock = Path(os.getenv("LOCK_FILE", str(root / ".cli_agent_gateway.lock"))).expanduser().resolve(); st = inspect_lock(lock); print(f"RUNNING pid={st.owner_pid} started_at={st.owner_started_at} lock={lock}" if st.locked else f"NOT_RUNNING lock={lock}")'

.PHONY: run-dingtalk-callback
run-dingtalk-callback:
	PYTHONPATH=$(PYTHONPATH) python3 -m cli_agent_gateway.channels.dingtalk_callback_server
