# Detect the operating system
UNAME_S := $(shell uname -s)
# Binary name
BINARY_NAME=kpi-collector
# Module path for go install
MODULE_PATH=github.com/redhat-best-practices-for-k8s/kpi-collection-tool

# Release and commit injected at build time
GIT_COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_RELEASE ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LINKER_FLAGS = -s -w \
	-X '$(MODULE_PATH)/internal/commands.gitCommit=$(GIT_COMMIT)' \
	-X '$(MODULE_PATH)/internal/commands.gitRelease=$(GIT_RELEASE)'

build:
	CGO_ENABLED=0 go build -ldflags="$(LINKER_FLAGS)" -o $(BINARY_NAME) ./cmd/kpi-collector

build-darwin-arm64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$(LINKER_FLAGS)" -o $(BINARY_NAME) ./cmd/kpi-collector

build-debug:
	CGO_ENABLED=0 go build -gcflags="all=-N -l" -ldflags="$(LINKER_FLAGS)" -o $(BINARY_NAME) ./cmd/kpi-collector

build-dynamic-linking:
	CGO_ENABLED=1 go build -ldflags="$(LINKER_FLAGS) -extldflags '-z relro -z now'" -o $(BINARY_NAME) ./cmd/kpi-collector

build-dynamic-linking-debug:
	CGO_ENABLED=1 go build -gcflags="all=-N -l" -ldflags="$(LINKER_FLAGS)" -o $(BINARY_NAME) ./cmd/kpi-collector

# Mac installation via Homebrew
install-golangci-lint-mac:
	brew install golangci-lint

# Linux/CI installation via go install
install-golangci-lint-linux:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

# Platform-agnostic golangci-lint installation
install-golangci-lint:
ifeq ($(UNAME_S),Darwin)
	$(MAKE) install-golangci-lint-mac
else
	$(MAKE) install-golangci-lint-linux
endif

# Lint depends only on golangci-lint installation
lint: install-golangci-lint
	golangci-lint run --timeout 10m0s

# Run all tests
test:
	go test ./... -v -ginkgo.v

# Install kpi-collector to user's Go bin directory
install:
	go install ./cmd/kpi-collector
	echo "✓ Installed to $(HOME)/go/bin/$(BINARY_NAME)"

# Uninstall kpi-collector
uninstall:
	rm -f $(HOME)/go/bin/$(BINARY_NAME)
	echo "✓ Uninstalled"

# Install kpi-collector from upstream (no local source needed)
install-kpi-collector:
	go install $(MODULE_PATH)/cmd/kpi-collector@latest
