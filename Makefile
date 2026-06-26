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

# Bump the connector version + build a fresh, importable .eext.
# EasyEDA refuses to re-import an .eext whose (uuid, version) is already
# installed, so use this whenever the user needs to load new connector code.
eext:
	npm --prefix extension run release
