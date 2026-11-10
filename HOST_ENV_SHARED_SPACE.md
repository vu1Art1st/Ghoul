# 宿主环境与共享空间

## 能力边界

宿主环境展示 Ghoul Agent 实际执行命令的环境，统一工具体检、白名单安装、安装代理和任务日志。Docker 部署下这里指 Agent 容器，不是 Docker 物理宿主机。

共享空间用于跨任务复用审核发布的脚本、工具、语言库及 Python Package。共享资产不随任务删除或任务归档；工作空间中的 `shared/` 是只读版本存储，发布和停用只能通过专用 API 完成。

## 发布与复用

1. 在任务工作目录验证脚本并清除目标地址、Cookie、Token 等敏感信息。
2. 在工作空间点击“发布到共享空间”，填写用途、入口、依赖和调用示例。
3. 系统拒绝符号链接、限制 512 MiB、计算内容哈希，并以不可变版本保存。
4. Agent 使用 `shared_search`、`shared_get`、`shared_materialize` 检索并复制指定版本到任务目录。
5. 复制件可按任务修改，不修改共享版本；任务不会自动切换到后续版本。

发布记录保留来源任务和意图 ID，但不设置任务外键，源任务删除后仍可复用。

## Python 运行时

首次安装 Package 时创建 `data/shared/python/venv`，所有安装和卸载串行执行，成功后保存 `requirements-freeze.txt`。自定义 Python 工具在共享解释器存在时自动使用它；Bash/PTY 可通过 `GHOUL_PYTHON` 明确调用，`GHOUL_PYTHONPATH` 指向共享语言库目录。

Python 语言库发布时以最新已发布版本维护导入目录：Linux 使用原子符号链接，Windows 使用同步副本。脚本和工具始终按任务选择的版本复制。

安装只允许明确的 PyPI 包名和版本约束，不接收 URL、本地路径、依赖文件或额外命令行参数。不要直接对系统 Python 执行 `pip install`。

## 安装权限

- Python Package：无需系统特权，安装到共享 venv。
- apt/dnf 与 `/usr/local/bin` 工具：Docker 中使用容器内安装器；Linux 二进制可用可选 `ghoul-hostd`。
- Linux 二进制发行包含 `ghoul-hostd`。参考 `deploy/ghoul-hostd.service` 替换数据库 DSN 后安装单元，并为主服务配置 `GHOUL_HOSTD_SOCKET=/run/ghoul/hostd.sock`。助手仅监听 Unix Socket、仅接受目录内工具 ID，不接受自由命令。
- Windows/macOS：提供体检和手工安装指引，不提供系统工具一键安装。
- 安装代理独立于目标流量代理，支持 http/https/socks5/socks5h 配置保存和连通性测试。

安装任务使用服务进程生命周期而非 HTTP 请求生命周期；支持排队、取消、日志、退出码和重启后中断标记。
