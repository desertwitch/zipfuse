# Makefile

A2X = a2x
DOCS_DIR = docs

ZIPFUSE = zipfuse
ZIPFUSE_DIR = ./cmd/zipfuse
ZIPFUSE_ADOC = $(DOCS_DIR)/zipfuse.adoc

HELPER = mount.zipfuse
HELPER_DIR = ./cmd/mount.zipfuse
HELPER_ADOC = $(DOCS_DIR)/mount.zipfuse.adoc

VERSION := $(shell \
  tag=$$(git describe --tags --exact-match 2>/dev/null); \
  if [ -n "$$tag" ]; then echo $$tag | sed 's/^v//'; \
  else git rev-parse --short=7 HEAD; fi)

.PHONY: all $(ZIPFUSE) $(HELPER) check clean debug docs docs-man docs-pdf docs-text docs-clean help info lint test test-coverage vendor

all: vendor $(ZIPFUSE) $(HELPER) ## Runs the entire build chain for the application

$(ZIPFUSE): ## Builds the application
	CGO_ENABLED=0 GOFLAGS="-mod=vendor" go build -ldflags="-w -s -X main.Version=$(VERSION) -buildid=" -trimpath -o $(ZIPFUSE) $(ZIPFUSE_DIR)

$(HELPER): ## Builds the helper application
	CGO_ENABLED=0 GOFLAGS="-mod=vendor" go build -ldflags="-w -s -X main.Version=$(VERSION) -buildid=" -trimpath -o $(HELPER) $(HELPER_DIR)

check: ## Runs all static analysis and tests on the application code
	@$(MAKE) lint
	@$(MAKE) test

clean: ## Returns the application build stage to its original state (deleting files)
	@$(MAKE) docs-clean
	@rm -vf $(ZIPFUSE) $(HELPER) || true

debug: ## Builds the application in debug mode (with symbols, race checks, ...)
	CGO_ENABLED=1 GOFLAGS="-mod=vendor" go build -ldflags="-X main.Version=$(VERSION)-DBG" -trimpath -race -o $(ZIPFUSE) $(ZIPFUSE_DIR)
	CGO_ENABLED=1 GOFLAGS="-mod=vendor" go build -ldflags="-X main.Version=$(VERSION)-DBG" -trimpath -race -o $(HELPER) $(HELPER_DIR)
	@$(MAKE) info

docs: ## Builds all documentation (manpages, PDF, plain text)
	@$(MAKE) docs-man
	@$(MAKE) docs-pdf
	@$(MAKE) docs-text

docs-man: ## Builds manpage documentation
	$(A2X) -f manpage $(ZIPFUSE_ADOC)
	$(A2X) -f manpage $(HELPER_ADOC)

docs-pdf: ## Builds PDF documentation
	$(A2X) -f pdf $(ZIPFUSE_ADOC)
	$(A2X) -f pdf $(HELPER_ADOC)

docs-text: ## Builds plain text documentation
	$(A2X) -f text $(ZIPFUSE_ADOC)
	$(A2X) -f text $(HELPER_ADOC)

docs-clean: ## Removes generated documentation files
	@rm -vf $(DOCS_DIR)/*.pdf $(DOCS_DIR)/*.text $(DOCS_DIR)/*.1 $(DOCS_DIR)/*.8 || true

help: ## Shows all build related commands of the Makefile
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

info: ## Shows information about the application binaries that were built
	@file $(ZIPFUSE) || true
	@ldd $(ZIPFUSE) || true
	@file $(HELPER) || true
	@ldd $(HELPER) || true

lint: ## Runs the linter on the application code
	@golangci-lint cache clean
	@golangci-lint run

test: ## Runs all written tests for and on the application code
	@go test -failfast -race -covermode=atomic ./...

test-coverage: ## Runs all coverage tests for and on the application code
	@go test -failfast -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.tmp ./... && \
	grep -v "mock_" coverage.tmp > coverage.txt && \
	rm coverage.tmp

vendor: ## Pulls the (remote) dependencies into the local vendor folder
	@go mod tidy
	@go mod vendor
