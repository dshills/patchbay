GO ?= go
GOLANGCI_LINT ?= golangci-lint
LINT_VERSION := v2.13.2
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
RELEASE_FLAGS ?=
LDFLAGS = -X patchbay/internal/version.Version=$(VERSION) -X patchbay/internal/version.Commit=$(COMMIT) -X patchbay/internal/version.BuildTime=$(BUILD_TIME)

.PHONY: build release release-test streamdeck-package streamdeck-verify plugin-example plugin-verify fmt fmt-check vet lint test race coverage check install-lint
build:
	mkdir -p bin
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/deckd ./cmd/deckd
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/deckctl ./cmd/deckctl
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/decksd ./cmd/decksd
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/deckplugincheck ./cmd/deckplugincheck
plugin-example:
	mkdir -p bin
	$(GO) build -trimpath -buildvcs=false -o bin/deckplugin-example ./examples/plugin
plugin-verify: plugin-example
	$(GO) run ./cmd/deckplugincheck --config configs/plugins.yaml
streamdeck-package:
	python3 scripts/package_streamdeck.py --version '$(VERSION)' --commit '$(COMMIT)' --build-time '$(BUILD_TIME)' $(RELEASE_FLAGS)
streamdeck-verify: build
	PATCHBAY_DECKSD_BINARY="$(CURDIR)/bin/decksd" $(GO) test ./adapters/streamdeck -run '^TestFakeAppWithRealDaemonAndRestart$$' -count=1
release:
	python3 scripts/release.py --version '$(VERSION)' --commit '$(COMMIT)' --build-time '$(BUILD_TIME)' $(RELEASE_FLAGS)
release-test:
	python3 -m unittest discover -s scripts -p 'test_*.py'
fmt:
	gofmt -w $$(find cmd internal pkg adapters examples -name '*.go')
fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal pkg adapters examples -name '*.go'))" || (echo 'Run make fmt'; exit 1)
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
check: fmt-check vet lint test race build plugin-verify release-test
install-lint:
	GOBIN=$(CURDIR)/.tools $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)
