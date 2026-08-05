BINARY    := ratline
SHELL_BIN := ratline-shell
MODULE    := github.com/ALIRAZA47/ratline-cli

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
# The commit's date, not the wall clock. Baking in "now" makes every build a different
# binary, so the published SHA256SUMS cannot be checked by rebuilding from the tag — the
# one verification that does not require trusting whoever uploaded it. Falls back to the
# clock outside a git checkout.
DATE    ?= $(shell git log -1 --format=%cd --date=format:%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)

# CGO off so the binary is genuinely static: no glibc version to match, and it
# runs on any Ubuntu or Debian of the last decade. The SQLite driver is pure Go
# for the same reason.
export CGO_ENABLED := 0

LDFLAGS := -s -w \
	-X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
	-X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(MODULE)/internal/buildinfo.Date=$(DATE)

DIST := dist

.PHONY: help
help: ## Show the available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build both binaries for this host
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/$(BINARY)
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(SHELL_BIN) ./cmd/$(SHELL_BIN)

.PHONY: test
test: ## Run the unit tests
	go test -race ./...

.PHONY: test-short
test-short: ## Run the unit tests without the race detector
	go test ./...

.PHONY: fuzz
fuzz: ## Fuzz the validators for 30 seconds each
	go test -run '^$$' -fuzz FuzzUsername -fuzztime 30s ./internal/validate
	go test -run '^$$' -fuzz FuzzDomain -fuzztime 30s ./internal/validate
	go test -run '^$$' -fuzz FuzzResolveWithin -fuzztime 30s ./internal/validate
	go test -run '^$$' -fuzz FuzzAppModule -fuzztime 30s ./internal/validate
	go test -run '^$$' -fuzz FuzzSlug -fuzztime 30s ./internal/validate
	go test -run '^$$' -fuzz FuzzParseCommand -fuzztime 30s ./internal/system

.PHONY: cover
cover: ## Run the tests and open a coverage report
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: integration
integration: ## Run the integration suite against a real Ubuntu container
	@command -v docker >/dev/null || { echo "docker is required for the integration suite"; exit 1; }
	@set -e; \
	  code=0; \
	  docker compose -f test/integration/docker-compose.yml up --build \
	      --abort-on-container-exit --exit-code-from harness || code=$$?; \
	  if [ -s test/integration/results/suite.txt ]; then \
	    echo; echo "== the suite transcript =="; \
	    cat test/integration/results/suite.txt; \
	  fi; \
	  docker compose -f test/integration/docker-compose.yml down -v >/dev/null 2>&1 || true; \
	  exit $$code

# Pinned, and the same version CI installs. An unpinned `@latest` means a new
# release can fail the build on a commit that did not change, and the v2 module path
# matters: v1 cannot read the v2 configuration schema this repository uses.
GOLANGCI_VERSION ?= v2.12.2

.PHONY: lint
lint: ## Run golangci-lint, the same version CI uses
	@command -v golangci-lint >/dev/null || { \
		echo "golangci-lint is not installed. Install the version CI uses:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)"; \
		exit 1; }
	golangci-lint run

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format the source
	gofmt -s -w .

.PHONY: check
check: fmt vet test ## Format, vet and test

.PHONY: dist
dist: ## Cross-compile release binaries for amd64 and arm64
	rm -rf $(DIST) && mkdir -p $(DIST)
	@for arch in amd64 arm64; do \
		echo "building linux/$$arch"; \
		GOOS=linux GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o $(DIST)/$(BINARY)-linux-$$arch ./cmd/$(BINARY); \
		GOOS=linux GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o $(DIST)/$(SHELL_BIN)-linux-$$arch ./cmd/$(SHELL_BIN); \
	done
	cd $(DIST) && sha256sum * > SHA256SUMS

.PHONY: deb
deb: dist ## Build .deb packages with nfpm
	@command -v nfpm >/dev/null || { \
		echo "nfpm is not installed."; \
		echo "  go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest"; \
		exit 1; }
	@for arch in amd64 arm64; do \
		VERSION=$(VERSION) ARCH=$$arch nfpm package \
			--config packaging/nfpm.yaml --packager deb --target $(DIST)/; \
	done

.PHONY: completions
completions: build ## Generate shell completions into dist/completions
	mkdir -p $(DIST)/completions
	./bin/$(BINARY) completion bash > $(DIST)/completions/$(BINARY).bash
	./bin/$(BINARY) completion zsh  > $(DIST)/completions/_$(BINARY)
	./bin/$(BINARY) completion fish > $(DIST)/completions/$(BINARY).fish

.PHONY: docs-commands
docs-commands: build ## Regenerate docs/reference/commands.md from the binary
	bash scripts/gen-commands.sh ./bin/$(BINARY) docs/reference/commands.md

.PHONY: man
man: build ## Generate man pages into dist/man
	mkdir -p $(DIST)/man
	./bin/$(BINARY) man --dir $(DIST)/man

.PHONY: install
install: build ## Install both binaries onto this host (needs root)
	install -o root -g root -m 0755 bin/$(BINARY) /usr/local/bin/$(BINARY)
	install -d -o root -g root -m 0755 /usr/local/lib/ratline
	install -o root -g root -m 0755 bin/$(SHELL_BIN) /usr/local/lib/ratline/$(SHELL_BIN)

.PHONY: clean
clean: ## Remove build artefacts
	rm -rf bin $(DIST) coverage.out
