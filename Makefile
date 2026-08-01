# mount-wrapper — common developer targets

MODULE   := github.com/hilather/mount-wrapper
BIN      := mount-wrapper
CMD      := ./cmd/mount-wrapper
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

export PATH := $(HOME)/.local/go/bin:$(HOME)/.local/node-v22.14.0-linux-x64/bin:$(PATH)

.PHONY: all build build-musl package-musl test vet lint web-install web-dev web-build web-check web-test web-e2e parity release-snapshot smoke smoke-rocky smoke-musl smoke-package package-contents-smoke clean tidy fmt help

all: test build

help:
	@echo "Targets:"
	@echo "  make build       Build $(BIN) to ./bin/"
	@echo "  make build-musl  Static linux binary via Alpine container (D7 extra path)"
	@echo "  make package-musl  build-musl + dist/*_linux_*_musl.tar.gz (+ SHA256SUMS)"
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
	@echo "  make release-snapshot  goreleaser snapshot (CGO=0 linux/darwin; no musl/docker)"
	@echo "  make smoke       Binary smoke (version/doctor/serve --once)"
	@echo "  make smoke-rocky Rocky 8 container binary smoke (docker/podman)"
	@echo "  make smoke-musl  Alpine musl/static build + binary smoke (docker/podman)"
	@echo "  make smoke-package  Deb content inventory (nfpm + dpkg-deb; soft-skip if missing)"
	@echo "  make package-contents-smoke  Alias of smoke-package"
	@echo "  # tar-only (no nfpm): PACKAGE_TAR=… SKIP_DEB=1 ./scripts/smoke-package-contents.sh"
	@echo "  make clean       Remove bin/, dist/, and web/dist"

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BIN) $(CMD)

# Static Linux binary inside golang:*-alpine (optional D7 path). Default
# releases stay CGO_ENABLED=0 via goreleaser; this does not replace them.
# ARCHS=amd64,arm64 for multi-arch. Needs docker or podman.
build-musl:
	./scripts/build-musl.sh

# Optional musl release tarballs into dist/ (after or without goreleaser).
# CI release.yml runs this after GoReleaser and gh-uploads the archives.
# ARCHS defaults to amd64,arm64 (shell). Needs docker or podman for build-musl.
package-musl:
	ARCHS="$${ARCHS:-amd64,arm64}" ./scripts/build-musl.sh
	ARCHS="$${ARCHS:-amd64,arm64}" REQUIRE_ALL=1 ./scripts/package-musl-release.sh

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
# Pure GoReleaser CGO=0 only (no docker). Optional musl: make package-musl after.
# Requires: goreleaser, node (for web-build hook), git history.
release-snapshot: web-build
	@command -v goreleaser >/dev/null 2>&1 || { echo "install: go install github.com/goreleaser/goreleaser/v2@latest"; exit 1; }
	goreleaser release --snapshot --clean
	@echo "snapshot OK under dist/. Optional musl tarballs: make package-musl"

# No FUSE: version, doctor, config show --local, serve --once.
smoke:
	./scripts/smoke-binary.sh --build

# Needs docker or podman; runs CGO=0 binary inside rockylinux:8.
smoke-rocky:
	./scripts/smoke-rocky8.sh --build

# Alpine container build (static) + smoke with that binary (no FUSE).
smoke-musl: build-musl
	BIN=./bin/mount-wrapper-musl ./scripts/smoke-binary.sh

# Deb content inventory via packaging/nfpm.yaml + dpkg-deb -c.
# Soft-skips (exit 0) when nfpm or dpkg-deb is missing; CI sets REQUIRE_TOOLS=1.
# Tar-only (no nfpm): PACKAGE_TAR=path/to.tar.gz SKIP_DEB=1 ./scripts/smoke-package-contents.sh
# Always-on under make test: TestPackageTarInventory (synthetic tar + PACKAGE_TAR=).
smoke-package package-contents-smoke:
	./scripts/smoke-package-contents.sh --build

web-e2e:
	cd web && RUN_E2E=1 npm run test:e2e

parity:
	./tools/parity/run_all.sh

clean:
	rm -rf bin dist web/dist
