# OpenSurge for QNAP persistence model

The supported QNAP deployment persists the **entire `/data` tree as one bind mount**.

Recommended host path:

```text
/share/Container/opensurge
```

Compose maps it to:

```text
/share/Container/opensurge  ->  /data
```

Do not split the directories below into anonymous Docker volumes unless you fully understand the recovery semantics. `runtime/` is not ordinary user data, but it must survive an in-place container recreation so OpenSurge can recognize and reconcile interrupted network state safely.

## Directory layout

| Container path | Purpose | Persist across recreate | Include in normal backup |
| --- | --- | --- | --- |
| `/data/config` | Main OpenSurge configuration | **Required** | **Required** |
| `/data/control` | Administrator credential hash, internal control token and Web control state | **Required** | **Required** |
| `/data/profiles` | Imported/managed profile data | **Required** | **Required** |
| `/data/providers` | Provider/rule-provider files and caches used by the active configuration | Recommended | Recommended |
| `/data/state` | Durable feature/integration state, for example optional Tailscale state | **Required when used** | **Required when used** |
| `/data/backups` | OpenSurge-created configuration backups | Recommended | Recommended |
| `/data/runtime` | Rendered runtime files, applied-state journal, PID/fingerprint and network-namespace reconciliation data | **Required for in-place recovery** | Usually **exclude** from disaster/migration backup |
| `/data/logs` | OpenSurge component logs | Recommended for diagnostics | Optional |
| `/data/licenses` | Runtime license/notice copies | Optional | Not necessary |

## Why `/data/runtime` is persisted

Container Station may recreate a container with a new Linux network namespace while reusing the same host `boot_id`. OpenSurge records both runtime ownership and network-namespace identity. Keeping `/data/runtime` across an **in-place upgrade/recreate** lets the replacement container classify the old state as interrupted and clean it up without signalling unrelated reused PIDs.

Therefore:

- in-place update on the same NAS: preserve the whole `/data` tree, including `runtime/`;
- ordinary stop/start or container recreation: preserve the whole `/data` tree;
- migration/disaster restore to another NAS: restore user/configuration data, but remove stale `/data/runtime` before the first start on the new NAS.

## Recommended backup set

For a backup intended to restore OpenSurge configuration on another system, include at least:

```text
config/
control/
profiles/
providers/
state/
backups/
```

`logs/` is optional.

Do **not** treat `runtime/` as portable configuration. It contains ownership information for a specific running/container instance.

## Safe in-place update

1. Keep the same `OPENSURGE_DATA_PATH`.
2. Stop/recreate only the container/image, not the data directory.
3. Start the replacement container with the same bind mount.
4. Check:

```sh
docker exec opensurge omg status --config /data/config/opensurge.yaml --format json
```

If it reports an interrupted runtime, run:

```sh
docker exec opensurge omg stop --config /data/config/opensurge.yaml
```

before starting the gateway again.

## Migration to another NAS

After restoring the persistent directory to the new NAS, remove only stale runtime ownership data before first start:

```sh
rm -rf /share/Container/opensurge/runtime/*
```

Do not delete `config/`, `control/`, `profiles/`, `providers/`, `state/` or `backups/`.

Then update the new NAS deployment `.env`, especially:

- `OPENSURGE_PARENT_INTERFACE`
- `OPENSURGE_IP`
- `OPENSURGE_SUBNET`
- `OPENSURGE_GATEWAY`
- `OPENSURGE_DATA_PATH`

The QNET parent interface is host-specific and must never be copied blindly from another NAS.
