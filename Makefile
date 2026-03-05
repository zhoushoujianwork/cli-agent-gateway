SHELL := /bin/zsh

.PHONY: app

app:
	@./macos/CLIApp/scripts/build_macos_gui_app.sh
