# Ghoul x Toshell Integration

本目录冻结 Ghoul 与 Toshell 的 Phase 0 集成契约。Toshell 以独立 sidecar 运行，Ghoul 只通过服务端 REST 适配器访问它；不复制 Toshell 源码，不把 Toshell Web 控制台嵌入 Ghoul。

## 基线

- Toshell：`v1.4.3`，commit `8bf4643c1affbe109993123475422f6d26897903`
- 许可证：MIT
- 上游：<https://github.com/iQingshan/Toshell>
- 已冻结端点：`GET /api/v1/health`、`GET /api/v1/mcp/tools`、`GET /api/v1/sessions`
- 工具表：38 个，read 13 / confirm 14 / danger 11
- Ghoul Agent 工具面：8 个原生 `c2_*` 工具，Planner 默认只加载其中 3 个只读工具

版本和许可证元数据见 [`source.json`](source.json)。完整工具等级见 [`contract/expected_levels.json`](contract/expected_levels.json)，裁剪后的工具面见 [`tool_surface.json`](tool_surface.json)。

## 安全边界

- `c2.enabled` 默认 `false`；启用后也默认关闭 Confirm 与 Danger。
- Toshell API Key 只允许由 Ghoul 服务端适配器从 secret store 读取，不得进入浏览器、Agent、任务目录或日志。
- `api_key_ref` 只保存引用，例如 `env:TOSHELL_API_KEY`，不得保存明文 Key。
- Toshell API 只监听回环或专用内网地址；控制台只对运维网/VPN 开放。
- Toshell AI 与对外 MCP 默认关闭。
- Phase 0 不执行任何 C2 操作；契约采集时监听器、AI、MCP 全部关闭。

## 启动 sidecar

1. 复制 [`server.yaml.example`](server.yaml.example) 到受控配置目录。
2. 替换 API Key、JWT Key、Listener Encryption Key、管理员口令和 Basic Auth 口令。
3. 从锁定的 Toshell commit 构建并启动：

```powershell
cd E:\SecTools\AI-Agent\ARTEX-fork\Toshell
go build -o E:\SecTools\AI-Agent\Ghoul\.tmp\toshell-contract\toserver.exe ./cmd/server
& E:\SecTools\AI-Agent\Ghoul\.tmp\toshell-contract\toserver.exe `
  -config <受控目录>\server.yaml
```

健康检查不需要认证，其他管理接口使用 `X-API-Key` 或 Bearer JWT：

```powershell
Invoke-RestMethod http://127.0.0.1:18081/api/v1/health
Invoke-RestMethod http://127.0.0.1:18081/api/v1/sessions `
  -Headers @{ 'X-API-Key' = $env:TOSHELL_API_KEY }
```

## Ghoul 配置

```json
{
  "c2": {
    "enabled": false,
    "provider": "toshell",
    "base_url": "http://127.0.0.1:18081",
    "api_key_ref": "env:TOSHELL_API_KEY",
    "allow_confirm": false,
    "allow_danger": false
  }
}
```

配置解析会强制 fail-closed：未知 provider 会关闭整个适配器；未启用适配器时 Confirm/Danger 都关闭；Danger 不能在 Confirm 关闭时单独开启。

## 刷新契约快照

脚本会构建锁定版本的 Toshell，在随机回环端口启动隔离 sidecar，采集健康检查、工具表和空会话列表，并重新计算 token 基线：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass `
  -File .\integrations\toshell\scripts\capture-contract.ps1
```

已有可信 Toshell 二进制时可跳过构建：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass `
  -File .\integrations\toshell\scripts\capture-contract.ps1 `
  -ServerExe .\.tmp\toshell-contract\toserver.exe -SkipBuild
```

离线执行契约测试：

```powershell
go test ./integrations/toshell/contract
```

## 工具面

第一版只暴露 8 个 Ghoul 原生工具：

| 工具 | 等级 | Planner | 用途 |
|---|---|---|---|
| `c2_session_list` | read | 是 | 列出已绑定会话 |
| `c2_session_context` | read | 是 | 读取会话身份与近期任务摘要 |
| `c2_recon` | read | 是 | 白名单只读侦察 |
| `c2_file_list` | confirm | 否 | 列出目录 |
| `c2_process_list` | confirm | 否 | 列出进程 |
| `c2_screenshot` | confirm | 否 | 截屏 |
| `c2_exec` | danger | 否 | 任意命令，必须一次性审批 |
| `c2_tunnel` | confirm | 否 | 查看、启动、停止隧道 |

