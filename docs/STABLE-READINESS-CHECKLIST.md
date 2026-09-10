# QNAP stable-readiness verification checklist

Before merging a stable-readiness change, verify all of the following:

- same-LAN policy routing has both the primary `iif -> table` rule and the later `iif -> prohibit` guard;
- removing either the primary selector or the TUN default route cannot fall through to the namespace main table;
- first administrator setup rejects missing/incorrect bootstrap tokens and consumes a valid token exactly once;
- the LAN Web process runs unprivileged with no effective capabilities while Control remains loopback-only;
- the QNAP runtime image contains neither `opensurge-manager` nor `opensurge-orchestrator`;
- TUN mode keeps Mihomo mixed proxy and internal DNS listeners on loopback;
- source URL dialing rejects private, CGNAT/Tailscale, documentation, benchmark and reserved destinations;
- `/health/ready` authenticates to a real Control API endpoint;
- existing administrator credentials survive container recreation without generating a new bootstrap token;
- amd64 and arm64 images build successfully;
- rolling test releases publish Docker archives, SPDX JSON SBOMs, checksums and GitHub attestations.
