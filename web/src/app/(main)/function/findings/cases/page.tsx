"use client";

import * as React from "react";
import {
  ArrowLeft,
  GitMerge,
  Loader2,
  Plus,
  RefreshCw,
  Save,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { Finding, FindingCase, FindingCaseDetail, FindingTaskOption, Severity } from "@/lib/types";
import Link from "next/link";

function findingLabel(finding: Finding): string {
  return finding.name || finding.vulnclass || `Finding #${finding.finding_id ?? finding.id}`;
}

function findingKey(finding: Finding): string {
  return finding.finding_id ?? finding.id;
}

function taskLabel(option: FindingTaskOption): string {
  const name = option.name || option.description || `任务 #${option.id}`;
  return `#${option.id} ${name}`;
}

export default function FindingCasesPage() {
  const [taskId, setTaskId] = React.useState("");
  const [taskOptions, setTaskOptions] = React.useState<FindingTaskOption[]>([]);
  const [cases, setCases] = React.useState<FindingCase[]>([]);
  const [selected, setSelected] = React.useState<FindingCaseDetail | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [resolving, setResolving] = React.useState(false);
  const [suggesting, setSuggesting] = React.useState(false);
  const [findingInput, setFindingInput] = React.useState("");
  const [selectedFindings, setSelectedFindings] = React.useState<Finding[]>([]);
  const [title, setTitle] = React.useState("");
  const [reason, setReason] = React.useState("");
  const [report, setReport] = React.useState("");
  const [severity, setSeverity] = React.useState<Severity>("high");
  const [severityReason, setSeverityReason] = React.useState("");

  const selectedFindingIds = React.useMemo(
    () => selectedFindings.map(findingKey).filter(Boolean),
    [selectedFindings],
  );

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

  async function loadTaskOptions() {
    try {
      const stats = await api.findingStats();
      setTaskOptions(stats.tasks ?? []);
    } catch (error) {
      toast.error(`加载任务列表失败: ${error}`);
    }
  }

  React.useEffect(() => {
    void loadTaskOptions();
  }, []);

  React.useEffect(() => {
    void loadCases();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId]);

  async function openCase(item: FindingCase) {
    try {
      const detail = await api.findingCase(item.id);
      setSelected(detail);
      setReport(detail.case.report ?? item.report ?? "");
      setSeverity((detail.case.severity || item.severity || "high") as Severity);
      setSeverityReason(detail.case.severity_reason || item.severity_reason || "");
    } catch (error) {
      toast.error(`加载详情失败: ${error}`);
    }
  }

  async function addFindings() {
    const parts = findingInput
      .split(/[\s,，;；]+/)
      .map((value) => value.trim())
      .filter(Boolean);
    if (parts.length === 0) return;
    if (parts.some((value) => !/^\d+$/.test(value) || Number(value) <= 0)) {
      toast.error("Finding ID 必须是正整数");
      return;
    }
    const existing = new Set(selectedFindings.map(findingKey));
    const ids = Array.from(new Set(parts)).filter((id) => !existing.has(id));
    if (ids.length === 0) {
      setFindingInput("");
      return;
    }

    setResolving(true);
    try {
      const loaded = await Promise.all(ids.map((id) => api.getFinding(id)));
      const next = [...selectedFindings, ...loaded];
      const taskIds = new Set(next.map((finding) => finding.task_id).filter(Boolean).map(String));
      if (taskIds.size !== 1 || next.some((finding) => !finding.task_id)) {
        toast.error("只能归并同一任务下的漏洞");
        return;
      }
      setSelectedFindings(next);
      setFindingInput("");
      toast.success(`已获取 ${loaded.length} 条漏洞`);
    } catch (error) {
      toast.error(`获取漏洞失败: ${error}`);
    } finally {
      setResolving(false);
    }
  }

  function removeFinding(finding: Finding) {
    const key = findingKey(finding);
    setSelectedFindings((current) => current.filter((item) => findingKey(item) !== key));
  }

  async function suggestCase() {
    if (selectedFindingIds.length < 2) {
      toast.error("至少选择两条 Finding");
      return;
    }
    setSuggesting(true);
    try {
      const suggestion = await api.suggestFindingCase(selectedFindingIds);
      setTitle(suggestion.title);
      setReason(suggestion.reason);
      toast.success("AI 已生成统一名称和归并理由");
    } catch (error) {
      toast.error(`AI 生成失败: ${error}`);
    } finally {
      setSuggesting(false);
    }
  }

  async function mergeFindings() {
    if (selectedFindingIds.length < 2 || !title.trim() || !reason.trim()) {
      toast.error("请选择至少两个 Finding，并填写统一名称和归并理由");
      return;
    }
    setSaving(true);
    try {
      const item = await api.mergeFindingCases(
        undefined,
        selectedFindingIds,
        title.trim(),
        reason.trim(),
      );
      toast.success(`已归并到漏洞组 #${item.id}`);
      setSelectedFindings([]);
      setFindingInput("");
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
    const findingId = findingKey(finding);
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

  async function deleteCase() {
    if (!selected) return;
    if (!window.confirm(`删除漏洞组「${selected.case.title}」？漏洞本身和证据不会被删除。`)) return;
    setSaving(true);
    try {
      await api.deleteFindingCase(selected.case.id);
      setSelected(null);
      setReport("");
      setSeverityReason("");
      toast.success("漏洞组已删除");
      await loadCases();
    } catch (error) {
      toast.error(`删除漏洞组失败: ${error}`);
    } finally {
      setSaving(false);
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
            <Label className="text-xs">任务筛选</Label>
            <Select value={taskId || "all"} onValueChange={(value) => setTaskId(value === "all" ? "" : value)}>
              <SelectTrigger className="w-64" aria-label="任务筛选">
                <SelectValue placeholder="全部任务" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部任务</SelectItem>
                {taskOptions.map((option) => {
                  const id = String(option.id);
                  return (
                    <SelectItem key={id} value={id}>
                      <span className="flex w-full items-center gap-2">
                        <span className="max-w-56 truncate" title={taskLabel(option)}>
                          {taskLabel(option)}
                        </span>
                        <span className="text-muted-foreground tabular-nums">{option.count}</span>
                      </span>
                    </SelectItem>
                  );
                })}
              </SelectContent>
            </Select>
          </div>
          <Button variant="outline" onClick={() => void loadCases()} disabled={loading}>
            {loading ? <Loader2 className="animate-spin" /> : <RefreshCw />}
            刷新
          </Button>
        </div>
      </div>

      <div className="grid gap-4 xl:grid-cols-[380px_minmax(0,1fr)]">
        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">新建归并</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="space-y-1.5">
                <Label htmlFor="merge-ids">Finding ID</Label>
                <div className="flex gap-2">
                  <Input
                    id="merge-ids"
                    value={findingInput}
                    onChange={(event) => setFindingInput(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        event.preventDefault();
                        void addFindings();
                      }
                    }}
                    placeholder="105, 108"
                  />
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => void addFindings()}
                    disabled={resolving || !findingInput.trim()}
                  >
                    {resolving ? <Loader2 className="animate-spin" /> : <Plus />}
                    添加
                  </Button>
                </div>
              </div>

              {selectedFindings.length > 0 ? (
                <div className="divide-y rounded-md border">
                  {selectedFindings.map((finding) => (
                    <div key={findingKey(finding)} className="flex items-center gap-2 px-3 py-2">
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-medium">{findingLabel(finding)}</div>
                        <div className="text-muted-foreground truncate text-xs">
                          #{findingKey(finding)} · {finding.severity} · {finding.vulnclass}
                        </div>
                      </div>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`移除 Finding #${findingKey(finding)}`}
                        title="移除"
                        onClick={() => removeFinding(finding)}
                      >
                        <X />
                      </Button>
                    </div>
                  ))}
                </div>
              ) : null}

              <div className="space-y-1.5">
                <div className="flex items-center justify-between gap-2">
                  <Label htmlFor="merge-title">统一名称</Label>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => void suggestCase()}
                    disabled={suggesting || selectedFindingIds.length < 2}
                  >
                    {suggesting ? <Loader2 className="animate-spin" /> : <Sparkles />}
                    AI 生成
                  </Button>
                </div>
                <Input id="merge-title" value={title} onChange={(event) => setTitle(event.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="merge-reason">归并理由</Label>
                <Textarea
                  id="merge-reason"
                  rows={3}
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                  placeholder="说明这些记录为何是同一根因"
                />
              </div>
              <Button
                className="w-full"
                onClick={() => void mergeFindings()}
                disabled={saving || selectedFindingIds.length < 2}
              >
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
            <div className="flex items-center justify-between gap-3">
              <CardTitle className="min-w-0 truncate text-sm">{selected ? selected.case.title : "选择漏洞组"}</CardTitle>
              {selected ? (
                <Button variant="destructive" size="sm" onClick={() => void deleteCase()} disabled={saving}>
                  <Trash2 />
                  删除漏洞组
                </Button>
              ) : null}
            </div>
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
                            #{findingKey(finding)} · {finding.vulnclass} · {finding.severity}
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
                    <Input
                      id="case-severity-reason"
                      value={severityReason}
                      onChange={(event) => setSeverityReason(event.target.value)}
                    />
                  </div>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="case-report">统一报告</Label>
                  <Textarea
                    id="case-report"
                    rows={16}
                    value={report}
                    onChange={(event) => setReport(event.target.value)}
                    placeholder="基于所有成员证据整理统一报告"
                  />
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
