GO ?= go
DIST_DIR ?= dist
VERSION ?= dev
ARGS ?=
EXTENSIONS_DIR ?=

# Go adds -lobjc for each Objective-C package.
ifeq ($(shell $(GO) env GOHOSTOS GOOS),darwin darwin)
export CGO_LDFLAGS := $(CGO_LDFLAGS) -Wl,-no_warn_duplicate_libraries
endif

.DEFAULT_GOAL := run
.PHONY: run install-server install-extensions release

ifeq ($(OS),Windows_NT)
SHELL := cmd.exe
.SHELLFLAGS := /C
EXE := .exe
SERVER_DIR ?= $(USERPROFILE)/.pencode
else
SERVER_DIR ?= $(HOME)/.pencode
endif

# Usage: make run ARGS="--ssh user@host --remote-path /path/to/project"
run: install-server install-extensions
	$(GO) run -ldflags "-X github.com/pencode-labs/pencode/internal/version.Value=$(VERSION)" . $(ARGS)

install-server:
	$(GO) build -ldflags "-X github.com/pencode-labs/pencode/internal/version.Value=$(VERSION)" -o "$(SERVER_DIR)/pencode-server$(EXE)" ./cmd/pencode-server

install-extensions:
	$(GO) run ./cmd/pencode-install-extensions -go "$(GO)" -directory "$(EXTENSIONS_DIR)"

# Build with Onix's release resource pipeline, then create a native installer.
release:
	$(GO) run ./cmd/pencode-release -output "$(DIST_DIR)" -version "$(VERSION)"
