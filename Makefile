GO ?= go
GOLANGCI_LINT ?= golangci-lint
LINT_VERSION := v2.13.2
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -X patchbay/internal/version.Version=$(VERSION) -X patchbay/internal/version.Commit=$(COMMIT) -X patchbay/internal/version.BuildTime=$(BUILD_TIME)

.PHONY: build release streamdeck-package streamdeck-verify fmt fmt-check vet lint test race coverage check install-lint
build:
	mkdir -p bin
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/deckd ./cmd/deckd
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/deckctl ./cmd/deckctl
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/decksd ./cmd/decksd
streamdeck-package:
	python3 scripts/package_streamdeck.py --version '$(VERSION)' --commit '$(COMMIT)' --build-time '$(BUILD_TIME)'
streamdeck-verify: build
	PATCHBAY_DECKSD_BINARY="$(CURDIR)/bin/decksd" $(GO) test ./adapters/streamdeck -run '^TestFakeAppWithRealDaemonAndRestart$$' -count=1
release:
	python3 scripts/release.py --version '$(VERSION)' --commit '$(COMMIT)' --build-time '$(BUILD_TIME)'
fmt:
	gofmt -w $$(find cmd internal pkg adapters -name '*.go')
fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal pkg adapters -name '*.go'))" || (echo 'Run make fmt'; exit 1)
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
