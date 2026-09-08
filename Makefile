# OpenSurge for QNAP — Linux/QNAP gateway port derived from OpenSurge for Mac.
# Phase 1 intentionally exposes only targets backed by files already present in
# this branch. Docker image, namespace-lab and release-verification targets are
# added in later phases together with their implementations.

.PHONY: test test-network-linux build web-install web-build web-test lint doctor status

test:
	go test ./...

# These tests mutate the current Linux network namespace and are opt-in. Run
# only in a disposable namespace/container with NET_ADMIN and nft/ip installed.
test-network-linux:
	OPEN_SURGE_NETWORK_TESTS=1 go test ./internal/platform/linux/

build:
	go build -o bin/omg ./cmd/omg
	go build -o bin/opensurge-control ./cmd/opensurge-control

lint:
	go vet ./...
	cd web && pnpm run lint

web-install:
	cd web && pnpm install

web-build:
	cd web && pnpm run build

web-test:
	cd web && pnpm run test

doctor:
	go run ./cmd/omg doctor --config examples/config.example.yaml

status:
	go run ./cmd/omg status --config examples/config.example.yaml
