# Ghoul WebShell 参考项目源码调研

> 调研日期：2026-11-10
> 访问方式：通过 `http://127.0.0.1:12306` 代理访问 GitHub 并浅克隆默认分支
> 用途：为 `WEBSHELL_MANAGEMENT_PLAN.md` 提供事实依据，不作为运行时依赖清单

## 1. 调研范围

本轮实际检查了以下仓库的默认分支、最新提交、许可证、README、目录结构、关键协议/工具实现和测试：

| 项目 | Commit | 语言 | License | 结论 |
|---|---|---|---|---|
| `Marven11/EtherGhost` | `5e6e981d1a5409efbe9013f80e5641f926ec5dab` | Python | MIT | 适合参考 PHP 一句话、Behinder、会话抽象和本地实验环境，不直接嵌入 Ghoul |
| `7-e1even/memshell-party-cli` | `16c7a7fbb804ea2945c3b6efc8d43ab92ecc75be` | TypeScript | MIT | 适合参考 Agent 工具面、协议注册表、结构化结果和 OP log；不能原样暴露其 MCP |
| `Xaaaa-bip/GodzillaSuper` | `b954206c83218a731eea40d63b562fcc922f5c35` | Java | 无 | 只能研究功能分类和操作语义，不能复制或依赖源码 |
| `Ch1ngg/Godzilla-MCP-Headless` | `c2f04d59bc51df42ef925e1d2d5d9d6fc65f7f5a` | Java | 无 | 只能参考无头 MCP 形态，不能复制或作为 sidecar 分发 |
| `zema1/suo5` | `6904c18e276c2dc36250838d68135a5893c4f66b` | Go | MIT | 可作为 Ghoul 的 Suo5 传输层依赖或受控子进程 |
| `AIPentest/CyberStrikeAI` | `b43bdb991c3bee4f433cd1a0a370a90c4e24241e` | Go | Apache-2.0 | AI-native 渗透平台；已有 WebShell 连接管理、AI 会话、终端和文件操作 |
| `jiushill/godzilla-mcp` | `5cbca789805bd8ecdb06ad377c49ee94db7dd9b8` | Python | README 声称 MIT，仓库无 LICENSE 文件 | 不作为依赖；协议实现明显简化且缺少测试 |
| `qtc-de/webshell-cli` | `5bd570f8265825117c4b6923b1e8e8f4a5d5aa0b` | Python | GPL-3.0 | 只参考 CLI 交互，不复制实现 |
| `AntSwordProject/antSword` | `4ab11f821d7e2545dfaa003f1c764087eb4086a1` | JavaScript | MIT | 蚁剑生态参考；GUI/Electron 架构不适合 Agent 直连 |
| `MemShellParty/MemShellParty` | `461dfa998e0165d83b5db4a6c8594fc675af3c78` | Java | MIT | 可作为可选的内存马生成后端，优先自托管 |
| `0x7556/wolfshell` | `f7731ff4fb4b5997a44ba67e3c1692cf073f0a16` | ASP.NET | MIT | 参考 AI/MCP 产品形态；体积大且偏 .NET/ASPX，不作为首期依赖 |

说明：

- “无许可证”不等于可以自由使用。没有明确授权时，不应复制源码、二进制、payload 或结构性实现。
- `jiushill/godzilla-mcp` 的 `pyproject.toml` 写了 MIT，但仓库没有 `LICENSE` 文件。商用或分发前必须由作者补充授权。
- `AntSwordProject/antSword` 的 GitHub API 识别为 MIT，但 README 另有限制性表述，实施前仍需复核其 LICENSE 和插件生态授权。

## 2. EtherGhost

### 2.1 实际结构

- 233 个受版本控制文件，约 38.6 MB。
- Python 3.8+，FastAPI、SQLite/SQLAlchemy、httpx、pycryptodome。
- B/S 架构：Python 后端 + Vue 前端，默认只提供 HTTP API 和 Web UI。
- 会话元数据存 SQLite；进程内会话缓存 300 秒。
- 支持 PHP 一句话、PHP Raw、Behinder PHP AES/XOR、Behinder JSP、Linux 命令一句话、反向 Shell、SSH 等。

