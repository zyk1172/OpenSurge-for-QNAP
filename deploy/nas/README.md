# Synology, fnOS and generic Docker NAS

Experimental **IPv4 / macvlan / same-LAN manual gateway** support. Physical-client, NAS reboot and soak testing on Synology/fnOS remains pending. QNAP keeps its [QNET deployment](../qnap/README.md).

Use a newly built image containing multi-NAS support, loaded from the matching amd64/arm64 CI archive. Historical image names remain `opensurge-for-qnap`; an old release with that name does not automatically contain this feature. Production NAS devices do not need Go or Node.

From `deploy/nas`, copy `.env.synology.example`, `.env.fnos.example` or `.env.example` to `.env`. Set the actual wired LAN parent interface, reserved container IPv4, canonical LAN CIDR, upstream router, dedicated absolute data directory and non-root Web UID/GID (`id <NAS-user>`). Example interface names, volume numbers and user IDs are not universal defaults.

```sh
sh ./preflight.sh --list-interfaces
sh ./preflight.sh
docker compose --env-file .env -f docker-compose.yml up -d
docker logs opensurge
```

Keep the adjacent `deploy/qnap/preflight.sh`, which provides shared checks. Rootful Docker, Compose, macvlan, `/dev/net/tun` and ingress-interface policy routing are required. Preflight tests persistence permissions and kernel routing in disposable containers without changing NAS routes.

Access `http://OpenSurge-IP:8080` from **another LAN device**, open the password-free management page, import a Mihomo profile, and start the gateway. Set one physical client's IPv4 gateway and DNS to the container IP and verify DNS, DIRECT, PROXY, TCP and UDP/QUIC.

macvlan isolates the NAS host from the container; host curl and NAS reverse proxies can therefore fail even when LAN clients work. Host Takeover is unavailable on these platforms. No Docker socket, host namespace, privileged mode or host-network gateway is used. Keep NAS gateway/DNS and main-router DHCP unchanged.

Synology's official project interface accepts Compose, while its network GUI documents bridge/host only. SSH + Compose is the recommended initial route. Verify package/model compatibility, TUN availability and all Compose parameters on the target device. The new profiles do not ship native `.spk`/`.fpk` packages.

Keep `/data` on updates. Existing persistent configuration is never overwritten by `.env`; changing network addresses requires synchronizing the stopped gateway's stored config and recreating this project's network/container. Do not delete persistent data. Full platform instructions and source-backed limitations: [Chinese deployment guide](README.zh-CN.md), [research and acceptance checklist](../../docs/NAS_ADAPTATION_RESEARCH.zh-CN.md).
