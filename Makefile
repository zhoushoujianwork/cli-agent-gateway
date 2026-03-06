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
	@echo "  gui-dev       Restart service stack, then build/open macOS GUI app"


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
gui-dev:
	@cd src && go run ./cmd/gateway-cli gatewayd-up --json >/dev/null
	@cd src && go run ./cmd/gateway-cli restart --json >/dev/null
	@./macos/CLIApp/scripts/build_macos_gui_app.sh --output-dir "$(HOME)/Desktop"
