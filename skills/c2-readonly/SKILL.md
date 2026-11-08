---
name: c2-readonly
description: 读取已绑定的 Toshell C2 会话、监听器和任务摘要。仅用于只读态势判断；不得执行命令、创建任务或调用 Toshell 原始 MCP 工具。
tags:
  class: [c2, toshell, readonly]
---

# C2 只读接入

1. 先用 `c2_session_list` 查看绑定到当前任务的会话。
2. 用 `c2_session_context` 读取会话身份、监听器和近期任务摘要。
3. `c2_recon` 在 Phase 3 只返回白名单动作和现有任务摘要，不执行命令。
4. 会话未绑定时不得尝试调用；绑定由操作员在 Ghoul C2 页面完成。
5. 不要把 Toshell API Key、原始事件流或完整任务输出写入报告；只使用脱敏摘要。
