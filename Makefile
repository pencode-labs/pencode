GO ?= go
DIST_DIR ?= dist
ARGS ?=

.DEFAULT_GOAL := run
.PHONY: run release

ifeq ($(OS),Windows_NT)
SHELL := cmd.exe
.SHELLFLAGS := /C
EXE := .exe
endif

# Usage: make run ARGS="--ssh user@host --remote-path /path/to/project"
run:
	$(GO) run . $(ARGS)

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
