"use client";

import * as React from "react";
import { ArrowLeft, GitMerge, Loader2, RefreshCw, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { Finding, FindingCase, FindingCaseDetail, Severity } from "@/lib/types";
import Link from "next/link";

function findingLabel(finding: Finding): string {
  return finding.name || finding.vulnclass || `Finding #${finding.finding_id ?? finding.id}`;
}

export default function FindingCasesPage() {
  const [taskId, setTaskId] = React.useState("");
  const [cases, setCases] = React.useState<FindingCase[]>([]);
  const [selected, setSelected] = React.useState<FindingCaseDetail | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [mergeIds, setMergeIds] = React.useState("");
  const [title, setTitle] = React.useState("");
  const [reason, setReason] = React.useState("");
  const [report, setReport] = React.useState("");
  const [severity, setSeverity] = React.useState<Severity>("high");
  const [severityReason, setSeverityReason] = React.useState("");

  async function loadCases() {
    setLoading(true);
    try {
      const result = await api.findingCases(taskId.trim() || undefined, 1, 100);
      setCases(result.items);
    } catch (error) {
      toast.error(`加载漏洞组失败: ${error}`);
    } finally {
      setLoading(false);
    }
  }

  React.useEffect(() => {
    void loadCases();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function openCase(item: FindingCase) {
    try {
      const detail = await api.findingCase(item.id);
      setSelected(detail);
      setReport(item.report ?? "");
      setSeverity((item.severity || "high") as Severity);
      setSeverityReason(item.severity_reason || "");
    } catch (error) {
      toast.error(`加载详情失败: ${error}`);
    }
  }

  async function mergeFindings() {
    const ids = mergeIds
      .split(/[,\s]+/)
      .map((value) => value.trim())
      .filter(Boolean);
    if (!taskId.trim() || ids.length < 2 || !title.trim() || !reason.trim()) {
      toast.error("请填写任务、至少两个 Finding ID、名称和归并理由");
      return;
    }
    setSaving(true);
    try {
      const item = await api.mergeFindingCases(taskId.trim(), ids, title.trim(), reason.trim());
      toast.success(`已归并到漏洞组 #${item.id}`);
      setMergeIds("");
      setTitle("");
      setReason("");
      await loadCases();
      await openCase(item);
    } catch (error) {
      toast.error(`归并失败: ${error}`);
    } finally {
      setSaving(false);
    }
  }

  async function saveReport() {
    if (!selected) return;
    setSaving(true);
    try {
      const updated = await api.updateFindingCase(selected.case.id, {
        version: selected.case.version,
        title: selected.case.title,
        report,
        severity,
        reason: severityReason || selected.case.reason,
      });
      setSelected({ ...selected, case: updated });
      toast.success("统一报告已保存");
    } catch (error) {
      toast.error(`保存失败: ${error}`);
    } finally {
      setSaving(false);
    }
  }

  async function detach(finding: Finding) {
    if (!selected) return;
    const findingId = finding.finding_id || finding.id;
    const note = window.prompt("移出原因", "证据显示属于不同根因");
    if (!note?.trim()) return;
    try {
      await api.detachFindingCaseMember(selected.case.id, findingId, note.trim());
      toast.success("已移出漏洞组");
      await openCase(selected.case);
      await loadCases();
    } catch (error) {
      toast.error(`移出失败: ${error}`);
    }
  }

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Link href="/function/findings" className="text-muted-foreground hover:text-foreground">
            <ArrowLeft className="size-4" />
          </Link>
          <GitMerge className="size-5" />
          <h1 className="text-lg font-semibold">漏洞归并</h1>
        </div>
        <div className="flex items-end gap-2">
          <div className="space-y-1">
            <Label htmlFor="case-task" className="text-xs">任务 ID</Label>
            <Input id="case-task" className="w-36" value={taskId} onChange={(event) => setTaskId(event.target.value)} placeholder="可选" />
          </div>
          <Button variant="outline" onClick={() => void loadCases()} disabled={loading}>
            {loading ? <Loader2 className="animate-spin" /> : <RefreshCw />}
            刷新
          </Button>
        </div>
      </div>

      <div className="grid gap-4 xl:grid-cols-[340px_minmax(0,1fr)]">
        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">新建归并</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="space-y-1.5">
                <Label htmlFor="merge-ids">Finding ID</Label>
                <Input id="merge-ids" value={mergeIds} onChange={(event) => setMergeIds(event.target.value)} placeholder="12, 18, 21" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="merge-title">统一名称</Label>
                <Input id="merge-title" value={title} onChange={(event) => setTitle(event.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="merge-reason">归并理由</Label>
                <Textarea id="merge-reason" rows={3} value={reason} onChange={(event) => setReason(event.target.value)} placeholder="说明这些记录为何是同一根因" />
              </div>
              <Button className="w-full" onClick={() => void mergeFindings()} disabled={saving}>
                {saving ? <Loader2 className="animate-spin" /> : <GitMerge />}
                归并
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">漏洞组</CardTitle>
            </CardHeader>
            <CardContent className="max-h-[520px] space-y-2 overflow-y-auto">
              {cases.length === 0 ? <div className="text-muted-foreground py-8 text-center text-sm">暂无漏洞组</div> : null}
              {cases.map((item) => (
                <button
                  type="button"
                  key={item.id}
                  onClick={() => void openCase(item)}
                  className={`w-full rounded-md border p-3 text-left transition-colors hover:bg-muted/60 ${
                    selected?.case.id === item.id ? "border-primary bg-muted/50" : ""
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate font-medium">{item.title}</span>
                    <Badge variant={item.active ? "default" : "secondary"}>{item.active ? "有效" : "已解散"}</Badge>
                  </div>
                  <div className="text-muted-foreground mt-1 text-xs">
                    #{item.id} · {item.count} 条 · v{item.version}
                  </div>
                </button>
              ))}
            </CardContent>
          </Card>
        </div>

        <Card className="min-w-0">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">{selected ? selected.case.title : "选择漏洞组"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            {!selected ? (
              <div className="text-muted-foreground py-16 text-center text-sm">选择左侧漏洞组查看成员和统一报告</div>
            ) : (
              <>
                <div className="flex flex-wrap items-center gap-2 text-sm">
                  <Badge variant="outline">v{selected.case.version}</Badge>
                  <Badge variant="outline">报告 v{selected.case.report_version}</Badge>
                  <span className="text-muted-foreground">{selected.case.reason}</span>
                </div>
                <div className="space-y-2">
                  <div className="text-sm font-medium">成员</div>
                  <div className="divide-y rounded-md border">
                    {selected.members.map((finding) => (
                      <div key={finding.id} className="flex items-center gap-3 px-3 py-2">
                        <div className="min-w-0 flex-1">
                          <div className="truncate text-sm font-medium">{findingLabel(finding)}</div>
                          <div className="text-muted-foreground truncate text-xs">
                            #{finding.finding_id ?? finding.id} · {finding.vulnclass} · {finding.severity}
                          </div>
                        </div>
                        <Button variant="ghost" size="sm" onClick={() => void detach(finding)}>
                          <Trash2 />
                          移出
                        </Button>
                      </div>
                    ))}
                  </div>
                </div>
                <div className="grid gap-3 md:grid-cols-[160px_minmax(0,1fr)]">
                  <div className="space-y-1.5">
                    <Label htmlFor="case-severity">统一等级</Label>
                    <select
                      id="case-severity"
                      className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
                      value={severity}
                      onChange={(event) => setSeverity(event.target.value as Severity)}
                    >
                      <option value="critical">critical</option>
                      <option value="high">high</option>
                      <option value="medium">medium</option>
                      <option value="low">low</option>
                    </select>
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="case-severity-reason">评级依据</Label>
                    <Input id="case-severity-reason" value={severityReason} onChange={(event) => setSeverityReason(event.target.value)} />
                  </div>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="case-report">统一报告</Label>
                  <Textarea id="case-report" rows={16} value={report} onChange={(event) => setReport(event.target.value)} placeholder="基于所有成员证据整理统一报告" />
                </div>
                <div className="flex justify-end">
                  <Button onClick={() => void saveReport()} disabled={saving}>
                    {saving ? <Loader2 className="animate-spin" /> : <Save />}
                    保存统一报告
                  </Button>
                </div>
                <div className="space-y-2">
                  <div className="text-sm font-medium">审计记录</div>
                  <div className="max-h-52 space-y-2 overflow-y-auto">
                    {selected.events.map((event) => (
                      <div key={event.id} className="text-muted-foreground rounded-md border px-3 py-2 text-xs">
                        <span className="text-foreground font-medium">{event.action}</span> · {event.actor} · {event.reason}
                      </div>
                    ))}
                  </div>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
