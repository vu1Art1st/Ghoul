"use client";

import * as React from "react";

import { C2ApprovalCenter } from "@/components/c2-approval-center";
import { api } from "@/lib/api";
import type { C2Status } from "@/lib/types";

export default function C2ApprovalsPage() {
  const [status, setStatus] = React.useState<C2Status | null>(null);

  React.useEffect(() => {
    api
      .c2Status()
      .then(setStatus)
      .catch(() => setStatus(null));
  }, []);

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <div>
        <h1 className="text-lg font-semibold">C2 审批中心</h1>
        <p className="text-muted-foreground mt-1 text-sm">
          审批 Confirm/Danger 操作，核对参数哈希、任务范围和会话快照；执行结果会自动物化为 Ghoul 证据。
        </p>
      </div>
      <C2ApprovalCenter baseURL={status?.instance?.base_url} />
    </div>
  );
}
