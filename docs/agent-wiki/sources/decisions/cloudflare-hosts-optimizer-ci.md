# Cloudflare Optimizer validation gates

Required before merge:

- `go test ./...`
- `go vet ./...`
- QNAP Web tests and QNAP-target build
- existing QNAP single-container CI
- security gate

Network behavior must remain fail-closed: the optimizer must refuse to use a probe route that does not resolve to the configured physical interface, and runtime application must continue through the existing transactional profile reload path.