### 2.2 可复用设计

- `SessionInfo` + `SessionInterface` + `@register_session` 的分层。
- 统一动作包括命令、目录、文件读写、上传下载、TCP 发送、反向 Shell、HTTP 请求和进程管理。
- 对 PHP 请求/响应定界、编码器/解码器、文件分块和错误类型有较细的实现。
- 自带 PHP/JSP/Behinder 测试环境和单元/E2E 测试，可用于协议 fixture 参考。

### 2.3 不能直接采用的部分

- API 没有 Ghoul 所需的任务绑定、RoE、审批、证据和 Finding 关联。
- CORS 使用 `allow_origins=["*"]`。
- Basic Auth 是可选项；未启用时 API 直接开放。
- 凭据保存在会话配置 JSON/SQLite 中，没有 Ghoul 所需的 secret reference/vault 边界。
- AntSword encoder/decoder 依赖 Node.js 动态执行 JS；自定义 encoder/decoder 通过 `importlib` 加载 Python 模块。这会给 Agent 平台引入本地代码执行面。
- `php_eval`、`execute_cmd`、`send_http_request`、`open_reverse_shell` 等是通用高危原语，不能作为 MCP 工具直接暴露。
- JSP Behinder 路径依赖本地 `javac` 临时编译 class，运行环境复杂。

### 2.4 对 Ghoul 的取舍

- 借鉴会话接口、PHP 定界/编码和测试样例。
- 不嵌入 EtherGhost，不把其 FastAPI 作为 Ghoul 后端。
- 对 AntSword encoder 采用声明式白名单或签名插件，不在请求路径中执行任意 JS/Python。

## 3. memshell-party-cli

### 3.1 实际结构

- 97 个受版本控制文件，约 1.0 MB。
- TypeScript 0.8.0，Node.js >= 18，MIT。
- 同时提供 CLI 和 MCP stdio 服务。
- 协议注册表统一为 `ShellProtocol`：`test`、`exec`、`upload`、`download`。
- 内置协议：Godzilla、Behinder、Suo5 连接测试、mimic。
- 生成能力通过自托管或官方 MemShellParty API 完成。

### 3.2 值得借鉴

- `src/connect/registry.ts` 用注册表收敛协议能力，命令不按协议名硬编码。
- `ConnectTestResult`、`ExecResult`、`TransferResult` 结构紧凑，适合 Agent 解析。
- `src/core/oplog.ts` 的 operation log：
  - 不记录 pass/key/headerValue；
  - 不记录文件内容；
  - exec 输出截断到 2000 字符；
  - 支持按 category/target 查询。
- `src/connect/http.ts`：
  - 不自动跟随重定向；
  - 明确不使用系统代理，只使用专用 `MEMPARTY_PROXY`；
  - 每次请求 `agent:false`，避免复用被服务端关闭的连接。
- 文件下载通过本地落盘，而不是把整个文件内容放进 MCP 文本响应。
- Godzilla/Behinder 协议实现包含分块传输、完整性校验、字符集和错误分类。

### 3.3 关键问题

- MCP 直接接收 `pass`、`key`、`headerValue` 等凭据。
- `target_list` 返回完整的已保存 target 对象，其中包含明文密码、key 和 gate header。
- `~/.memparty/targets.json` 使用 `0600`，但仍是明文存储。
- `download_file`/`upload_file` 接受本地任意路径，没有任务 workspace 约束。
- `exec_command`、`generate_memshell`、`upload_file` 等没有审批级别、任务绑定或 RoE 校验。
- MCP 工具数量为 21 个，包含生成、执行、传输、配置查询、target 管理和日志查询。
- 默认可使用公开生成服务，真实项目中存在供应链和目标信息泄露风险。

### 3.4 对 Ghoul 的取舍

