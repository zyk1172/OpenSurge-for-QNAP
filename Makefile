# OpenSurge for QNAP — Linux/QNAP Docker gateway derived from OpenSurge for Mac.
#
# The macOS-era targets (menubar, gui-installer, notarize, lima lab, same-lan)
# were removed with the macOS runtime. Tests run natively where possible; the
# Linux-only network tests and the namespace lab require a Linux host or
# container (see make lab-test-linux).

.PHONY: test build web-install web-build web-test lint
.PHONY: image image-check verify-third-party-notices
.PHONY: doctor status

test:
	go test ./...

# The Linux backend tests that mutate real host networking are opt-in, because
# a developer laptop must never be reconfigured by go test. Enable them inside a
# disposable container with CAP_NET_ADMIN:
#   docker run --rm --cap-add NET_ADMIN --cap-add NET_RAW \
#     -v "$$PWD":/src -w /src golang:1.25 \
#     bash -c 'apt-get update -qq && apt-get install -y -qq nftables iproute2 && \
#       OPEN_SURGE_NETWORK_TESTS=1 go test ./internal/platform/...'
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

# --- Linux network namespace lab -------------------------------------------------
# Three-namespace lab: client -> gateway -> upstream. Replaces the macOS
# Lima/vmnet lab that the upstream project used. Requires a Linux host with
# iproute2, nftables and CAP_NET_ADMIN; inside a container run with --privileged
# or --cap-add NET_ADMIN and a writable /proc/sys (sysctls).
lab-test-linux:
	./tests/labnetns/lab-test.sh

# --- Container image ------------------------------------------------------------
image:
	docker build -f docker/Dockerfile -t opensurge-qnap:dev .

image-check:
	docker run --rm --entrypoint opensurge-doctor opensurge-qnap:dev doctor

# CI entrypoint: fail when the version/checksum assertions in
# THIRD_PARTY_NOTICES.md disagree with the pins in docker/Dockerfile.
verify-third-party-notices:
	./scripts/verify-third-party-notices.sh
