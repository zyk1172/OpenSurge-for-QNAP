# QNAP 局域网远程管理 API

OpenSurge for QNAP 提供独立的 Remote Management Token，使局域网内的 AI Agent、自动化脚本或其他可信客户端可以调用 OpenSurge 的管理能力，而不需要模拟浏览器登录，也不会暴露内部 Control Token。

## 架构边界

远程客户端只访问 QNAP 上已经对局域网提供服务的 Web Gateway：

```text
AI / Automation
    |
    | Authorization: Bearer osr_...
    v
http://<OpenSurge-IP>:8080/api/remote/v1/...
    |
    | Web Gateway 验证 Remote Management Token
    | 丢弃外部 Token，并注入内部 Control Token
    v
http://127.0.0.1:61767/api/v1/...
    |
    v
OpenSurge Control
```

内部 Control API 继续只监听 loopback，不会因为远程管理功能而暴露到 LAN。

Remote Management Token 与以下两种凭证彼此独立：

- Web 管理员 Cookie；
- Web Gateway -> Control 的内部 Control Token。

## 创建 Token

登录 OpenSurge Web GUI，进入 QNAP Network 页面，在 **LAN management token** 区域选择 **Create token**。

Token 格式：

```text
osr_<随机值>
```

完整 Token 只在创建或轮换时显示一次。OpenSurge 在 `/data/web-auth/remote-management-token.json` 中只保存 Token 的 SHA-256 摘要、显示前缀和创建时间，不保存完整明文 Token。

如果丢失 Token，请在 Web GUI 中 Rotate；旧 Token 会立即失效。

也可以随时 Revoke，撤销后现有 AI/API 客户端立即失去权限。

## 给 AI 的连接信息

最少只需要给 Agent 两项：

```text
Base URL: http://<OpenSurge-IP>:8080/api/remote/v1
Token: osr_...
```

所有请求使用：

```http
Authorization: Bearer osr_...
```

建议 Agent 的第一步始终调用：

```http
GET /api/remote/v1/capabilities
```

这个端点会返回机器可读的能力目录、认证方式、并发修改约束和主要管理端点。它同样要求有效 Token，因此不会向未认证的局域网客户端公开管理面信息。

## 基本示例

以下示例假设：

```sh
BASE='http://192.168.2.241:8080/api/remote/v1'
TOKEN='osr_REPLACE_ME'
```

### 查看总览

```sh
curl -fsS \
  -H "Authorization: Bearer $TOKEN" \
  "$BASE/overview"
```

### 查看当前配置

配置接口返回当前 revision，并使用 ETag 保护并发修改：

```sh
curl -i -fsS \
  -H "Authorization: Bearer $TOKEN" \
  "$BASE/config"
```

修改配置时必须先读取最新版本，再携带当前 `If-Match`：

```sh
curl -fsS -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'If-Match: "<CURRENT_REVISION>"' \
  --data @config.json \
  "$BASE/config"
```

不要让 Agent 在 revision 冲突后盲目覆盖；应重新 GET、理解新配置后再决定是否提交。

### 启动、停止和重载

```sh
curl -fsS -X POST -H "Authorization: Bearer $TOKEN" "$BASE/gateway/start"
curl -fsS -X POST -H "Authorization: Bearer $TOKEN" "$BASE/gateway/stop"
curl -fsS -X POST -H "Authorization: Bearer $TOKEN" "$BASE/gateway/reload"
curl -fsS -X POST -H "Authorization: Bearer $TOKEN" "$BASE/gateway/restart-mihomo"
```

网关生命周期操作可能返回异步 operation。收到 `id` 后应查询：

```sh
curl -fsS \
  -H "Authorization: Bearer $TOKEN" \
  "$BASE/operations/<OPERATION_ID>"
```

直到操作进入 `succeeded` 或 `failed`，不要因为客户端超时就自动重复执行 POST。

### 添加订阅 / 配置源