- 借鉴协议注册表、结构化结果、OP log 和重定向/代理安全策略。
- 不直接把其 MCP 注册到 Ghoul Agent。
- 如使用其生成能力，只允许自托管 API，并通过 Ghoul 原生 `webshell_payload` 适配器调用。
- Ghoul 自己保存 target/profile、自己执行审批和证据物化。

## 4. GodzillaSuper

### 4.1 实际结构

- 373 个受版本控制文件，约 43.9 MB。
- Java GUI 应用，源码默认 GBK 编码。
- 无 LICENSE 文件，GitHub API license 为 `null`。
- 覆盖 JSP、ASPX、PHP、ASP、ASP.NET Core、数据库、内存马、C2 Profile、插件、MCP 等大量功能。

### 4.2 可参考的操作目录

- Shell CRUD、连接测试、编码探测。
- 文件列表、读、写、上传、下载、复制、移动、删除、属性。
- 数据库连接、库表枚举、查询。
- Payload 列表、C2 Profile、Shell 生成。
- 内存 Servlet/Filter 注入、列举、卸载。
- 操作日志和团队 PostgreSQL 模式。

### 4.3 不可直接接入的原因

- 无许可证，不能复制源码、二进制、payload 或插件。
- MCP 默认 `0.0.0.0:9123`。
- Bearer Token 是可选保护；本机模式可无 token。
- MCP 暴露约 70+ 工具。
- `shell_info` 直接返回 AES key 和密码；`shell_get` 返回完整配置。
- `config_read`/`config_write`、`settings_set`、`shell_create`、`file_upload_local`、`file_download_local` 等属于宿主控制面能力。
- `filter_shell_add`、`memory_shell_inject`、`mimic`/C2 相关功能风险极高。
- 大量功能直接在 MCP 调用中执行，没有独立的策略/审批账本。

### 4.4 对 Ghoul 的取舍

- 只使用其工具分类和 UI 工作流作为需求清单。
- 不复制实现，不打包 jar，不把它的 MCP 接入 Ghoul。
- 明确禁止任何把密码、AES key、Cookie、审批 token 返回 Agent 的输出字段。

## 5. Godzilla-MCP-Headless

### 5.1 实际结构

- 13 个受版本控制文件，约 193 KB。
- 单个约 2700 行的 Java 文件 + Python 辅助脚本。
- Java 8，依赖 `io.github.beichendream:godzilla:4.01`，运行时需要额外 `godzilla.jar` 和数据目录。
- 无 LICENSE 文件。
- 支持 stdio 与 HTTP，HTTP 默认 `0.0.0.0:5566`。
- README 列出 56 个工具，覆盖会话、生成、命令、文件、数据库、内存马、PHP 专项、Windows/.NET 提权和凭据抓取。

### 5.2 值得借鉴

- 不启动 GUI 也能使用 stdio/HTTP 的 MCP 形态。
- 通过运行时初始化 payload/cryption 注册表实现无头启动。
- 保留 data.db 兼容性，便于有图形界面的存量用户迁移。
- `real_cmd` 的 start/write/read/stop/list 会话模型适合终端交互。

### 5.3 关键问题

- 无许可证。
- HTTP 模式没有鉴权；README 明确说明只应绑定 `127.0.0.1`，但代码默认是 `0.0.0.0`。
- 56 个工具直接暴露执行、文件、数据库、内存马、提权和凭据能力。
- `download_file`、`upload_file`、`big_file_upload` 接受宿主机本地路径。
- 没有任务绑定、危险等级、双阶段审批、幂等或证据链。
- 对 `godzilla.jar` 的运行时依赖不能固定来源，供应链不可接受。

### 5.4 对 Ghoul 的取舍

- 只参考无头 MCP 的启动方式和终端会话协议。
- 不复制、不打包、不部署。

## 6. Suo5

### 6.1 实际结构

- Go 项目，MIT，CLI + GUI。
- 传输层包 `github.com/zema1/suo5/suo5` 可直接导入。
- 提供 `Suo5Config`、`Connect(ctx, config)`、`Suo5Client` 和 SOCKS5/Forward handler。
- 支持 `auto`、`full`、`half`、`classic` 四种模式。
- 支持 SOCKS5、本地认证、TCP forward、上游代理、CookieJar、浏览器头、心跳、排除域名和重试。

