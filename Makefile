SHELL := /bin/zsh

.PHONY: usage help
usage help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@echo "  usage, help   Show this help message"
	@echo "  run           Run gateway (go run ./cmd/gateway-cli run)"
	@echo "  build         Build CLI binary to ./bin/cag"
	@echo "  gui-open      Build/open macOS GUI app"
	@echo "  gui-dev       Build CLI, best-effort restart service stack, then build/open macOS GUI app"


.PHONY: run
run:
	@cd src && go run ./cmd/gateway-cli run

.PHONY: build
build:
	@mkdir -p bin
	@cd src && go build -o ../bin/cag ./cmd/gateway-cli
	@echo "Built: ./bin/cag"

.PHONY: gui-open
gui-open:
	@./macos/CLIApp/scripts/build_macos_gui_app.sh --output-dir "$(HOME)/Desktop"

.PHONY: gui-dev
gui-dev: build
	@CAG_GRPC_DISABLE=1 ./bin/cag restart --json >/dev/null || echo "[WARN] local gateway restart failed; continuing to GUI build"
	@./bin/cag gatewayd-up --json >/dev/null || echo "[WARN] gatewayd-up failed; GUI will use direct CLI fallback"
	@./macos/CLIApp/scripts/build_macos_gui_app.sh --output-dir "$(HOME)/Desktop"
