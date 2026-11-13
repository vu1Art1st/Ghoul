# 宿主环境与共享空间

## 能力边界

宿主环境展示 Ghoul Agent 实际执行命令的环境，统一工具体检、白名单安装、安装代理和任务日志。Docker 部署下这里指 Agent 容器，不是 Docker 物理宿主机。

共享空间用于跨任务复用审核发布的脚本、工具、语言库、Skill、模板及完整 CTF
报告。共享资产不随任务删除或任务归档；工作空间中的 `shared/` 是只读版本存储，
发布和停用只能通过专用 API 完成。

## 发布与复用

1. 在任务工作目录验证脚本并清除目标地址、Cookie、Token 等敏感信息。
2. 在工作空间点击“发布到共享空间”，填写用途、入口、依赖和调用示例。
3. 系统拒绝符号链接、限制 512 MiB、计算内容哈希，并以不可变版本保存。
4. Agent 使用 `shared_search`、`shared_get`、`shared_materialize` 检索并复制指定版本到任务目录。
5. 复制件可按任务修改，不修改共享版本；任务不会自动切换到后续版本。

发布记录保留来源任务和意图 ID，但不设置任务外键，源任务删除后仍可复用。

## 资产资料与内容浏览

- `display_name` 是面向用户的显示名称，可单独修改并支持中文；`name` 是稳定路径标识，
  用于任务目录复制和 Python 语言库链接，发布后不随普通资料编辑变化。
- 描述、标签、语言、调用示例、依赖和运行时说明通过 `PATCH /api/shared/assets/{id}`
  独立更新，不创建新版本，也不要求重新填写来源路径。
- 资产详情页直接读取当前或历史版本的只读文件树，文本文件在线预览，二进制和超过
  2 MiB 的文件只提供下载。
- 来源任务和来源路径从版本记录返回，可跳回任务详情，也可直接打开工作空间中的来源目录。
- 资料编辑、版本发布和启停操作写入 `shared_asset_events`，按资产保留最近 100 条记录。

## Python 运行时

共享 Python 由 `uv` 管理。默认 venv 位于
`data/shared/python/venvs/default`，uv 的解释器、缓存和工具目录也全部位于
`data/shared/python/uv` 下；旧 `data/shared/python/venv` 继续兼容读取。

首次安装 Package 时优先执行：

```text
uv venv --python <uv-managed-or-system-python> data/shared/python/venvs/default
uv pip install --python <venv-python> <package-spec>
uv pip freeze --python <venv-python>
```

所有安装、卸载和修复串行执行，成功后保存
`data/shared/python/locks/requirements-freeze.txt`。自定义 Python 工具在共享解释器
存在时自动使用它；Bash/PTY 通过 `GHOUL_PYTHON` 调用实际存在的解释器，
`GHOUL_PYTHONPATH` 指向共享语言库目录。venv 未初始化或已损坏时不再注入指向空路径的
`GHOUL_PYTHON`，而是明确标记运行时回退来源。

Python 语言库发布时以最新已发布版本维护导入目录：Linux 使用原子符号链接，Windows 使用同步副本。脚本和工具始终按任务选择的版本复制。

安装只允许明确的 PyPI 包名和版本约束，不接收 URL、本地路径、依赖文件或额外命令行
参数。系统 Python 不是首选安装目标；只有同一包规格的共享 uv 安装任务已经失败时，
才允许执行受控回退：

```text
uv pip install --system --python <system-python> <package-spec>
```

启用 PEP 668 且 uv 不可用时才使用
`python3 -m pip install --break-system-packages`。系统安装会记录目标解释器、包规格、
`system-install` 和 `ephemeral`，容器重建后可能丢失。

## 其他语言运行时

Java、Node.js、Go 和 uv 自身的版本安装由 `mise` 管理。`MISE_DATA_DIR`、
`MISE_CONFIG_DIR`、`MISE_CACHE_DIR`、`MISE_STATE_DIR` 和 shim 目录均位于
`data/shared/mise`。Python 解释器与 Python 包仍只由 uv 管理，不交给 mise。

`runtime_status` 返回实际解析到的解释器、来源和降级原因；`runtime_ensure` 只接受固定
action。共享运行时不可用时允许 Agent 调用系统 Python、Java、Node.js 继续执行。

## PoC、赛题与任务关联

PoC 按目标、产品、组件、版本、CVE/CWE 和验证状态沉淀。`poc_records` 是可重建索引，
Finding、探索图、资产图和共享资产版本仍是事实与文件内容的权威来源。可执行文件通过
`shared_asset_id + version` 关联；复用后默认重新验证，不能把历史有效直接当成当前有效。

`scope_type=ctf` 的赛题档案不脱敏，可在 `metadata` 中保存赛事、题目、Flag、POA 和完整
Writeup，并通过共享资产关联附件、源码、流量及利用脚本。

新建任务时，系统从描述、目标和目标地址推荐最多 8 个历史任务，展示命中原因与置信度。
选择后仍写入现有 `source_task_ids`，只读继承任务事实，不递归、不复制凭据、流量或临时
文件，也不自动扩大测试范围。

## 安装权限

- Python Package：优先安装到共享 uv venv；共享安装失败后才允许系统 Python 回退。
- Java/Node.js/Go：通过 mise 安装到共享目录，白名单限定语言工具及版本。
- apt/dnf 与 `/usr/local/bin` 工具：Docker 中使用容器内安装器；Linux 二进制可用可选 `ghoul-hostd`。
- Linux 二进制发行包含 `ghoul-hostd`。参考 `deploy/ghoul-hostd.service` 替换数据库 DSN 后安装单元，并为主服务配置 `GHOUL_HOSTD_SOCKET=/run/ghoul/hostd.sock`。助手仅监听 Unix Socket、仅接受目录内工具 ID，不接受自由命令。
- Windows/macOS：提供体检和手工安装指引，不提供系统工具一键安装。
- 安装代理独立于目标流量代理，支持 http/https/socks5/socks5h 配置保存和连通性测试。

安装任务使用服务进程生命周期而非 HTTP 请求生命周期；支持排队、取消、日志、退出码和重启后中断标记。
