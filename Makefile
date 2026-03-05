SHELL := /bin/zsh

.PHONY: run
run:
	@cd src && go run ./cmd/gateway-cli run

.PHONY: build
build:
	@mkdir -p bin
	@cd src && go build -o ../bin/cag ./cmd/gateway-cli
	@echo "Built: ./bin/cag"
