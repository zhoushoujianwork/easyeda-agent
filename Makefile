.PHONY: test fmt actions build daemon dev eext connector lint-test

test:
	go test ./...

# Rule-trust harness for the schematic linter: orientation-table consistency
# (orientation.json derives to its frozenTable; matches the connector) +
# fixture goldens (known-good board stays clean, known-bad cases still fire).
lint-test:
	python3 tools/schematic-lint/tests/run.py

fmt:
	gofmt -w cmd internal

actions:
	go run ./cmd/easyeda actions

build:
	go build -o bin/easyeda ./cmd/easyeda

daemon:
	go run ./cmd/easyeda daemon

# Live-reload the daemon for development (.air.toml): rebuilds + restarts on any
# .go change; the connector auto-reconnects (it port-scans 49620-49629). Keep
# this running in a terminal while developing so the daemon is always up.
dev:
	@command -v air >/dev/null 2>&1 || { echo "air not found — install: go install github.com/air-verse/air@latest"; exit 1; }
	air

# Build the connector .eext at the CURRENT version (no bump).
connector:
	npm --prefix extension run build

# Cut a fresh, importable connector .eext: bump the PATCH version AND mint a new
# uuid (scripts/bump.mjs), typecheck, build. EasyEDA dedups installed extensions
# by UUID — a version bump alone will NOT re-import (it silently fails); the fresh
# uuid is what unblocks it. Prints the .eext path to import in EasyEDA.
eext:
	node extension/scripts/bump.mjs patch
	npm --prefix extension run typecheck
	npm --prefix extension run build
	@printf '\n✅ import in EasyEDA → extension/build/dist/easyeda-agent-connector_v%s.eext\n' "$$(node -p "require('./extension/extension.json').version")"