### 6.2 适合 Ghoul 的部分

- 作为 `webshell_transport` 的底层实现，不重新实现 Suo5 协议。
- 使用 Ghoul 的 context 控制生命周期。
- 用 Ghoul 的 `local_port`、`target_scope`、`max_connections` 和 `expires_at` 配置约束 Suo5。
- `exclude_domain` 可用于避免代理回环和范围外域名。

### 6.3 集成注意

- 当前 `zema1/suo5` 的 `go.mod` 要求 Go 1.26.5，Ghoul 是 Go 1.26.3；若直接作为模块依赖需要评估升级，否则可先以受控子进程运行其 CLI。
- 其默认配置禁用证书校验的代码路径需要被 Ghoul 的外层 TLS 策略覆盖。
- 本地 SOCKS5 默认无认证，Ghoul 必须在 127.0.0.1 上随机端口监听，并优先启用用户名密码或等价访问控制。
- 必须限制转发目标，避免成为任意内网跳板。

## 7. 其他项目

### 7.1 jiushill/godzilla-mcp

- Python、29 个 MCP 工具、单次 commit。
- README 声称实现 Godzilla v4.0.1，但协议层大量使用简单的 `execCommand:<cmd>`、`file_operation:<path>` 字符串；缺少 Godzilla payload class 上传和响应包装细节。
- 无 LICENSE 文件、无测试。
- `shells.json` 明文保存凭据，`shell_list` 返回明文密码/key。
- 不作为协议基线，只用来证明“Agent 需要结构化工具面”。

### 7.2 qtc-de/webshell-cli

- Python、GPL-3.0、交互式 CLI。
- 只支持本项目自带的 PHP/JSP/ASPX 特制 Shell，不是通用 Godzilla/Behinder 客户端。
- 命令历史默认写入 `~/.webshell_cli_history`，没有审批。
- 可参考 `cd`、`!download`、`!upload`、`!env` 的交互习惯，不复制 GPL 实现。

### 7.3 AntSword

- 蚁剑主仓是 Electron GUI，MIT。
- 其 encoder/decoder 和插件生态丰富，但实现依赖 Node/JS 运行时。
- 适合参考编码器分类和流量格式，不适合作为 Ghoul 的嵌入式 Agent 运行时。

### 7.4 MemShellParty

- Java、MIT、约 40 MB，覆盖 Java Web 中间件内存马生成。
- 更适合作为可选的自托管生成服务，而不是把其 UI/后端直接复制进 Ghoul。
- 生成结果应由 Ghoul 记录为 artifact/evidence，并在注入前走 Danger 审批。

### 7.5 WolfShell

- ASP.NET/.NET 高端 WebShell/C2 管理工具，MIT，仓库约 642 MB。
- 内置 `WolfShellMCP`，支持自然语言驱动。
- 功能覆盖 .NET 内存马、端口复用、PowerShell、C2 等，远超首期范围。
- 其 AI/MCP 产品形态可参考，但不适合作为 Ghoul 的首期通用 WebShell 驱动。

## 8. 横向结论

### 8.1 共性能力

参考项目共同覆盖以下能力：

1. Shell 配置和连接测试。
2. 命令执行、文件管理、数据库查询。
3. 分块上传/下载和完整性校验。
4. 字符集处理。
5. 代理、Cookie 和 TLS。
6. 内存马和隧道。
7. Agent/MCP 工具面。

### 8.2 共性缺陷

所有项目都在不同程度上存在以下缺口：

1. 凭据和工具参数直接进入 Agent/MCP 上下文。
2. 目标配置、密码、key、Cookie 或完整 shell 信息通过列表/详情接口返回。
3. 没有任务绑定和最小权限会话可见性。
4. Confirm/Danger 动作没有 Ghoul 级别的审批和幂等。
5. 本地文件路径可由 Agent 任意指定。
6. 高危工具数量过多，schema 体积大且误触概率高。
7. 外部生成服务、Java jar、Node 包和插件缺少供应链固定。
8. 原始输出直接返回 Agent，没有统一 sanitization/evidence boundary。
9. 没有清理和生命周期记录，尤其是内存马和隧道。

