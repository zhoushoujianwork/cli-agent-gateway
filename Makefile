SHELL := /bin/zsh
ENV_FILE ?= .env
PYTHONPATH ?= src
WORKDIR ?= $(shell if [ -f $(ENV_FILE) ]; then awk -F= '/^CODEX_WORKDIR=/{print $$2; exit}' $(ENV_FILE); fi)

.PHONY: run
run:
	@if [ -z "$(WORKDIR)" ]; then \
		echo "WORKDIR is empty. Set CODEX_WORKDIR in .env or run: make run WORKDIR=/abs/path"; \
		exit 1; \
	fi
	PYTHONPATH=$(PYTHONPATH) python3 -m cli_agent_gateway.app.main "$(WORKDIR)"
