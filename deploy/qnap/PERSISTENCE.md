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

## QNAP ownership and ACL model

QNAP shared folders are not plain generic-Linux directories. QTS/QuTS shared-folder permissions, Advanced Folder Permissions/ACLs, quotas and the numeric UID/GID visible to containers can all affect a bind mount.

OpenSurge therefore does **not** assume that every QNAP uses one fixed identity:

```text
OPENSURGE_WEB_UID=1000
OPENSURGE_WEB_GID=100
```

Those are only practical defaults: UID `1000` is common for the first normal NAS account and GID `100` is commonly the QNAP `everyone` group. Installations differ. Confirm the account that owns or is allowed to use the Container share on the NAS:

```sh
id <username>
```

Then put the numeric values in `deploy/qnap/.env`.

The privileged Control process remains responsible for gateway/runtime state. The LAN-facing Web process runs with the configured unprivileged UID/GID and only needs persistent write access to:

```text
/data/web-auth
```

The entrypoint **never recursively chowns `/data`**. It only assigns the OpenSurge-owned `web-auth` directory to the configured Web UID/GID. This avoids rewriting ownership across a QNAP shared folder that may carry QTS/QuTS ACLs or be managed by another NAS account.

Before deployment, run:

```sh
sh ./preflight.sh
```

The host-specific preflight uses the actual OpenSurge image and actual bind mount to verify:

1. container-root create/write/fsync/rename/delete operations under `/data`;
2. creation and ownership of `/data/web-auth` only;
3. create/rename/delete as `OPENSURGE_WEB_UID:OPENSURGE_WEB_GID` inside `/data/web-auth`.

A host-side `[ -w PATH ]` check is not considered sufficient because it can pass while the container identity or QNAP ACL still rejects the real operation.

If the probe fails, do **not** use `chmod -R 777` and do not recursively `chown` the shared folder. First check:

- `id <username>` on the NAS;
- QTS/QuTS shared-folder read/write permission;
- Advanced Folder Permissions / ACL inheritance;
- user/group quota;
- the numeric owner reported by `stat`/preflight.

## Directory layout

| Container path | Purpose | Persist across recreate | Include in normal backup |
| --- | --- | --- | --- |
| `/data/config` | Main OpenSurge configuration | **Required** | **Required** |
| `/data/control` | Internal control token and Control state | **Required** | **Required** |
| `/data/web-auth` | Web administrator credential hash | **Required** | **Required** |
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
web-auth/
profiles/
providers/
state/
backups/
```

`logs/` is optional.

Do **not** treat `runtime/` as portable configuration. It contains ownership information for a specific running/container instance.

## Safe in-place update

1. Keep the same `OPENSURGE_DATA_PATH`.
2. Keep the same verified `OPENSURGE_WEB_UID/GID`, unless the QNAP account/ACL changed.
3. Stop/recreate only the container/image, not the data directory.
4. Start the replacement container with the same bind mount.
5. Check:

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

Do not delete `config/`, `control/`, `web-auth/`, `profiles/`, `providers/`, `state/` or `backups/`.

Then update the new NAS deployment `.env`, especially:

- `OPENSURGE_PARENT_INTERFACE`
- `OPENSURGE_IP`
- `OPENSURGE_SUBNET`
- `OPENSURGE_GATEWAY`
- `OPENSURGE_DATA_PATH`
- `OPENSURGE_WEB_UID`
- `OPENSURGE_WEB_GID`

Run `sh ./preflight.sh` again on the destination NAS. The QNET parent interface, filesystem ACLs and numeric user/group identity are host-specific and must never be copied blindly from another NAS.
