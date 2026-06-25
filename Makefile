.PHONY: test fmt actions build daemon

test:
	go test ./...

fmt:
	gofmt -w cmd internal

actions:
	go run ./cmd/easyeda actions

build:
	go build -o bin/easyeda ./cmd/easyeda

daemon:
	go run ./cmd/easyeda daemon
