GO ?= go
GOBIN := $(shell $(GO) env GOPATH)/bin
# Prefer golangci-lint on PATH, otherwise fall back to the one in GOPATH/bin.
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo $(GOBIN)/golangci-lint)

.PHONY: build test race lint fmt vet cover sim tidy check tools

## build: compile all packages and commands
build:
	$(GO) build ./...

## test: run the test suite
test:
	$(GO) test ./...

## race: run the test suite with the race detector
race:
	$(GO) test -race ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## lint: run golangci-lint
lint:
	@test -x "$(GOLANGCI_LINT)" || { echo "golangci-lint not found; install with: make tools"; exit 1; }
	$(GOLANGCI_LINT) run ./...

## tools: install developer tools (golangci-lint) into GOPATH/bin
tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0

## fmt: format the code
fmt:
	$(GOLANGCI_LINT) fmt ./...

## cover: run tests and print total coverage
cover:
	$(GO) test -cover ./...

## sim: run all simulator scenarios
sim:
	$(GO) run ./cmd/simulator

## tidy: tidy the module graph
tidy:
	$(GO) mod tidy

## check: everything CI runs
check: fmt vet lint race
