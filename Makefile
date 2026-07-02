.PHONY: help test fmt actions api-index build install dev-build daemon dev eext eext-fresh connector lint-test release

DIST := dist

# Bare `make` prints the cheatsheet below.
.DEFAULT_GOAL := help

help: ## show this cheatsheet
	@echo "easyeda-agent — make targets"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Daemon runs under 'make dev' (air, background). To pick up daemon code"
	@echo "changes just edit a .go file — air reloads & the connector auto-reconnects."
	@echo "Don't kill/swap daemons by hand; it wedges the connector (→ click Reconnect)."

test: ## go test ./...
	go test ./...

# Rule-trust harness for the schematic linter: orientation-table consistency
# (orientation.json derives to its frozenTable; matches the connector) +
# fixture goldens (known-good board stays clean, known-bad cases still fire).
lint-test: ## linter rule-trust harness (orientation + fixtures)
	python3 skills/easyeda-agent/scripts/tests/run.py

fmt: ## gofmt cmd + internal
	gofmt -w cmd internal

actions: ## print the typed action catalog
	go run ./cmd/easyeda actions

api-index: ## regenerate the embedded eda.* API index (run after bumping pro-api-types)
	python3 internal/apidoc/gen.py

# Dev version stamp: `git describe` (e.g. v0.5.1-3-g1d7b7c8[-dirty]) so a locally
# built binary reports a meaningful version via `easyeda -v` instead of "dev".
DEV_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DEV_LDFLAGS := -X 'github.com/zhoushoujianwork/easyeda-agent/internal/version.Version=$(DEV_VERSION)'
# Where `make install` drops the binary (matches install.sh's default).
PREFIX ?= /usr/local

build: ## build bin/easyeda (version-stamped via git describe)
	go build -ldflags "$(DEV_LDFLAGS)" -o bin/easyeda ./cmd/easyeda

install: build ## build + install to $(PREFIX)/bin (default /usr/local/bin; may need sudo)
	@mkdir -p "$(PREFIX)/bin" 2>/dev/null || true
	@if install -m 0755 bin/easyeda "$(PREFIX)/bin/easyeda" 2>/dev/null; then \
		printf '✅ installed → %s/bin/easyeda  (%s)\n' "$(PREFIX)" "$(DEV_VERSION)"; \
	else \
		echo "  $(PREFIX)/bin not writable — retrying with sudo…"; \
		sudo install -m 0755 bin/easyeda "$(PREFIX)/bin/easyeda" && \
		printf '✅ installed → %s/bin/easyeda  (%s)\n' "$(PREFIX)" "$(DEV_VERSION)"; \
	fi

dev-build: ## (air hook) version-stamped build to bin + best-effort refresh of the PATH CLI
	@go build -ldflags "$(DEV_LDFLAGS)" -o bin/easyeda ./cmd/easyeda
	@install -m 0755 bin/easyeda "$(PREFIX)/bin/easyeda" 2>/dev/null \
		&& printf '  ↻ PATH CLI refreshed → %s/bin/easyeda (%s)\n' "$(PREFIX)" "$(DEV_VERSION)" \
		|| printf '  ⚠ PATH CLI NOT refreshed (%s/bin not writable) — run `make install` once with sudo\n' "$(PREFIX)"

daemon: ## one-shot daemon (no reload) — prefer `make dev`
	go run ./cmd/easyeda daemon

# Live-reload the daemon for development (.air.toml): rebuilds + restarts on any
# .go change; the connector auto-reconnects (it port-scans 49620-49629). Keep
# this running in a terminal while developing so the daemon is always up.
dev: ## hot-reload the daemon (air) — mirrors output to tmp/daemon.log (truncated each start)
	@command -v air >/dev/null 2>&1 || { echo "air not found — install: go install github.com/air-verse/air@latest"; exit 1; }
	@mkdir -p tmp
	@# Kill any leftover daemon+watcher from a prior session so we always bind 49620.
	@pkill -TERM -f '/easyeda daemon' 2>/dev/null || true
	@sleep 0.4
	air 2>&1 | tee tmp/daemon.log

# Build the connector .eext at the CURRENT version (no bump).
connector: ## build .eext at the current version/uuid (no bump)
	npm --prefix extension run build

# Cut an importable connector .eext (default: STABLE uuid). Bump PATCH + typecheck
# + build. EasyEDA dedups installed extensions by uuid, so to load this you update
# in place: uninstall the old one in EasyEDA's 已安装 tab, then import the printed
# .eext. Keeps ONE extension entry. Use `make eext-fresh` only if the installed
# one won't uninstall.
eext: ## bump patch + build importable .eext (STABLE uuid; uninstall old → import)
	node extension/scripts/bump.mjs patch
	npm --prefix extension run typecheck
	npm --prefix extension run build
	@printf '\n✅ uninstall old in 已安装, then import → extension/build/dist/easyeda-agent-connector_v%s.eext\n' "$$(node -p "require('./extension/extension.json').version")"