通过 URL 添加 mihomo profile：

```sh
curl -fsS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"name":"main","kind":"mihomo_profile","url":"https://example.com/profile.yaml"}' \
  "$BASE/sources"
```

查看来源：

```sh
curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE/sources"
```

之后可使用：

```text
POST /sources/{id}/refresh
GET  /sources/{id}/preview
POST /sources/{id}/apply
POST /sources/{id}/export
```

应用前建议 Agent 先读取 preview/diff，而不是看到新版本后直接覆盖当前运行配置。

### 设备与设备策略

```text
GET  /devices
GET  /device-traffic
GET  /device-policy
PUT  /device-policy
POST /devices/{device}/selectors/{slot}
POST /devices/{device}/connections/refresh
```

可用于：设备发现、批量策略生成、指定设备切换代理策略、策略修改后关闭旧连接等。

### 代理策略与出口

```text
GET  /policies
POST /policies/{group}/selection
POST /policy-workspace
GET  /local-routing
POST /local-routing
POST /local-routing/connections/refresh
GET  /proxy-health
POST /proxy-health/tests
GET  /providers
POST /providers/{name}/refresh
```

Agent 可以先测延迟/健康状态，再决定出口，而不是只按节点名称猜测。

### 连通性与诊断

```text
GET  /connectivity
POST /connectivity/tests
GET  /doctor
POST /doctor
GET  /diagnostics
GET  /operations
GET  /operations/{id}
GET  /events
```

这组接口适合 AI 做“先观察 -> 诊断 -> 修改 -> 再验证”的闭环。

### 网络信息

QNAP 版允许 AI 查看容器和 QNET 相关运行信息，但不会通过 Remote API 修改 QTS 宿主网络：

```text
GET  /network/interfaces
GET  /network/defaults
GET  /network/discovery
POST /network/dhcp-probe
```

以下 macOS/宿主网络操作在 QNAP 上仍然被禁止：

```text
/api/v1/network/apply-static
/api/v1/network/restore-dhcp
/api/v1/menubar
/api/v1/sleep-prevention
/api/v1/sources/{id}/reveal
```

Remote Management Token 不能绕过这些 QNAP 边界。

### Tailscale / Headscale

```text
GET  /tailscale
GET  /tailscale/discovery
PUT  /tailscale
POST /tailscale/forget-identity
```

## 推荐的 Agent 工作方式

给 AI 的系统提示或工具说明中建议明确以下规则：

1. 第一请求读取 `/capabilities`，不要假设接口版本。
2. 修改前读取相关当前状态和 revision。
3. 配置写入遵守 ETag / `If-Match`，遇到 409 必须重新读取。
4. 启停等异步动作只提交一次，再通过 `/operations/{id}` 跟踪。
5. 添加或更新 source 后先 preview，再决定 apply。
6. 修改代理/设备策略后根据需要刷新旧连接，并重新检查 connectivity/Doctor。
7. 不尝试访问 QNAP 明确禁止的宿主网络操作。

这种流程允许 Agent 获得很大的管理能力，同时保持操作可观察、可验证，并避免重复提交破坏性动作。

## 安全说明

Remote Management Token 等价于 OpenSurge 管理权限，应按管理员密钥对待。

- 默认面向可信 LAN；不要直接把 OpenSurge Web 端口裸露到公网。
- 如果需要跨互联网访问，优先通过可信 VPN/Tailscale，或在受控反向代理后使用 HTTPS。
- 不要把 Token 写入公开仓库、日志、截图或提示词模板。
- Token 泄露时直接在 Web GUI Rotate/Revoke。
- Remote Token 不能通过 Remote API 创建、轮换或撤销自身；这些动作必须由已登录 Web 管理员执行。
- 内部 Control Token 不会返回给 Remote API 客户端。
- 该功能不需要 Docker Socket，也不改变 QNAP Web 进程的 capability 边界。
