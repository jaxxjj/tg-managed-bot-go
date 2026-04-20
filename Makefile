# =============================================================================
# tg-managed-bot-go — developer Makefile
#
# This library has two Go modules:
#   - root           (pure library, zero external deps)
#   - examples/minimal  (gin demo server, own go.mod with replace directive)
#
# Targets run against the root module by default. Use `make example-*`
# for the demo module.
# =============================================================================

resolve-bin = $(shell command -v $(1) 2>/dev/null)
GO := $(call resolve-bin,go)
GOLANGCI_LINT := $(call resolve-bin,golangci-lint)
ifeq ($(strip $(GO)),)
GO := go
endif

EXAMPLE_DIR := examples/minimal
COVERAGE_FILE := coverage.out
COVERAGE_HTML := coverage.html

.DEFAULT_GOAL := help

# =============================================================================
# Build
# =============================================================================

.PHONY: build

build:
	@echo "--> Building root module"
	@$(GO) build ./...

# =============================================================================
# Testing
# =============================================================================

.PHONY: test test-cover test-cover-html bench

test:
	@echo "--> Running tests with -race"
	@$(GO) test -race ./...

test-cover:
	@echo "--> Running tests with coverage"
	@$(GO) test -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	@$(GO) tool cover -func=$(COVERAGE_FILE) | tail -1

test-cover-html: test-cover
	@$(GO) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "--> Coverage HTML: $(COVERAGE_HTML)"

bench:
	@echo "--> Running benchmarks"
	@$(GO) test -run=NONE -bench=. -benchmem ./...

# =============================================================================
# Formatting
# =============================================================================

.PHONY: fmt fmt-check tidy tidy-all

fmt:
	@echo "--> Formatting Go code"
	@$(GO) fmt ./...

fmt-check:
	@echo "--> Checking Go code formatting"
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "Unformatted files:"; \
		echo "$$out"; \
		echo "Run 'make fmt' to fix."; \
		exit 1; \
	fi

tidy:
	@echo "--> go mod tidy (root)"
	@$(GO) mod tidy

tidy-all: tidy
	@echo "--> go mod tidy ($(EXAMPLE_DIR))"
	@cd $(EXAMPLE_DIR) && $(GO) mod tidy

# =============================================================================
# Linting
# =============================================================================

.PHONY: vet lint lint-fix

vet:
	@echo "--> go vet"
	@$(GO) vet ./...

lint:
	@echo "--> golangci-lint run"
	@if [ -z "$(GOLANGCI_LINT)" ]; then \
		echo "golangci-lint not installed. Run 'make install-tools'."; \
		exit 1; \
	fi
	@$(GOLANGCI_LINT) run ./...

lint-fix:
	@echo "--> golangci-lint run --fix"
	@if [ -z "$(GOLANGCI_LINT)" ]; then \
		echo "golangci-lint not installed. Run 'make install-tools'."; \
		exit 1; \
	fi
	@$(GOLANGCI_LINT) run --fix ./...

# =============================================================================
# Verify (what CI runs)
# =============================================================================

.PHONY: verify

verify: fmt-check vet lint test
	@echo "--> all checks passed"

# =============================================================================
# Example (examples/minimal, independent go.mod)
# =============================================================================

.PHONY: example-build example-run example-vet example-tidy

example-build:
	@echo "--> Building $(EXAMPLE_DIR)"
	@cd $(EXAMPLE_DIR) && $(GO) build ./...

example-run:
	@echo "--> Running $(EXAMPLE_DIR) (set PAIRING_SECRET env var)"
	@cd $(EXAMPLE_DIR) && $(GO) run .

example-vet:
	@cd $(EXAMPLE_DIR) && $(GO) vet ./...

example-tidy:
	@cd $(EXAMPLE_DIR) && $(GO) mod tidy

# =============================================================================
# Tooling
# =============================================================================

.PHONY: install-tools

install-tools:
	@echo "--> Installing developer tools"
	@echo "    golangci-lint (macOS: brew install golangci-lint)"
	@if [ "$$(uname)" = "Darwin" ]; then \
		brew list golangci-lint >/dev/null 2>&1 || brew install golangci-lint; \
	else \
		echo "    On Linux, see https://golangci-lint.run/usage/install/"; \
	fi

# =============================================================================
# Cleanup
# =============================================================================

.PHONY: clean

clean:
	@echo "--> Cleaning generated artifacts"
	@rm -f $(COVERAGE_FILE) $(COVERAGE_HTML)
	@rm -f $(EXAMPLE_DIR)/minimal

# =============================================================================
# Help
# =============================================================================

.PHONY: help

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build & run:"
	@echo "  build               Build root module"
	@echo "  example-build       Build examples/minimal"
	@echo "  example-run         Run examples/minimal (needs PAIRING_SECRET)"
	@echo ""
	@echo "Testing:"
	@echo "  test                Run tests with -race"
	@echo "  test-cover          Run tests + print coverage summary"
	@echo "  test-cover-html     Run tests + emit coverage.html"
	@echo "  bench               Run benchmarks"
	@echo ""
	@echo "Formatting:"
	@echo "  fmt                 gofmt ./..."
	@echo "  fmt-check           Check gofmt (exit 1 if diff)"
	@echo "  tidy                go mod tidy (root)"
	@echo "  tidy-all            go mod tidy (root + example)"
	@echo ""
	@echo "Linting:"
	@echo "  vet                 go vet ./..."
	@echo "  lint                golangci-lint run"
	@echo "  lint-fix            golangci-lint run --fix"
	@echo ""
	@echo "CI composite:"
	@echo "  verify              fmt-check + vet + lint + test"
	@echo ""
	@echo "Maintenance:"
	@echo "  install-tools       Install golangci-lint (macOS via brew)"
	@echo "  clean               Remove coverage files and binaries"
