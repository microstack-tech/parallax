# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: prlx prlx-gui prlx-gui-cross prlx-gui-package android ios pvm all test clean devtools lint cross package darwin-universal release

GOBIN      = ./build/bin
GO        ?= latest
GORUN      = env GO111MODULE=on go run
PKG        = ./cmd/prlx
LDFLAGS   ?= -s -w
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null)

# -------- existing targets --------

prlx:
	$(GORUN) build/ci.go install ./cmd/prlx
	@echo "Done building."
	@echo "Run \"$(GOBIN)/prlx\" to launch parallax."

# On Linux, Wails 2.x defaults to webkit2gtk-4.0 but Arch (and recent
# Debian/Ubuntu) only ship 4.1. The webkit2_41 build tag selects the
# newer ABI. Override with `make prlx-gui WAILS_TAGS=` to opt out.
WAILS_TAGS ?= webkit2_41

prlx-gui:
	@command -v wails >/dev/null 2>&1 || { \
	  echo "wails CLI not found. Install with:"; \
	  echo "  go install github.com/wailsapp/wails/v2/cmd/wails@latest"; \
	  exit 1; \
	}
	cd cmd/prlx-gui && wails build -clean -tags "$(WAILS_TAGS)" -ldflags "$(LDFLAGS)"
	@echo "Done building Parallax Desktop."
	@echo "Binary in cmd/prlx-gui/build/bin/"

prlx-gui-cross:
	@command -v wails >/dev/null 2>&1 || { \
	  echo "wails CLI not found. Install with:"; \
	  echo "  go install github.com/wailsapp/wails/v2/cmd/wails@latest"; \
	  exit 1; \
	}
	cd cmd/prlx-gui && wails build -clean -tags "$(WAILS_TAGS)" -platform linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64 -ldflags "$(LDFLAGS)"
	@echo "Done cross-building Parallax Desktop."

# prlx-gui-package builds the desktop GUI for every entry in GUI_TARGETS
# and writes one parallax-gui-<os>-<arch>.{zip,tar.gz} per target into
# PACKAGEDIR. Wails GUI cross-compilation is best-effort: building for
# darwin/* from a non-mac host requires osxcross, building for windows/*
# from a non-windows host requires mingw-w64, etc. When the toolchain is
# missing the per-target build prints a warning and the loop continues so
# the rest of the bundles still get produced.
GUI_TARGETS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

prlx-gui-package:
	@command -v wails >/dev/null 2>&1 || { \
	  echo "wails CLI not found. Install with:"; \
	  echo "  go install github.com/wailsapp/wails/v2/cmd/wails@latest"; \
	  exit 1; \
	}
	@set -euo pipefail; \
	REPO_ROOT="$(REPO_ROOT)"; \
	PACKAGEDIR="$(PACKAGEDIR)"; \
	GUI_BIN="$$REPO_ROOT/cmd/prlx-gui/build/bin"; \
	mkdir -p "$$PACKAGEDIR"; \
	for t in $(GUI_TARGETS); do \
	  os="$${t%/*}"; \
	  arch="$${t#*/}"; \
	  echo "==> wails build for $$os/$$arch"; \
	  rm -rf "$$GUI_BIN"; \
	  if ! ( cd "$$REPO_ROOT/cmd/prlx-gui" && wails build -clean -tags "$(WAILS_TAGS)" -platform "$$t" -ldflags "$(LDFLAGS)" ); then \
	    echo "   !! skipped $$os/$$arch (wails build failed — usually missing native toolchain)"; \
	    continue; \
	  fi; \
	  if [ ! -d "$$GUI_BIN" ] || [ -z "$$(ls -A "$$GUI_BIN" 2>/dev/null)" ]; then \
	    echo "   !! skipped $$os/$$arch (no output in build/bin)"; \
	    continue; \
	  fi; \
	  bundle="parallax-gui-$$os-$$arch"; \
	  out="$$PACKAGEDIR/$$bundle.zip"; \
	  rm -f "$$out"; \
	  ( cd "$$GUI_BIN" && zip -9 -r "$$out" . >/dev/null ); \
	  echo "-> Packaged $$(basename "$$out")"; \
	done; \
	if ls "$$PACKAGEDIR"/parallax-gui-*.zip >/dev/null 2>&1; then \
	  if command -v shasum >/dev/null 2>&1; then \
	    ( cd "$$PACKAGEDIR" && shasum -a 256 parallax-gui-*.zip >> SHA256SUMS.txt ); \
	  else \
	    ( cd "$$PACKAGEDIR" && sha256sum parallax-gui-*.zip >> SHA256SUMS.txt ); \
	  fi; \
	fi; \
	echo "GUI bundles in $$PACKAGEDIR/"

all:
	$(GORUN) build/ci.go install

android:
	$(GORUN) build/ci.go aar --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/parallax.aar\" to use the library."
	@echo "Import \"$(GOBIN)/parallax-sources.jar\" to add javadocs"
	@echo "For more info see https://stackoverflow.com/questions/20994336/android-studio-how-to-attach-javadoc"

ios:
	$(GORUN) build/ci.go xcode --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/Parallax.framework\" to use the library."

test: all
	$(GORUN) build/ci.go test

lint: ## Run linters.
	$(GORUN) build/ci.go lint

