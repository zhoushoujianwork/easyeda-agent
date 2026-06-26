.PHONY: help test fmt actions build daemon dev eext eext-fresh connector lint-test

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
	python3 tools/schematic-lint/tests/run.py

fmt: ## gofmt cmd + internal
	gofmt -w cmd internal

actions: ## print the typed action catalog
	go run ./cmd/easyeda actions

build: ## build bin/easyeda
	go build -o bin/easyeda ./cmd/easyeda

daemon: ## one-shot daemon (no reload) — prefer `make dev`
	go run ./cmd/easyeda daemon

# Live-reload the daemon for development (.air.toml): rebuilds + restarts on any
# .go change; the connector auto-reconnects (it port-scans 49620-49629). Keep
# this running in a terminal while developing so the daemon is always up.
dev: ## hot-reload the daemon (air) — mirrors output to tmp/daemon.log (truncated each start)
	@command -v air >/dev/null 2>&1 || { echo "air not found — install: go install github.com/air-verse/air@latest"; exit 1; }
	@mkdir -p tmp
	@# Kill any leftover daemon+watcher from a prior session so we always bind 49620.
	@pkill -TERM -f 'tmp/easyeda daemon' 2>/dev/null || true
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
