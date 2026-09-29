GO ?= go
GOBIN := $(shell $(GO) env GOPATH)/bin
# Prefer golangci-lint on PATH, otherwise fall back to the one in GOPATH/bin.
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo $(GOBIN)/golangci-lint)

.PHONY: build build-linux test race lint fmt vet cover sim analyze demo tidy check tools tf-check

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

## build-linux: build the deployable linux/amd64 binaries into bin/ (used by infra/)
build-linux:
	mkdir -p bin
	for cmd in testapp controller stress; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w" -o bin/$$cmd ./cmd/$$cmd || exit 1; \
	done

## tf-check: format-check and validate the Terraform module (no AWS calls)
tf-check:
	terraform -chdir=infra fmt -check -recursive
	terraform -chdir=infra init -backend=false -input=false
	terraform -chdir=infra validate

## sim: run all simulator scenarios
sim:
	$(GO) run ./cmd/simulator

## analyze: compute the evaluation metrics from the simulator logs (run make sim first)
analyze:
	$(GO) run ./cmd/analyze sim-logs

## demo: interactive live demo (demo profile); type help for the commands
demo:
	$(GO) run ./cmd/simulator -demo

## tidy: tidy the module graph
tidy:
	$(GO) mod tidy

## check: everything CI runs
check: fmt vet lint race