clean:
	env GO111MODULE=on go clean -cache
	rm -fr build/_workspace/pkg/ $(GOBIN)/* build/cross build/package build/ci

devtools:
	env GOBIN= go install golang.org/x/tools/cmd/stringer@latest
	env GOBIN= go install github.com/fjl/gencodec@latest
	env GOBIN= go install github.com/golang/protobuf/protoc-gen-go@latest
	env GOBIN= go install ./cmd/abigen
	@type "solc"  2> /dev/null || echo 'Please install solc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

SHELL := /bin/bash


# -------- cross-build & packaging via build/ci.go (uses env GOBIN) --------

CMDS       ?=
TARGETS    := linux/amd64 linux/arm64 linux/armv7 linux/386 windows/amd64 windows/386 darwin/amd64 darwin/arm64
REPO_ROOT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
CROSSDIR   ?= $(REPO_ROOT)build/cross
PACKAGEDIR ?= $(REPO_ROOT)build/package
CICMD       := build/ci
USE_DLGO ?= 1
DLGO_FLAG := $(if $(filter 1 true yes,$(USE_DLGO)),-dlgo,)
BUNDLE_PREFIX ?= parallax
LICENSE_FILES ?= COPYING LICENSE
CMDS_RELEASE := clef parallaxkey prlx

# Build the helper once for the host
$(CICMD): build/ci.go
	@mkdir -p $(dir $@)
	go build -trimpath -o $@ build/ci.go

# Expand targets like "linux/amd64" -> "build/cross/linux-amd64"
CROSS_TARGETS := $(addprefix $(CROSSDIR)/,$(subst /,-,$(TARGETS)))

# Per-target build rule. Invoked once per OS/arch.
$(CROSSDIR)/%: $(CICMD)
	@set -euo pipefail; \
	GOOS="$(word 1,$(subst -, ,$*))"; \
	ARCH="$(word 2,$(subst -, ,$*))"; \
	GOARCH="$$ARCH"; GOARM=""; \
	if [[ "$$ARCH" == "armv7" ]]; then GOARCH="arm"; GOARM="7"; fi; \
	echo "==> ci install for $$GOOS/$$GOARCH$${GOARM:+ (GOARM=$$GOARM)}"; \
	mkdir -p "$@"; \
	rm -f "$@"/*; \
	env CGO_ENABLED=0 GOOS="$$GOOS" GOARCH="$$GOARCH" GOARM="$$GOARM" GOBIN="$@" \
	  $(CICMD) install $(DLGO_FLAG) $(foreach c,$(CMDS),./cmd/$(c))

cross: $(CROSS_TARGETS)
	@echo "Cross-compiled binaries under $(CROSSDIR)/<os>-<arch>/"

package: cross
	@set -euo pipefail; \
	REPO_ROOT="$(REPO_ROOT)"; \
	CROSSDIR="$(CROSSDIR)"; \
	PACKAGEDIR="$(PACKAGEDIR)"; \
	BUNDLE_PREFIX="$(BUNDLE_PREFIX)"; \
	: "$$REPO_ROOT" "$$CROSSDIR" "$$PACKAGEDIR" "$$BUNDLE_PREFIX"; \
	rm -rf "$$PACKAGEDIR"; \
	mkdir -p "$$PACKAGEDIR"; \
	shopt -s nullglob; \
	for dir in "$$CROSSDIR"/*; do \
	  [ -d "$$dir" ] || continue; \
	  base="$$(basename "$$dir")"; \
	  GOOS="$${base%-*}"; \
	  ARCH="$${base#*-}"; \
	  tmpdir="$$(mktemp -d)"; \
	  cp -a "$$dir/"* "$$tmpdir/"; \
	  for lic in COPYING LICENSE; do \
	    [ -f "$$REPO_ROOT/$$lic" ] && cp "$$REPO_ROOT/$$lic" "$$tmpdir/" || true; \
	  done; \
	  bundle="$$BUNDLE_PREFIX-$$GOOS-$$ARCH"; \
	  if [ "$$GOOS" = "windows" ]; then \
	    out="$$PACKAGEDIR/$$bundle.zip"; \
	    ( cd "$$tmpdir" && zip -9 -r "$$out" . >/dev/null ); \
	  else \
	    out="$$PACKAGEDIR/$$bundle.tar.gz"; \
	    ( cd "$$tmpdir" && find . -type f -print0 | xargs -0 touch -t 202001010000; \
	      tar --mtime='UTC 2020-01-01' --owner=0 --group=0 --numeric-owner -cf - -C "$$tmpdir" . | gzip -n > "$$out" ); \
	  fi; \
	  rm -rf "$$tmpdir"; \
	  echo "-> Packaged $$(basename "$$out")"; \
	done; \
	if ls "$$PACKAGEDIR"/*.zip "$$PACKAGEDIR"/*.tar.gz >/dev/null 2>&1; then \
	  if command -v shasum >/dev/null 2>&1; then \
	    ( cd "$$PACKAGEDIR" && shasum -a 256 *.zip *.tar.gz > SHA256SUMS.txt ); \
	  else \
	    ( cd "$$PACKAGEDIR" && sha256sum *.zip *.tar.gz > SHA256SUMS.txt ); \
	  fi; \
	fi; \
	echo "Bundles + checksums in $$PACKAGEDIR/"

release: export CMDS := $(CMDS_RELEASE)
release:
	@echo "==> Cleaning old build outputs"
	env GO111MODULE=on go clean -cache
	rm -fr build/_workspace/pkg/ $(GOBIN)/* build/cross build/package build/ci
	rm -rf $(CROSSDIR) $(PACKAGEDIR)
	@echo "==> Cross-compiling CLI binaries: $(CMDS)"
	$(MAKE) package
	@echo "==> Cross-building Parallax Desktop GUI"
	$(MAKE) prlx-gui-package
