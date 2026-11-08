"use client";

import * as React from "react";
import {
  AlertTriangle,
  CheckCircle2,
  ExternalLink,
  Loader2,
  RefreshCw,
  ShieldCheck,
  XCircle,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { C2Evidence, C2Operation, C2OperationDetail, C2Security } from "@/lib/types";

function fmtTime(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function statusVariant(status: string): "default" | "secondary" | "destructive" | "outline" {
  if (status === "completed" || status === "approved") return "default";
  if (status === "pending_approval" || status === "running" || status === "dispatched") return "secondary";
  if (status === "failed" || status === "denied" || status === "expired") return "destructive";
  return "outline";
}

export function C2ApprovalCenter({ baseURL }: { baseURL?: string }) {
  const [items, setItems] = React.useState<C2Operation[]>([]);
  const [selectedID, setSelectedID] = React.useState("");
  const [detail, setDetail] = React.useState<C2OperationDetail | null>(null);
  const [security, setSecurity] = React.useState<C2Security | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState("");
  const [reason, setReason] = React.useState("人工复核通过");
  const [findingID, setFindingID] = React.useState("");
  const [findingReason, setFindingReason] = React.useState("C2 证据关联 Finding");

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const [result, securityResult] = await Promise.all([api.c2Operations({ limit: 200 }), api.c2Security()]);
      setItems(result.items);
      setSecurity(securityResult);
      if (!selectedID || !result.items.some((item) => item.id === selectedID)) {
        setSelectedID(result.items[0]?.id ?? "");
      }
    } catch (error) {
      toast.error(`加载 C2 操作失败: ${error}`);
    } finally {
      setLoading(false);
    }
  }, [selectedID]);

  React.useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  React.useEffect(() => {
    if (!selectedID) {
      setDetail(null);
      return;
    }
    api
      .c2Operation(selectedID)
      .then(setDetail)
      .catch((error) => toast.error(`加载操作详情失败: ${error}`));
  }, [selectedID]);

  async function approve(stage: number) {
    if (!detail) return;
    setBusy(`approve-${stage}`);
    try {
      const challenge = await api.challengeC2Operation(detail.operation.id, stage);
      const result = await api.approveC2Operation(detail.operation.id, {
        stage,
        token: challenge.token,
        reason: reason.trim() || "人工复核通过",
        execute: stage === detail.operation.approval_stages,
      });
      toast.success(result.all_approved ? "审批完成并已提交执行" : `第 ${stage} 阶段审批完成`);
      await load();
      setDetail(await api.c2Operation(detail.operation.id));
    } catch (error) {
      toast.error(`审批失败: ${error}`);
    } finally {
      setBusy("");
    }
  }

  async function reject() {
    if (!detail) return;
    setBusy("reject");
    try {
      await api.rejectC2Operation(detail.operation.id, { stage: 1, reason: reason.trim() || "人工拒绝" });
      toast.success("操作已拒绝");
      await load();
      setDetail(await api.c2Operation(detail.operation.id));
    } catch (error) {
      toast.error(`拒绝失败: ${error}`);
    } finally {
      setBusy("");
    }
  }

  async function execute() {
    if (!detail) return;
    setBusy("execute");
    try {
      await api.executeC2Operation(detail.operation.id);
      toast.success("操作已提交执行");
      setDetail(await api.c2Operation(detail.operation.id));
      await load();
    } catch (error) {
      toast.error(`执行失败: ${error}`);
    } finally {
      setBusy("");
    }
  }

  async function bindEvidence(evidence: C2Evidence) {
    const id = Number(findingID);
    if (!id) {
      toast.error("请输入有效的 Finding ID");
      return;
    }
    setBusy(`evidence-${evidence.id}`);
    try {
      await api.bindC2EvidenceFinding(evidence.id, {
        finding_id: id,
        reason: findingReason.trim() || "C2 证据关联 Finding",
      });
      toast.success("证据已绑定到 Finding");
      if (detail) setDetail(await api.c2Operation(detail.operation.id));
    } catch (error) {
      toast.error(`绑定证据失败: ${error}`);
    } finally {
      setBusy("");
    }
  }

  const pending = items.filter((item) => item.status === "pending_approval");
  const operation = detail?.operation;

  return (
    <div className="grid gap-4 xl:grid-cols-[minmax(300px,0.7fr)_minmax(0,1.3fr)]">
      <Card className="min-w-0">
        <CardHeader className="pb-3">
          <div className="flex items-center justify-between gap-2">
            <CardTitle className="flex items-center gap-2 text-sm">
              <ShieldCheck className="size-4" />
              C2 操作审批
              <Badge variant="secondary">{pending.length} 待审批</Badge>
            </CardTitle>
            {security ? (
              <Badge variant={security.ok ? "default" : "destructive"}>
                {security.ok ? "安全自检通过" : "安全自检告警"}
              </Badge>
            ) : null}
            <Button variant="ghost" size="icon" onClick={() => void load()} disabled={loading}>
              <RefreshCw className={loading ? "animate-spin" : ""} />
            </Button>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          <div className="max-h-[680px] overflow-auto">
            {items.length === 0 && !loading ? (
              <div className="text-muted-foreground px-4 py-12 text-center text-sm">暂无 C2 操作</div>
            ) : null}
            {items.map((item) => (
              <button
                key={item.id}
                type="button"
                onClick={() => setSelectedID(item.id)}
                className={`w-full border-t px-4 py-3 text-left transition-colors hover:bg-muted/50 ${
                  selectedID === item.id ? "bg-muted/70" : ""
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate text-sm font-medium">{item.tool}</span>
                  <Badge variant={statusVariant(item.status)}>{item.status}</Badge>
                </div>
                <div className="text-muted-foreground mt-1 flex items-center justify-between gap-2 text-xs">
                  <span>#{item.id} · task {item.task_id}</span>
                  <span>{item.level}</span>
                </div>
                <div className="text-muted-foreground mt-1 truncate text-xs">
                  {item.action || String(item.params?.path || item.params?.command || "") || "—"}
                </div>
              </button>
            ))}
          </div>
        </CardContent>
      </Card>

      <Card className="min-w-0">
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <AlertTriangle className="size-4" />
            操作详情
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {!operation ? (
            <div className="text-muted-foreground py-16 text-center text-sm">选择一条操作查看审批与证据</div>
          ) : (
            <>
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-sm">#{operation.id}</span>
                    <Badge variant={statusVariant(operation.status)}>{operation.status}</Badge>
                    <Badge variant="outline">{operation.level}</Badge>
                    <Badge variant="outline">{operation.actor}/{operation.source}</Badge>
                  </div>
                  <div className="mt-2 text-lg font-semibold">{operation.tool}</div>
                  <div className="text-muted-foreground text-xs">
                    task {operation.task_id} · session {operation.c2_session_id} · {fmtTime(operation.requested_at)}
                  </div>
                </div>
                {baseURL ? (
                  <Button variant="outline" size="sm" asChild>
                    <a href={baseURL} target="_blank" rel="noreferrer">
                      <ExternalLink />
                      Toshell 管理台
                    </a>
                  </Button>
                ) : null}
              </div>

              <div className="grid gap-3 md:grid-cols-2">
                <Info label="参数哈希" value={operation.params_hash} mono />
                <Info label="Toshell task" value={operation.external_task_id ? String(operation.external_task_id) : "—"} />
                <Info label="审批阶段" value={String(operation.approval_stages)} />
                <Info label="完成时间" value={fmtTime(operation.completed_at)} />
              </div>

              <div className="rounded-md border p-3">
                <div className="mb-2 text-xs font-medium text-muted-foreground">参数</div>
                <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">
                  {JSON.stringify(operation.params, null, 2)}
                </pre>
              </div>

              {operation.error ? (
                <div className="rounded-md border border-red-500/30 bg-red-500/5 p-3 text-sm text-red-600">
                  {operation.error}
                </div>
              ) : null}

              {operation.approval_required && operation.status === "pending_approval" ? (
                <div className="space-y-3 rounded-md border p-3">
                  <div className="text-sm font-medium">人工审批</div>
                  <Textarea value={reason} onChange={(event) => setReason(event.target.value)} rows={2} />
                  <div className="flex flex-wrap gap-2">
                    {Array.from({ length: operation.approval_stages }, (_, index) => index + 1).map((stage) => (
                      <Button key={stage} size="sm" onClick={() => void approve(stage)} disabled={Boolean(busy)}>
                        {busy === `approve-${stage}` ? <Loader2 className="animate-spin" /> : <CheckCircle2 />}
                        通过第 {stage} 阶段
                      </Button>
                    ))}
                    <Button variant="outline" size="sm" onClick={() => void reject()} disabled={Boolean(busy)}>
                      {busy === "reject" ? <Loader2 className="animate-spin" /> : <XCircle />}
                      拒绝
                    </Button>
                  </div>
                  {operation.approval_stages > 1 ? (
                    <div className="text-muted-foreground text-xs">
                      Danger 操作需要完成两个独立阶段；最后一阶段通过后才会提交 Toshell。
                    </div>
                  ) : null}
                </div>
              ) : null}

              {operation.status === "approved" ? (
                <Button onClick={() => void execute()} disabled={Boolean(busy)}>
                  {busy === "execute" ? <Loader2 className="animate-spin" /> : <CheckCircle2 />}
                  提交执行
                </Button>
              ) : null}

              <div>
                <div className="mb-2 text-xs font-medium text-muted-foreground">范围快照</div>
                <div className="flex flex-wrap gap-2">
                  {(detail?.scope ?? []).map((scope, index) => (
                    <Badge key={`${scope.kind}-${index}`} variant="outline">
                      {scope.kind}: {scope.domain || scope.net || scope.value || "—"}
                    </Badge>
                  ))}
                  {(detail?.scope ?? []).length === 0 ? <span className="text-muted-foreground text-xs">无范围</span> : null}
                </div>
              </div>

              <div>
                <div className="mb-2 flex items-center justify-between gap-2">
                  <div className="text-xs font-medium text-muted-foreground">审批记录</div>
                  <div className="text-muted-foreground text-xs">{detail?.approvals.length ?? 0} 阶段</div>
                </div>
                <div className="space-y-2">
                  {detail?.approvals.map((item) => (
                    <div key={item.id} className="flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-xs">
                      <span>阶段 {item.stage} · {item.approved_by || "待审批"}</span>
                      <Badge variant={item.status === "approved" ? "default" : item.status === "pending" ? "secondary" : "destructive"}>
                        {item.status}
                      </Badge>
                    </div>
                  ))}
                </div>
              </div>

              <div>
                <div className="mb-2 text-xs font-medium text-muted-foreground">证据</div>
                <div className="space-y-2">
                  {detail?.evidence.map((item) => (
                    <div key={item.id} className="rounded-md border p-3">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="text-sm font-medium">{item.title || item.kind}</div>
                        <Badge variant={item.redacted ? "secondary" : "outline"}>
                          {item.redacted ? "已脱敏" : item.kind}
                        </Badge>
                      </div>
                      <div className="text-muted-foreground mt-1 text-xs">
                        {item.sha256.slice(0, 16)}… · {item.size} bytes · {fmtTime(item.created_at)}
                      </div>
                      {item.content ? (
                        <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-all text-xs">
                          {item.content}
                        </pre>
                      ) : null}
                      {item.finding_id ? (
                        <Badge className="mt-2" variant="outline">已绑定 Finding #{item.finding_id}</Badge>
                      ) : (
                        <div className="mt-3 flex flex-wrap items-end gap-2">
                          <div className="w-32 space-y-1">
                            <Label htmlFor={`finding-${item.id}`}>Finding ID</Label>
                            <Input
                              id={`finding-${item.id}`}
                              value={findingID}
                              onChange={(event) => setFindingID(event.target.value)}
                            />
                          </div>
                          <div className="min-w-48 flex-1 space-y-1">
                            <Label htmlFor={`finding-reason-${item.id}`}>原因</Label>
                            <Input
                              id={`finding-reason-${item.id}`}
                              value={findingReason}
                              onChange={(event) => setFindingReason(event.target.value)}
                            />
                          </div>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => void bindEvidence(item)}
                            disabled={busy === `evidence-${item.id}`}
                          >
                            {busy === `evidence-${item.id}` ? <Loader2 className="animate-spin" /> : null}
                            绑定
                          </Button>
                        </div>
                      )}
                    </div>
                  ))}
                  {(detail?.evidence.length ?? 0) === 0 ? (
                    <div className="text-muted-foreground text-xs">操作完成后证据会自动物化到这里。</div>
                  ) : null}
                </div>
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function Info({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0 rounded-md border px-3 py-2">
      <div className="text-muted-foreground text-[11px]">{label}</div>
      <div className={`mt-1 truncate text-sm ${mono ? "font-mono" : ""}`}>{value}</div>
    </div>
  );
}
