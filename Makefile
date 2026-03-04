SHELL := /bin/zsh
ENV_FILE ?= .env
PYTHONPATH ?= src
WORKDIR ?= $(shell if [ -f $(ENV_FILE) ]; then awk -F= '/^CODEX_WORKDIR=/{print $$2; exit}' $(ENV_FILE); fi)
DEFAULT_WORKDIR := $(shell pwd)

.PHONY: config status create-macos-launcher create-macos-app build-macos-gui-app clean

config:
	@APP_WORKDIR="$(WORKDIR)"; \
	if [ -z "$$APP_WORKDIR" ]; then APP_WORKDIR="$(DEFAULT_WORKDIR)"; fi; \
	PYTHONPATH=$(PYTHONPATH) python3 -c 'from pathlib import Path; import os, sys; from infra.setup_wizard import run_setup_wizard; run_setup_wizard(Path(os.getcwd()), sys.argv[1], force=True)' "$$APP_WORKDIR"

status:
	@PYTHONPATH=$(PYTHONPATH) python3 -c 'import os; from pathlib import Path; from infra.config import load_dotenv; from infra.process_lock import inspect_lock; root = Path(os.getcwd()); load_dotenv(root / ".env"); lock = Path(os.getenv("LOCK_FILE", str(root / ".cli_agent_gateway.lock"))).expanduser().resolve(); st = inspect_lock(lock); print(f"RUNNING pid={st.owner_pid} started_at={st.owner_started_at} lock={lock}" if st.locked else f"NOT_RUNNING lock={lock}")'

.PHONY: build-macos-gui-app
build-macos-gui-app:
	@./macos/CLIApp/scripts/build_macos_gui_app.sh

clean:
	rm -f .dingtalk_inbox.jsonl
	rm -f .cli_agent_gateway.lock
	rm -f .agent_gateway_state.json .agent_gateway_interactions.jsonl
	rm -f logs/state.json logs/interactions.jsonl
	rm -rf logs/reports tmp/runtime
