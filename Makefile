# OpenSurge for QNAP — Linux/QNAP Docker gateway derived from OpenSurge for Mac.

.PHONY: test test-network-linux lab-test-linux build web-install web-build web-test lint doctor status image image-smoke compose-qnap-check

test:
	go test ./...

# These tests mutate the current Linux network namespace and are opt-in. Run
# only in a disposable namespace/container with NET_ADMIN and nft/ip installed.
test-network-linux:
	OPEN_SURGE_NETWORK_TESTS=1 go test ./internal/platform/linux/

# Creates three disposable Linux network namespaces (client -> gateway ->
# upstream) and executes the network integration binary only inside the gateway
# namespace. Requires Linux + passwordless sudo/root + iproute2/nftables/ping.
lab-test-linux:
	bash ./tests/labnetns/lab-test.sh

build:
	go build -o bin/omg ./cmd/omg
	go build -o bin/opensurge-control ./cmd/opensurge-control
	go build -o bin/opensurge-container ./cmd/opensurge-container

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

image:
	docker build -f docker/Dockerfile -t opensurge-for-qnap:dev .

image-smoke: image
	rm -rf /tmp/opensurge-image-smoke && mkdir -p /tmp/opensurge-image-smoke
	docker rm -f opensurge-image-smoke >/dev/null 2>&1 || true
	docker run -d --name opensurge-image-smoke -p 127.0.0.1:18080:8080 -v /tmp/opensurge-image-smoke:/data opensurge-for-qnap:dev
	@for i in $$(seq 1 30); do curl -fsS http://127.0.0.1:18080/health/ready >/dev/null && break; sleep 1; done
	curl -fsS http://127.0.0.1:18080/api/auth/state
	docker rm -f opensurge-image-smoke >/dev/null

compose-qnap-check:
	cd deploy/qnap && \
	OPENSURGE_IP=192.168.50.2 \
	OPENSURGE_SUBNET=192.168.50.0/24 \
	OPENSURGE_GATEWAY=192.168.50.1 \
	OPENSURGE_PARENT_INTERFACE=eth0 \
	docker compose -f docker-compose.yml config --quiet
