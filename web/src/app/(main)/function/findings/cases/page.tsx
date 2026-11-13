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
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type {
  Finding,
  FindingCase,
  FindingCaseDetail,
  FindingMergeJob,
  FindingTaskOption,
  Severity,
} from "@/lib/types";
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
  const [taskFindings, setTaskFindings] = React.useState<Finding[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [findingsLoading, setFindingsLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [selectedFindings, setSelectedFindings] = React.useState<Finding[]>([]);
  const [addMemberIds, setAddMemberIds] = React.useState<string[]>([]);
  const [autoJob, setAutoJob] = React.useState<FindingMergeJob | null>(null);
  const [report, setReport] = React.useState("");
  const [severity, setSeverity] = React.useState<Severity>("high");
  const [severityReason, setSeverityReason] = React.useState("");

  const selectedFindingIds = React.useMemo(
    () => selectedFindings.map(findingKey).filter(Boolean),
    [selectedFindings],
  );
  const selectableFindings = React.useMemo(() => taskFindings.filter((finding) => !finding.case_id), [taskFindings]);
  const addableFindings = React.useMemo(() => taskFindings.filter((finding) => !finding.case_id), [taskFindings]);
  const allSelectableSelected =
    selectableFindings.length > 0 &&
    selectableFindings.every((finding) => selectedFindingIds.includes(findingKey(finding)));

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

  async function loadAutoJob() {
    if (!taskId) {
      setAutoJob(null);
      return;
    }
    try {
      const result = await api.latestAutoFindingMergeJob(taskId);
      setAutoJob(result.job);
    } catch {
      setAutoJob(null);
    }
  }

  async function loadTaskOptions() {
    try {
      const stats = await api.findingStats();
      const options = stats.tasks ?? [];
      setTaskOptions(options);
      if (!taskId && options.length > 0) setTaskId(String(options[0].id));
    } catch (error) {
      toast.error(`加载任务列表失败: ${error}`);
    }
  }

  async function loadTaskFindings() {
    if (!taskId) {
      setTaskFindings([]);
      setSelectedFindings([]);
      return;
    }
    setFindingsLoading(true);
    try {
      const result = await api.findingsPage({
        page: 1,
        pageSize: 200,
        task: taskId,
        sort: "severity",
      });
      setTaskFindings(result.items);
      setSelectedFindings((current) =>
        current.filter((item) => result.items.some((next) => findingKey(next) === findingKey(item))),
      );
    } catch (error) {
      toast.error(`加载 Finding 清单失败: ${error}`);
    } finally {
      setFindingsLoading(false);
    }
  }

  React.useEffect(() => {
    void loadTaskOptions();
  }, []);

  React.useEffect(() => {
    void loadCases();
    void loadTaskFindings();
    void loadAutoJob();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId]);

  React.useEffect(() => {
    if (!taskId) return;
    const timer = window.setInterval(() => void loadAutoJob(), 5000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId]);

  async function openCase(item: FindingCase) {
    try {
      const detail = await api.findingCase(item.id);
      setSelected(detail);
      setAddMemberIds([]);
      setReport(detail.case.report ?? item.report ?? "");
      setSeverity((detail.case.severity || item.severity || "high") as Severity);
      setSeverityReason(detail.case.severity_reason || item.severity_reason || "");
    } catch (error) {
      toast.error(`加载详情失败: ${error}`);
    }
  }

  function toggleFinding(finding: Finding, checked: boolean) {
    setSelectedFindings((current) => {
      const key = findingKey(finding);
      if (checked) {
        return current.some((item) => findingKey(item) === key) ? current : [...current, finding];
      }
      return current.filter((item) => findingKey(item) !== key);
    });
  }

  function toggleAllFindings(checked: boolean) {
    setSelectedFindings(checked ? taskFindings.filter((finding) => !finding.case_id) : []);
  }

  function toggleAddMember(finding: Finding, checked: boolean) {
    const key = findingKey(finding);
    setAddMemberIds((current) =>
      checked ? (current.includes(key) ? current : [...current, key]) : current.filter((id) => id !== key),
    );
  }

  async function waitForMergeJob(jobId: string): Promise<FindingMergeJob> {
    for (let attempt = 0; attempt < 600; attempt += 1) {
      await new Promise((resolve) => window.setTimeout(resolve, 1000));
      const job = await api.findingMergeJob(jobId);
      if (job.status === "succeeded") return job;
      if (job.status === "failed") throw new Error(job.error || "漏洞归并 Agent 执行失败");
    }
    throw new Error("漏洞归并 Agent 超时");
  }

  async function runAgentMerge() {
    if (selectedFindingIds.length < 2) {
      toast.error("至少选择两条 Finding");
      return;
    }
    setSaving(true);
    try {
      const job = await api.createFindingMergeJob(selectedFindingIds);
      const finished = await waitForMergeJob(job.id);
      if (finished.case_ids.length === 0) {
        toast.success("未发现满足同一根因条件的漏洞组");
      } else {
        toast.success(
          `AI 已生成 ${finished.case_ids.length} 个漏洞组，保留 ${finished.ungrouped_finding_ids.length} 条未归并 Finding`,
        );
      }
      setSelectedFindings([]);
      await Promise.all([loadCases(), loadTaskFindings(), loadAutoJob()]);
      if (finished.case_ids.length === 1) {
        const item = await api.findingCase(String(finished.case_ids[0]));
        await openCase(item.case);
      } else {
        setSelected(null);
      }
    } catch (error) {
      toast.error(`AI 归并失败: ${error}`);
    } finally {
      setSaving(false);
    }
  }

  async function addMembersAndRegenerate() {
    if (!selected || addMemberIds.length === 0) return;
    setSaving(true);
    try {
      const result = await api.addFindingCaseMembers(selected.case.id, {
        finding_ids: addMemberIds.map(Number),
        expected_version: selected.case.version,
        reason: "人工补充同源成员并重新生成统一报告",
        regenerate: true,
      });
      if (result.job) {
        await waitForMergeJob(result.job.id);
      }
      toast.success("已添加成员并重新生成统一报告");
      setAddMemberIds([]);
      await Promise.all([loadCases(), loadTaskFindings()]);
      const detail = await api.findingCase(selected.case.id);
      setSelected(detail);
      setReport(detail.case.report ?? "");
      setSeverity((detail.case.severity || "high") as Severity);
      setSeverityReason(detail.case.severity_reason || "");
    } catch (error) {
      toast.error(`添加成员或重新生成失败: ${error}`);
      await Promise.all([loadCases(), loadTaskFindings()]);
      if (selected) {
        const detail = await api.findingCase(selected.case.id).catch(() => null);
        if (detail) {
          setSelected(detail);
          setReport(detail.case.report ?? "");
          setSeverity((detail.case.severity || "high") as Severity);
          setSeverityReason(detail.case.severity_reason || "");
        }
      }
    } finally {
      setSaving(false);
    }
  }

  async function regenerateCase() {
    if (!selected) return;
    setSaving(true);
    try {
      const job = await api.regenerateFindingCase(selected.case.id);
      const finished = await waitForMergeJob(job.id);
      const caseID = finished.case_ids[0] ? String(finished.case_ids[0]) : finished.case_id;
      if (!caseID) throw new Error("Agent 任务完成但未返回漏洞组");
      toast.success("AI 已重新生成定级和统一报告");
      await Promise.all([loadCases(), loadTaskFindings(), loadAutoJob()]);
      const detail = await api.findingCase(caseID);
      setSelected(detail);
      setReport(detail.case.report ?? "");
      setSeverity((detail.case.severity || "high") as Severity);
      setSeverityReason(detail.case.severity_reason || "");
    } catch (error) {
      toast.error(`重新生成失败: ${error}`);
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
          <Button
            variant="outline"
            onClick={() => void Promise.all([loadCases(), loadTaskFindings()])}
            disabled={loading || findingsLoading}
          >
            {loading ? <Loader2 className="animate-spin" /> : <RefreshCw />}
            刷新
          </Button>
        </div>
      </div>

      {taskId && autoJob ? (
        <div className="flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-xs">
          <Badge variant="secondary">自动归并</Badge>
          <span className="text-muted-foreground">
            任务完成后的最近一次自动归并：{autoJob.status}
            {autoJob.group_count > 0 ? ` · ${autoJob.group_count} 个漏洞组` : ""}
            {autoJob.error ? ` · ${autoJob.error}` : ""}
          </span>
        </div>
      ) : null}

      <div className="grid gap-4 xl:grid-cols-[380px_minmax(0,1fr)]">
        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader className="pb-3">
              <div className="flex items-center justify-between gap-2">
                <CardTitle className="text-sm">选择 Finding</CardTitle>
                <Badge variant="outline">{selectedFindingIds.length} 已选</Badge>
              </div>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="flex items-center justify-between rounded-md border px-3 py-2">
                <label className="flex items-center gap-2 text-sm font-medium">
                  <Checkbox
                    checked={allSelectableSelected}
                    onCheckedChange={(checked) => toggleAllFindings(checked === true)}
                    disabled={selectableFindings.length === 0 || findingsLoading}
                  />
                  全选可归并 Finding
                </label>
                {findingsLoading ? <Loader2 className="size-4 animate-spin text-muted-foreground" /> : null}
              </div>
              <div className="max-h-[360px] divide-y overflow-y-auto rounded-md border">
                {taskFindings.map((finding) => {
                  const key = findingKey(finding);
                  const grouped = Boolean(finding.case_id);
                  return (
                    <label
                      key={key}
                      className={`flex items-start gap-3 px-3 py-2.5 ${
                        grouped ? "cursor-not-allowed bg-muted/40" : "cursor-pointer hover:bg-muted/50"
                      }`}
                    >
                      <Checkbox
                        className="mt-0.5"
                        checked={selectedFindingIds.includes(key)}
                        disabled={grouped}
                        onCheckedChange={(checked) => toggleFinding(finding, checked === true)}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-2">
                          <span className="truncate text-sm font-medium">{findingLabel(finding)}</span>
                          <Badge variant="outline" className="shrink-0">
                            {finding.severity}
                          </Badge>
                        </span>
                        <span className="text-muted-foreground mt-0.5 block truncate text-xs">
                          #{key} · {finding.vulnclass} · {finding.status}
                        </span>
                        {grouped ? (
                          <span className="mt-1 block truncate text-xs text-primary">
                            已归并：{finding.case_title || `漏洞组 #${finding.case_id}`}
                          </span>
                        ) : null}
                      </span>
                    </label>
                  );
                })}
                {!findingsLoading && taskFindings.length === 0 ? (
                  <div className="text-muted-foreground px-3 py-8 text-center text-sm">当前任务暂无 Finding</div>
                ) : null}
              </div>
              <Button
                className="w-full"
                onClick={() => void runAgentMerge()}
                disabled={saving || selectedFindingIds.length < 2}
              >
                {saving ? <Loader2 className="animate-spin" /> : <Sparkles />}
                AI 自动分组并生成报告
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
                    <div className="flex shrink-0 gap-1">
                      {item.report_stale && item.active ? <Badge variant="secondary">报告过期</Badge> : null}
                      <Badge variant={item.active ? "default" : "secondary"}>{item.active ? "有效" : "已解散"}</Badge>
                    </div>
                  </div>
                  <div className="text-muted-foreground mt-1 text-xs">
                    #{item.id} · {item.count} 条 · v{item.version} · {item.managed_by === "auto" ? "自动" : "人工"}
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
                <div className="flex shrink-0 gap-2">
                  <Button variant="outline" size="sm" onClick={() => void regenerateCase()} disabled={saving}>
                    {saving ? <Loader2 className="animate-spin" /> : <Sparkles />}
                    AI 重新生成
                  </Button>
                  <Button variant="destructive" size="sm" onClick={() => void deleteCase()} disabled={saving}>
                    <Trash2 />
                    删除漏洞组
                  </Button>
                </div>
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
                  <Badge variant="secondary">
                    {selected.case.managed_by === "auto" ? "自动归并" : "人工锁定"}
                  </Badge>
                  {selected.case.report_stale ? <Badge variant="destructive">成员已变更，报告待更新</Badge> : null}
                  {!selected.case.report || !selected.case.severity || !selected.case.severity_reason ? (
                    <Badge variant="secondary">报告待生成</Badge>
                  ) : null}
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
                <div className="space-y-2 rounded-md border p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div>
                      <div className="text-sm font-medium">补充成员</div>
                      <div className="text-muted-foreground text-xs">选择尚未归并的 Finding，添加后会重新生成统一报告。</div>
                    </div>
                    <Button
                      size="sm"
                      onClick={() => void addMembersAndRegenerate()}
                      disabled={saving || addMemberIds.length === 0}
                    >
                      {saving ? <Loader2 className="animate-spin" /> : <Plus />}
                      添加并重新生成
                    </Button>
                  </div>
                  <div className="max-h-52 divide-y overflow-y-auto rounded-md border">
                    {addableFindings.map((finding) => {
                      const key = findingKey(finding);
                      return (
                        <label key={key} className="flex cursor-pointer items-start gap-3 px-3 py-2 hover:bg-muted/50">
                          <Checkbox
                            className="mt-0.5"
                            checked={addMemberIds.includes(key)}
                            onCheckedChange={(checked) => toggleAddMember(finding, checked === true)}
                          />
                          <span className="min-w-0 flex-1">
                            <span className="block truncate text-sm">{findingLabel(finding)}</span>
                            <span className="text-muted-foreground block truncate text-xs">
                              #{key} · {finding.severity} · {finding.vulnclass}
                            </span>
                          </span>
                        </label>
                      );
                    })}
                    {addableFindings.length === 0 ? (
                      <div className="text-muted-foreground px-3 py-5 text-center text-xs">暂无尚未归并的 Finding</div>
                    ) : null}
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
