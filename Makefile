SHELL := /bin/zsh

.PHONY: run
run:
	@cd src && go run ./cmd/gateway-cli run

.PHONY: build
build:
	@mkdir -p bin
	@cd src && go build -o ../bin/cag ./cmd/gateway-cli
	@echo "Built: ./bin/cag"

.PHONY: gui
gui:
	@./macos/CLIApp/scripts/build_macos_gui_app.sh --output-dir "$(HOME)/Desktop" --no-open

.PHONY: gui-open
gui-open:
	@./macos/CLIApp/scripts/build_macos_gui_app.sh --output-dir "$(HOME)/Desktop"

.PHONY: dev-done
dev-done: build gui-open
	@echo "Done: CLI + macOS GUI app built."