不向 Agent 暴露 `exec_module`、BYOVD、EDR、PPL、持久化、注入、AV-Ops L1+，也不直接注册 Toshell 的 38 个 MCP 工具。

## Token 基线

采集脚本使用与 Toshell 上下文估算一致的公开口径：CJK 字符约 1 token/字，其他字符约 4 字符/token，向上取整，并额外保留 1.3 倍安全余量。`v1.4.3` 实测：

| 工具面 | 数量 | JSON 字节 | 估算 tokens | 含安全余量 |
|---|---:|---:|---:|---:|
| Toshell 全量 | 38 | 36,729 | 10,705 | 13,917 |
| Ghoul 原生 `c2_*` | 8 | 3,508 | 955 | 1,242 |

Ghoul 原生工具面的 schema 预算为 `2,000` tokens，单次工具输出内联预算为 `4,000` tokens。完整数值见 [`contract/token_baseline.json`](contract/token_baseline.json)。

## UI 边界

| 能力 | Ghoul UI | Toshell 控制台 |
|---|---|---|
| 实例健康、会话、监听器状态 | 原生只读/常用管理 | 保留 |
| 会话绑定任务/资产、审批、证据 | 原生主入口 | 保留 |
| 文件、进程、截图、隧道 | 原生审批式操作 | 保留 |
| 载荷构建、监听器高级配置、插件 | 不做 | 主入口 |
| AV-Ops、BYOVD、EDR、PPL、驱动、注入 | 不做 | 仅运维网/VPN |
| 原始交互 Shell | 新窗口深链接 | 主入口 |

禁止 iframe。高级操作使用新窗口深链接，避免 CSP、SSO、Cookie、WebSocket 和权限状态耦合。

## Phase 3 只读接入

Phase 3 已接入服务端 REST/WS 客户端和 Ghoul 原生只读页面：

- 新增 `integrations/toshell/client`，只实现 GET 读取；API Key 支持 `env:NAME` 与 `file:PATH`，明文引用直接拒绝。
- 同步 `/api/v1/sessions`、`/api/v1/tasks`、`/api/v1/listeners`、`/api/v1/channels/health` 和 `/api/v1/health`。
- 消费 `/api/v1/ws/events`；保存 `c2_event_cursor`，断线后保留最后事件键，再执行全量对账。Toshell WS 没有可回放的事件 ID，因此断线期间事件以全量对账收敛。
- Ghoul API：`/api/c2/health`、`/api/c2/instances`、`/api/c2/sessions`、`/api/c2/listeners`、`/api/c2/tasks`、`/api/c2/events`，以及会话绑定/解绑。
- UI：`/function/c2` 展示实例健康、会话、监听器、任务摘要、事件和绑定关系。
- Planner 只读工具：`c2_session_list`、`c2_session_context`、`c2_recon`。Worker 只有在任务已有 active binding 时才获得同一裁剪面；未进入 C2 阶段不加载。
- `c2_recon` 在 Phase 3 只读返回白名单动作和已有任务摘要，不创建任务。Confirm/Danger 操作留给 Phase 4。

只读工具面基线：3 个工具，1,205 JSON bytes，327 estimated tokens，含 1.3 安全余量 426 tokens；见 [`contract/phase3_readonly_baseline.json`](contract/phase3_readonly_baseline.json)。

## Phase 0 验证记录

- 集成分支：`codex/ghoul-toshell-integration`
- sidecar 构建与回环健康检查：通过
- 只读 `/api/v1/health`、`/api/v1/mcp/tools`、`/api/v1/sessions` 采集：通过
- 工具等级、schema 形状、快照和 token 基线契约测试：通过
- Ghoul C2 配置 fail-closed 测试：通过
- 全仓既有测试基线：非 `server` 包通过；`server` 包仍依赖本机 PostgreSQL 配置，另有 4 个既有 Windows 路径分隔符断言失败

## AGPL 与来源

Ghoul 采用 AGPL-3.0。本集成通过网络调用独立 Toshell 进程，不把 Toshell 源码纳入 Ghoul 二进制。若后续复制或修改 Toshell 代码，必须保留 MIT notice，并在 Ghoul 的修改说明和发布包中记录变更。参考项目与许可证统一列在主 README 末尾。
