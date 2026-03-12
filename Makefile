SHELL := /bin/zsh

GUI_APP_NAME := CLI Agent Gateway
GUI_BUNDLE_ID := dev.mikas.cli-agent-gateway
GUI_OUTPUT_DIR ?= $(HOME)/Desktop
GUI_APP_PATH := $(GUI_OUTPUT_DIR)/$(GUI_APP_NAME).app

.PHONY: usage help
usage help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@echo "  usage, help   Show this help message"
	@echo "  run           Run gateway (go run ./cmd/gateway-cli run)"
	@echo "  build         Build CLI binary to ./bin/cag"
	@echo "  gui-build     Build macOS GUI app bundle to $(GUI_OUTPUT_DIR)"
	@echo "  gui-close     Quit/kill the running macOS GUI app"
	@echo "  gui-open      Build/open macOS GUI app"
	@echo "  gui-dev       Quit old app, rebuild CLI/service stack, then build/open macOS GUI app"


.PHONY: run
run:
	@cd src && go run ./cmd/gateway-cli run

.PHONY: build
build:
	@mkdir -p bin
	@cd src && go build -o ../bin/cag ./cmd/gateway-cli
	@echo "Built: ./bin/cag"

.PHONY: gui-build
gui-build: build
	@set -euo pipefail; \
	output_dir="$(GUI_OUTPUT_DIR)"; \
	app_path="$(GUI_APP_PATH)"; \
	contents_dir="$$app_path/Contents"; \
	macos_dir="$$contents_dir/MacOS"; \
	resources_dir="$$contents_dir/Resources"; \
	mkdir -p "$$macos_dir" "$$resources_dir"; \
	/usr/bin/python3 -c 'import json, pathlib, sys; home = pathlib.Path.home(); cfg = {"repoRoot": str(pathlib.Path(sys.argv[2])), "workdir": str(home / ".cag"), "lockFile": str(home / ".cag/runtime/gateway.lock"), "logFile": str(home / ".cag/gatewayd/gatewayd.log"), "stateFile": str(home / ".cag/runtime/state.json"), "interactionLogFile": str(home / ".cag/runtime/interactions.jsonl")}; pathlib.Path(sys.argv[1]).write_text(json.dumps(cfg, indent=2) + "\n")' "$$resources_dir/gateway_config.json" "$(CURDIR)"; \
	sdk_path="$$(xcrun --show-sdk-path --sdk macosx)"; \
	printf '%s\n' \
		'<?xml version="1.0" encoding="UTF-8"?>' \
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
		'<plist version="1.0">' \
		'<dict>' \
		'  <key>CFBundleDevelopmentRegion</key>' \
		'  <string>en</string>' \
		'  <key>CFBundleExecutable</key>' \
		'  <string>$(GUI_APP_NAME)</string>' \
		'  <key>CFBundleIdentifier</key>' \
		'  <string>$(GUI_BUNDLE_ID)</string>' \
		'  <key>CFBundleInfoDictionaryVersion</key>' \
		'  <string>6.0</string>' \
		'  <key>CFBundleName</key>' \
		'  <string>$(GUI_APP_NAME)</string>' \
		'  <key>CFBundlePackageType</key>' \
		'  <string>APPL</string>' \
		'  <key>CFBundleShortVersionString</key>' \
		'  <string>0.2.0</string>' \
		'  <key>CFBundleVersion</key>' \
		'  <string>1</string>' \
		'  <key>LSMinimumSystemVersion</key>' \
		'  <string>14.0</string>' \
		'  <key>NSHighResolutionCapable</key>' \
		'  <true/>' \
		'</dict>' \
		'</plist>' > "$$contents_dir/Info.plist"; \
	cp macos/CLIApp/Assets/AppLogo.svg "$$resources_dir/AppLogo.svg"; \
	xcrun swiftc \
		-parse-as-library \
		-sdk "$$sdk_path" \
		-target arm64-apple-macos14.0 \
		-framework AppKit \
		-framework SwiftUI \
		-framework Foundation \
		-framework CryptoKit \
		macos/CLIApp/Sources/main.swift \
		-o "$$macos_dir/$(GUI_APP_NAME)"; \
	/usr/bin/codesign --force --sign - "$$app_path" >/dev/null; \
	echo "Built: $$app_path"

.PHONY: gui-open
gui-open: gui-build
	@open "$(GUI_APP_PATH)"

.PHONY: gui-close
gui-close:
	@set -euo pipefail; \
	app_name="$(GUI_APP_NAME)"; \
	app_path="$(GUI_APP_PATH)"; \
	bundle_id="$(GUI_BUNDLE_ID)"; \
	/usr/bin/osascript -e 'tell application id "'"$$bundle_id"'" to quit' >/dev/null 2>&1 || true; \
	/usr/bin/osascript -e 'tell application "'"$$app_name"'" to quit' >/dev/null 2>&1 || true; \
	for _ in 1 2 3 4 5 6 7 8 9 10; do \
		if ! pgrep -x "$$app_name" >/dev/null 2>&1; then \
			break; \
		fi; \
		sleep 0.2; \
	done; \
	if pgrep -x "$$app_name" >/dev/null 2>&1; then \
		pkill -x "$$app_name" >/dev/null 2>&1 || true; \
		sleep 0.5; \
	fi; \
	if [ -d "$$app_path" ]; then \
		rm -rf "$$app_path"; \
	fi

.PHONY: gui-dev
gui-dev: gui-close build
	@$(MAKE) gui-build GUI_OUTPUT_DIR="$(GUI_OUTPUT_DIR)"
	@open "$(GUI_APP_PATH)"
