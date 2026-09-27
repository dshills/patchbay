GO ?= go
GOLANGCI_LINT ?= golangci-lint
LINT_VERSION := v2.13.2
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -X patchbay/internal/version.Version=$(VERSION) -X patchbay/internal/version.Commit=$(COMMIT) -X patchbay/internal/version.BuildTime=$(BUILD_TIME)

.PHONY: build release fmt fmt-check vet lint test race coverage check install-lint
build:
	mkdir -p bin
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/deckd ./cmd/deckd
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/deckctl ./cmd/deckctl
release:
	python3 scripts/release.py --version '$(VERSION)' --commit '$(COMMIT)' --build-time '$(BUILD_TIME)'
fmt:
	gofmt -w $$(find cmd internal pkg -name '*.go')
fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal pkg -name '*.go'))" || (echo 'Run make fmt'; exit 1)
vet:
	$(GO) vet ./...
lint:
	$(GOLANGCI_LINT) run
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
coverage:
	$(GO) test -coverprofile=coverage.out ./...
check: fmt-check vet lint test race build
install-lint:
	GOBIN=$(CURDIR)/.tools $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)