### 8.3 对 Ghoul 的直接指导

1. **原生会话与审批内核**：由 Ghoul 负责，不交给第三方 MCP。
2. **协议驱动可替换**：Godzilla/Behinder/一句话/AntSword 用 Go 驱动；Suo5 优先复用 MIT Go 库。
3. **生成服务可选**：MemShellParty 自托管后端仅作为生成器，不直接面向 Agent。
4. **凭据永不出服务端**：Agent 只引用 session/profile，不传密码和 key。
5. **不暴露第三方原始 MCP**：只允许显式的只读工具白名单；默认关闭。
6. **限制 artifact 路径**：Agent 只使用 Ghoul 管理的 artifact/evidence 引用。
7. **少量工具 + action-level policy**：在工具内部按 action 计算风险，禁止模型声明风险等级。
8. **以 fixture 驱动兼容性**：每个驱动必须有脱敏请求/响应和错误分类测试。

## 9. 推荐复用矩阵

| 能力 | 推荐实现方式 | 原因 |
|---|---|---|
| 会话/绑定/审批/证据 | Ghoul 原生 | 已有 C2 模式和数据库基础 |
| One-liner PHP/命令 | Ghoul 原生 Go Driver | 逻辑简单，需严格参数化 |
| Behinder PHP/JSP | Ghoul 原生 Go Driver | 需要凭据和请求状态控制；参考 MIT 实现 |
| Godzilla Java/PHP/C# | Ghoul 原生 Go Driver | 不能在未授权代码和第三方 MCP 上承重 |
| AntSword | 后续原生 Driver 或签名 encoder 插件 | JS 动态执行风险高 |
| Suo5 tunnel | 复用 `zema1/suo5` Go 模块 | MIT、成熟、具备 SOCKS5/forward |
| 内存马生成 | 可选自托管 MemShellParty | MIT、Java 生态匹配，避免引入 GUI/CLI 直连风险 |
| 外部 MCP | 仅受控只读 adapter | 不允许绕过 Ghoul 审批和 secret 边界 |
| UI | Ghoul 原生页面 | 统一会话、审批、证据和 Finding |

## 10. 实施前复核清单

1. 对准备复制代码的仓库再次核对 `LICENSE`、NOTICE 和文件头。
2. 固定 release tag、commit、依赖版本和校验和。
3. 对 Godzilla/Behinder payload class/asset 做独立授权审查。
4. 对 Suo5 模块做 Go 版本兼容和 license/NOTICE 审查。
5. 对 MemShellParty 自托管后端做镜像 digest 固定。
6. 为每个驱动建立脱敏 fixture 和本地受控环境。
7. 禁止在测试 fixture 中保存真实目标、Cookie、密码或 key。
8. 将参考项目说明同步到 README 的“参考项目与开源说明”。

## 11. CyberStrikeAI 对照

### 11.1 实际 WebShell 能力

CyberStrikeAI 当前实现的是“已有 WebShell 连接管理”，不是“生成并投递 WebShell”：

- 数据表 `webshell_connections` 保存 `url`、`password`、`type`、`method`、`cmd_param`、`remark`、`encoding`、`os`、`project_id`，另有 `webshell_connection_states` 保存前端工作区状态。
- HTTP API 提供连接 CRUD、连接状态、AI 历史、AI 会话列表、`POST /api/webshell/exec` 和 `POST /api/webshell/file`。
- UI 提供连接列表、xterm 虚拟终端、文件管理、数据库面板、AI 助手和项目绑定。
- AI 页面通过 `webshellConnectionId` 将当前连接注入 Agent 上下文。
- Agent 工具面共有 9 个：
  - `webshell_exec`
  - `webshell_file_list`
  - `webshell_file_read`
  - `webshell_file_write`
  - `manage_webshell_list`
  - `manage_webshell_add`
  - `manage_webshell_update`
  - `manage_webshell_delete`
  - `manage_webshell_test`
