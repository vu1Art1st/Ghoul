"use client";

import * as React from "react";
import Link from "next/link";
import { ArrowLeft, ExternalLink, Loader2, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import type { C2OperationDetail, C2Status } from "@/lib/types";

export function C2OperationDetailView({ id }: { id: string }) {
  const [detail, setDetail] = React.useState<C2OperationDetail | null>(null);
  const [status, setStatus] = React.useState<C2Status | null>(null);
  const [loading, setLoading] = React.useState(true);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const [operation, c2Status] = await Promise.all([api.c2Operation(id), api.c2Status()]);
      setDetail(operation);
      setStatus(c2Status);
    } catch (error) {
      toast.error(`加载 C2 操作详情失败: ${error}`);
    } finally {
      setLoading(false);
    }
  }, [id]);

  React.useEffect(() => {
    void load();
  }, [load]);

  if (loading && !detail) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="animate-spin" />
      </div>
    );
  }

  if (!detail) {
    return <div className="text-muted-foreground p-6 text-sm">操作不存在或 C2 适配器未启用。</div>;
  }

  const op = detail.operation;
  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="icon" asChild>
            <Link href="/system/c2/approvals">
              <ArrowLeft />
            </Link>
          </Button>
          <div>
            <h1 className="text-lg font-semibold">C2 操作 #{op.id}</h1>
            <div className="text-muted-foreground text-xs">
              {op.tool} · task {op.task_id} · {op.actor}/{op.source}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant={op.status === "completed" ? "default" : "secondary"}>{op.status}</Badge>
          <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
            <RefreshCw className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
          {status?.instance?.base_url ? (
            <Button variant="outline" size="sm" asChild>
              <a href={status.instance.base_url} target="_blank" rel="noreferrer">
                <ExternalLink />
                Toshell
              </a>
            </Button>
          ) : null}
        </div>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">操作</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <Row label="等级" value={op.level} />
            <Row label="参数哈希" value={op.params_hash} mono />
            <Row label="Toshell task" value={op.external_task_id ? String(op.external_task_id) : "—"} />
            <Row label="审批阶段" value={String(op.approval_stages)} />
            <Row label="请求时间" value={new Date(op.requested_at).toLocaleString()} />
            <Row label="完成时间" value={op.completed_at ? new Date(op.completed_at).toLocaleString() : "—"} />
            {op.error ? <div className="rounded-md border border-red-500/30 p-2 text-red-600">{op.error}</div> : null}
            <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md border p-3 text-xs">
              {JSON.stringify(op.params, null, 2)}
            </pre>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">审批快照</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {detail.approvals.map((approval) => (
              <div key={approval.id} className="rounded-md border p-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm font-medium">阶段 {approval.stage}</span>
                  <Badge variant={approval.status === "approved" ? "default" : "secondary"}>{approval.status}</Badge>
                </div>
                <div className="text-muted-foreground mt-2 text-xs">
                  参数哈希：{approval.params_hash}
                </div>
                <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-all text-xs">
                  {JSON.stringify({ task: approval.task_snapshot, session: approval.session_snapshot }, null, 2)}
                </pre>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">物化证据</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {detail.evidence.length === 0 ? (
            <div className="text-muted-foreground text-sm">暂无证据。任务执行完成后会自动从 Toshell 结果物化。</div>
          ) : null}
          {detail.evidence.map((item) => (
            <div key={item.id} className="rounded-md border p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="text-sm font-medium">{item.title || item.kind}</div>
                <div className="flex items-center gap-2">
                  <Badge variant="outline">{item.kind}</Badge>
                  {item.redacted ? <Badge variant="secondary">已脱敏</Badge> : null}
                  {item.finding_id ? <Badge>Finding #{item.finding_id}</Badge> : null}
                </div>
              </div>
              <div className="text-muted-foreground mt-1 text-xs">
                {item.sha256} · {item.size} bytes · {new Date(item.created_at).toLocaleString()}
              </div>
              {item.content ? (
                <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all text-xs">{item.content}</pre>
              ) : null}
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className={`truncate text-right ${mono ? "font-mono text-xs" : ""}`}>{value}</span>
    </div>
  );
}
