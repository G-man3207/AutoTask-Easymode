# atem developer tasks. `make check` is the full quality gate (also run in CI).
GOLANGCI_VERSION := v2.12.2
GOBIN := $(shell go env GOPATH)/bin
GOLANGCI := $(GOBIN)/golangci-lint

.PHONY: all check build vet lint fmt fmt-check test cover tools clean

all: check

## check: full quality gate — vet, formatting, lint, tests with race+coverage
check: vet lint test
	@echo "All checks passed."

## build: compile everything
build:
	go build ./...

## vet: go vet
vet:
	go vet ./...

## tools: install the pinned golangci-lint
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

## lint: run golangci-lint (this also reports formatting issues)
lint:
	$(GOLANGCI) run

## fmt: apply gofumpt formatting
fmt:
	$(GOLANGCI) fmt

## fmt-check: fail if formatting is needed
fmt-check:
	$(GOLANGCI) fmt --diff

## test: race detector + coverage (-count=1 ensures the profile is always written)
test:
	go test ./... -race -count=1 -covermode=atomic -coverprofile=coverage.out
	@go tool cover -func=coverage.out | tail -n 1

## cover: open the HTML coverage report
cover: test
	go tool cover -html=coverage.out

## clean: remove build/coverage artifacts
clean:
	rm -f coverage.out coverage.html atem atem.exe