- `manage_webshell_add` 接收 URL、password、type、method、cmd_param、remark，然后写入数据库；没有 payload 模板生成、artifact、部署、验证和 handoff。
- `manage_webshell_test` 默认执行 `whoami` 或 `dir`。

### 11.2 协议实现范围

- 执行请求构造为 `pass=<password>&<cmd_param>=<command>`，支持 GET/POST。
- 文件操作不是独立协议，而是把 `list/read/write/delete/mkdir/rename/upload` 翻译成 Linux/Windows shell 命令。
- 因此它主要兼容普通一句话 WebShell，不支持：
  - 冰蝎 AES/XOR 协议；
  - 哥斯拉 payload class、序列化参数和响应包装；
  - 蚁剑 encoder/decoder 链；
  - Suo5 隧道；
  - 内存马生命周期。
- `type` 字段只用于文件命令选择和默认操作系统判断，不是真正的协议 Driver。

### 11.3 安全与治理

值得借鉴：

- `webshell:read`、`webshell:write`、`webshell:delete` 权限。
- WebShell 资源级 RBAC、owner 和 project binding。
- 会话上下文绑定项目后，Agent 不能操作未绑定项目的 WebShell。
- API 返回连接列表时将 password 替换为掩码。
- OS auto 探测并持久化，支持 GBK/GB18030 解码。
- AI 上下文明确告知连接 ID、目标 OS、编码、当前目录和推荐命令集。
- 通用 HITL/agent finalization 机制可以要求工具执行证据。

仍然存在的问题：

- password 在 SQLite 中明文保存。
- Agent 可以通过 `manage_webshell_add` 直接提交连接密码。
- `webshell_exec`、`webshell_file_read` 的结果直接作为 MCP 文本返回，没有统一证据封存和敏感信息边界。
- 执行和文件操作没有 WebShell 专用的 operation ledger、参数哈希、双阶段审批或禁止自动重试。
- `POST /api/webshell/exec` 允许带 URL 和表单凭据的 ad hoc 执行，只要调用方有 `webshell:write`，不要求存在已登记连接。
- TLS 客户端固定 `InsecureSkipVerify: true`。
- 连接创建和删除有审计，但执行、文件读取、写入等高风险动作没有对应 WebShell 专用审计记录。
- 没有原版加载器 handoff 数据模型。

### 11.4 对 Ghoul 的结论

CyberStrikeAI 证明了 AI-native 平台中 WebShell 管理是有产品价值的，但它只覆盖了“已有简单 Shell 的连接管理和 AI 操作”：

| 能力 | CyberStrikeAI | Ghoul 目标 |
|---|---|---|
| 已有连接登记 | 有 | 有，且区分 record-only/managed |
| Agent 连接级上下文 | 有 | 有 |
| 命令执行 | 有 | 有，Danger |
| 文件 list/read/write | 有 | 有，Confirm/Danger 分级 |
| 数据库 UI | 有 | 有，后续 Agent DB tool |
| AI 会话历史 | 有 | 有 |
| Agent 生成 payload | 无 | 有 |
| payload artifact/deployment | 无 | 有 |
| 冰蝎/哥斯拉/蚁剑协议 | 无 | 规划支持 |
| Suo5 隧道 | 无 | 复用 MIT Go 模块 |
| 内存马生命周期 | 无 | 规划支持 |
| 原版加载器 handoff | 无 | 有 |
| 操作证据/Finding | 通用证据机制 | WebShell 专用 operation/evidence |
| Secret vault | 无，明文 SQLite | 有 |

因此 Ghoul 不应只做 CyberStrikeAI 同款“记录已有连接”。那只能获得约 65-72/100 的自动渗透收益；把 Agent 创建、加密协议、Suo5、证据和 handoff 做进去，才有机会达到 88-92/100。
