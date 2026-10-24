GO ?= go
DIST_DIR ?= dist
ARGS ?=
EXTENSIONS_DIR ?=

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
	$(GO) run . $(ARGS)

install-server:
	$(GO) build -o "$(SERVER_DIR)/pencode-server$(EXE)" ./cmd/pencode-server

install-extensions:
	$(GO) run ./cmd/pencode-install-extensions -go "$(GO)" -directory "$(EXTENSIONS_DIR)"

# Run the resulting executable from DIST_DIR so it can find resources/.
# Keep authoring resource support; onix_release requires binary resource packages.
release:
	$(GO) build -trimpath -ldflags="-s -w" -o "$(DIST_DIR)/pencode$(EXE)" .
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -Command "$$ErrorActionPreference = 'Stop'; New-Item -ItemType Directory -Force -Path '$(DIST_DIR)/resources' | Out-Null; Copy-Item -Path 'resources/*' -Destination '$(DIST_DIR)/resources' -Recurse -Force; Copy-Item -LiteralPath 'LICENSE' -Destination '$(DIST_DIR)/LICENSE' -Force"
else
	mkdir -p "$(DIST_DIR)/resources"
	cp -R resources/. "$(DIST_DIR)/resources/"
	cp LICENSE "$(DIST_DIR)/LICENSE"
endif
