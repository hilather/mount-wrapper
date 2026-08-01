# mount-wrapper — common developer targets

MODULE   := github.com/hilather/mount-wrapper
BIN      := mount-wrapper
CMD      := ./cmd/mount-wrapper
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

export PATH := $(HOME)/.local/go/bin:$(HOME)/.local/node-v22.14.0-linux-x64/bin:$(PATH)

.PHONY: all build test vet lint web-install web-dev web-build web-check web-test web-e2e parity release-snapshot clean tidy fmt help

all: test build

help:
	@echo "Targets:"
	@echo "  make build       Build $(BIN) to ./bin/"
	@echo "  make test        go test ./..."
	@echo "  make vet         go vet ./..."
	@echo "  make tidy        go mod tidy"
	@echo "  make fmt         gofmt -w"
	@echo "  make web-install npm install in web/"
	@echo "  make web-dev     Vite dev server (proxies /api → :8787)"
	@echo "  make web-build   Build SPA into internal/webui/dist"
	@echo "  make web-check   svelte-check + tsc"
	@echo "  make web-test    vitest unit tests in web/"
	@echo "  make web-e2e     optional Playwright smoke (RUN_E2E=1; install chromium first)"
	@echo "  make lint        golangci-lint if installed"
	@echo "  make parity      Regenerate tools/parity inventories (offline)"
	@echo "  make release-snapshot  goreleaser snapshot (linux/darwin amd64+arm64, deb/rpm)"
	@echo "  make clean       Remove bin/, dist/, and web/dist"

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BIN) $(CMD)

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './web/*')

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed; running go vet"; \
		go vet ./...; \
	fi

web-install:
	cd web && npm install

web-dev:
	cd web && npm run dev

web-build:
	cd web && npm run build
	rm -rf internal/webui/dist
	mkdir -p internal/webui/dist
	cp -a web/dist/. internal/webui/dist/
	@echo "SPA copied to internal/webui/dist (embed.FS)"

web-check:
	cd web && npm run check

web-test:
	cd web && npm test

# Optional SPA smoke: mocks /api/* (no daemon). Needs browsers:
#   cd web && npm run test:e2e:install
#   make web-e2e
# Multi-arch snapshot: linux/{amd64,arm64} + darwin/{amd64,arm64} + deb/rpm.
# Requires: goreleaser, node (for web-build hook), git history.
release-snapshot: web-build
	@command -v goreleaser >/dev/null 2>&1 || { echo "install: go install github.com/goreleaser/goreleaser/v2@latest"; exit 1; }
	goreleaser release --snapshot --clean

web-e2e:
	cd web && RUN_E2E=1 npm run test:e2e

parity:
	./tools/parity/run_all.sh

clean:
	rm -rf bin dist web/dist