# Fallback only: mint a FRESH uuid so it imports as a NEW extension with no
# uninstall — but it leaves a duplicate "EasyEDA Agent" entry you must delete
# afterward (else multiple connectors fight over the daemon).
eext-fresh: ## bump patch + FRESH uuid (imports as new entry; delete the old one)
	node extension/scripts/bump.mjs patch --uuid
	npm --prefix extension run typecheck
	npm --prefix extension run build
	@printf '\n✅ fresh-uuid build → import extension/build/dist/easyeda-agent-connector_v%s.eext, then DELETE the old entry\n' "$$(node -p "require('./extension/extension.json').version")"

# ── Release ───────────────────────────────────────────────────────────────────
# Usage: make release VERSION=v0.2.0
# Prerequisites:
#   1. gh CLI logged in (gh auth login)
#   2. connector built: make eext   (only needed when connector changed)
#   3. repo is public or you have release permissions
#
# What it does:
#   • cross-compiles CLI for darwin/linux/windows (amd64 + arm64)
#   • copies the latest .eext from extension/build/dist/
#   • tarballs the merged easyeda-agent skill into skills.tar.gz
#   • creates a git tag, pushes it, and creates a GitHub Release with all assets
_LDFLAGS = -s -w -X 'github.com/zhoushoujianwork/easyeda-agent/internal/version.Version=$(VERSION)'

release: ## cross-compile + package + GitHub Release  (VERSION=vX.Y.Z required)
ifndef VERSION
	$(error VERSION is required — usage: make release VERSION=v0.5.1)
endif
	@echo "── Building release $(VERSION) ──"
	rm -rf $(DIST) && mkdir -p $(DIST)
	@echo "  syncing connector version to $(VERSION)..."
	node extension/scripts/bump.mjs $(VERSION:v%=%) --require-changelog
	npm --prefix extension run typecheck
	npm --prefix extension run build
	@echo "  compiling CLI..."
	GOOS=darwin  GOARCH=amd64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_darwin_amd64      ./cmd/easyeda
	GOOS=darwin  GOARCH=arm64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_darwin_arm64      ./cmd/easyeda
	GOOS=linux   GOARCH=amd64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_linux_amd64       ./cmd/easyeda
	GOOS=linux   GOARCH=arm64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_linux_arm64       ./cmd/easyeda
	GOOS=windows GOARCH=amd64  go build -ldflags "$(_LDFLAGS)" -o $(DIST)/easyeda_windows_amd64.exe ./cmd/easyeda
	@echo "  packaging connector..."
	@EEXT=$$(ls extension/build/dist/*.eext 2>/dev/null | sort -V | tail -1); \
	 [ -n "$$EEXT" ] || { echo "connector build failed"; exit 1; }; \
	 cp "$$EEXT" $(DIST)/easyeda-agent-connector.eext && echo "  $$EEXT → connector.eext"
	@echo "  packaging skills..."
	tar --exclude='*/__pycache__' --exclude='*.pyc' -czf $(DIST)/skills.tar.gz -C skills easyeda-agent
	cp install.sh $(DIST)/install.sh
	@echo "  creating GitHub release..."
	git tag -a $(VERSION) -m "Release $(VERSION)" 2>/dev/null || echo "  (tag $(VERSION) already exists, reusing)"
	git push origin $(VERSION)
	gh release create $(VERSION) \
		$(DIST)/easyeda_darwin_amd64 \
		$(DIST)/easyeda_darwin_arm64 \
		$(DIST)/easyeda_linux_amd64 \
		$(DIST)/easyeda_linux_arm64 \
		$(DIST)/easyeda_windows_amd64.exe \
		$(DIST)/easyeda-agent-connector.eext \
		$(DIST)/skills.tar.gz \
		$(DIST)/install.sh \
		--title "easyeda-agent $(VERSION)" \
		--notes "$$(printf 'One-line install/update:\n\`\`\`\ncurl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | sh\n\`\`\`\n\nInstalls/updates:\n- easyeda CLI/daemon\n- easyeda-agent skill for Codex (~/.codex/skills) and/or Claude Code (~/.claude/skills) when detected\n- prints EasyEDA connector .eext import URL\n\nSkill targets: set \`EASYEDA_INSTALL_SKILLS=codex,claude\` to force targets, \`none\` to skip, or \`EASYEDA_SKILL_PRESERVE=1\` to keep local edits.')"
	@echo "✅ Released: https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/$(VERSION)"
