BINARY := gopuzzle
BIN_DIR := bin
GO_BIN := $(shell go env GOPATH)/bin

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the binary into ./bin/
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) .

.PHONY: install
install: ## Install the binary to $GOPATH/bin
	go install ./...

.PHONY: run
run: install ## Reinstall, then launch the TUI
	$(GO_BIN)/$(BINARY)

.PHONY: version
version: install ## Reinstall, then show version info
	$(GO_BIN)/$(BINARY) version

.PHONY: test
test: ## Run all tests (includes verifying every puzzle's canonical solution)
	go test ./...

.PHONY: test-puzzles
test-puzzles: ## Run only the puzzle-verification test
	go test -v ./internal/puzzle/ -run TestAllSolutionsPass

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format all Go files
	go fmt ./...

.PHONY: tidy
tidy: ## Tidy go.mod
	go mod tidy

.PHONY: check
check: fmt vet test ## Format, vet, and test in one shot

.PHONY: clean
clean: ## Remove local build artifacts
	rm -rf $(BIN_DIR)

.PHONY: clean-scratch
clean-scratch: ## Wipe ~/.gopuzzle/scratch (use when many puzzles were renamed)
	rm -rf $(HOME)/.gopuzzle/scratch
