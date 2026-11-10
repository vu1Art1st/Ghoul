"use client";

import * as React from "react";
import Link from "next/link";
import { Activity, ClipboardCheck, ExternalLink, Link2, Loader2, Monitor, RefreshCw, Server, Unlink } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type { C2Event, C2Listener, C2Session, C2Status, C2Task } from "@/lib/types";

function fmtTime(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export default function C2Page() {
  const [status, setStatus] = React.useState<C2Status | null>(null);
  const [sessions, setSessions] = React.useState<C2Session[]>([]);
  const [listeners, setListeners] = React.useState<C2Listener[]>([]);
  const [tasks, setTasks] = React.useState<C2Task[]>([]);
  const [events, setEvents] = React.useState<C2Event[]>([]);
  const [selectedId, setSelectedId] = React.useState("");
  const [loading, setLoading] = React.useState(true);
  const [binding, setBinding] = React.useState(false);
  const [taskId, setTaskId] = React.useState("");
  const [assetId, setAssetId] = React.useState("");
  const [manualTool, setManualTool] = React.useState("c2_recon");
  const [manualAction, setManualAction] = React.useState("system_info");
  const [manualValue, setManualValue] = React.useState("");
  const [manualBusy, setManualBusy] = React.useState(false);
  const [reason, setReason] = React.useState("确认会话归属后绑定");

  const selected = sessions.find((item) => item.session_id === selectedId);

  const load = React.useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      const [statusData, sessionData, listenerData, eventData] = await Promise.all([
        api.c2Status(),
        api.c2Sessions({ limit: 500 }),
        api.c2Listeners(),
        api.c2Events(100),
      ]);
      setStatus(statusData);
      setSessions(sessionData.items);
      setListeners(listenerData.items);
      setEvents(eventData.items);
      setSelectedId((current) => current || sessionData.items[0]?.session_id || "");
    } catch (error) {
      if (!silent) toast.error(`加载 C2 状态失败: ${error}`);
    } finally {
      if (!silent) setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load(false);
    const timer = window.setInterval(() => void load(true), 5_000);
    return () => window.clearInterval(timer);
  }, [load]);

  React.useEffect(() => {
    if (!selectedId) {
      setTasks([]);
      return;
    }
    api
      .c2Tasks(selectedId)
      .then((result) => setTasks(result.items))
      .catch(() => setTasks([]));
  }, [selectedId]);

  async function bindSession() {
    if (!selected) return;
    const numericTask = Number(taskId);
    if (!numericTask) {
      toast.error("请输入有效的 Ghoul 任务 ID");
      return;
    }
    setBinding(true);
    try {
      await api.bindC2Session(selected.session_id, {
        task_id: numericTask,
        asset_id: Number(assetId) || undefined,
        reason: reason.trim() || "人工绑定",
      });
      toast.success("会话已绑定");
      await load();
    } catch (error) {
      toast.error(`绑定失败: ${error}`);
    } finally {
      setBinding(false);
    }
  }

  async function unbindSession() {
    if (!selected) return;
    try {
      await api.unbindC2Session(selected.session_id);
      toast.success("已解除绑定");
      await load();
    } catch (error) {
      toast.error(`解绑失败: ${error}`);
    }
  }

  async function createManualOperation() {
    if (!selected?.binding) {
      toast.error("请先将会话绑定到当前 Ghoul 任务");
      return;
    }
    const params: Record<string, unknown> = {};
    if (manualTool === "c2_recon") params.action = manualAction;
    if (manualTool === "c2_file_list") params.path = manualValue;
    if (manualTool === "c2_tunnel") {
      params.action = manualAction;
      if (manualValue.trim()) params.local_port = Number(manualValue);
    }
    if (manualTool === "c2_exec") params.command = manualValue;
    setManualBusy(true);
    try {
      const created = await api.createC2Operation({
        task_id: Number(selected.binding.task_id),
        session_id: selected.session_id,
        tool: manualTool,
        action: manualTool === "c2_recon" || manualTool === "c2_tunnel" ? manualAction : undefined,
        params,
      });
      let operation = created.operation;
      if (operation.status === "approved") {
        operation = await api.executeC2Operation(operation.id);
      }
      toast.success(
        operation.status === "pending_approval"
          ? `操作 #${operation.id} 已创建，请在审批中心处理`
          : `操作 #${operation.id} 已提交`,
      );
      setManualValue("");
    } catch (error) {
      toast.error(`创建 C2 操作失败: ${error}`);
    } finally {
      setManualBusy(false);
    }
  }

  const instance = status?.instance;
  const advancedConsoleURL = status?.console_url || status?.mcp?.console_url || instance?.base_url;
  const statusVariant =
    status?.status === "error" ? "destructive" : status?.status === "reconnecting" ? "secondary" : "default";

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Monitor className="size-5" />
          <h1 className="text-lg font-semibold">C2 会话</h1>
          <Badge variant={status?.enabled ? statusVariant : "secondary"}>{status?.enabled ? status.status : "未启用"}</Badge>
          {status?.cursor?.status ? <Badge variant="outline">事件 {status.cursor.status}</Badge> : null}
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" asChild>
            <Link href="/system/c2/approvals">
              <ClipboardCheck />
              审批中心
            </Link>
          </Button>
          {advancedConsoleURL ? (
            <Button variant="outline" size="sm" asChild>
              <a href={advancedConsoleURL} target="_blank" rel="noreferrer">
                <ExternalLink />
                Toshell 控制台
              </a>
            </Button>
          ) : null}
        </div>
        <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
          <RefreshCw className={loading ? "animate-spin" : ""} />
          刷新
        </Button>
      </div>

      {!status?.enabled ? (
        <Card>
          <CardContent className="py-16 text-center text-sm text-muted-foreground">
            C2 适配器未启用，请通过配置文件开启 `c2.enabled`
          </CardContent>
        </Card>
      ) : (
        <>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-5">
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-xs text-muted-foreground">实例</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="flex items-center justify-between gap-2">
                  <div className="truncate text-lg font-semibold">{instance?.name || "Toshell"}</div>
                  {instance?.base_url ? (
                    <Button variant="ghost" size="icon" asChild>
                      <a href={advancedConsoleURL} target="_blank" rel="noreferrer" title="打开 Toshell 高级控制台">
                        <ExternalLink />
                      </a>
                    </Button>
                  ) : null}
                </div>
                <div className="text-muted-foreground truncate text-xs">{instance?.base_url}</div>
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-xs text-muted-foreground">MCP 只读面</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="flex items-center justify-between gap-2">
                  <div className="text-lg font-semibold tabular-nums">
                    {status.mcp?.configured ? `${status.mcp.tool_count ?? 0} 工具` : "未配置"}
                  </div>
                  <Badge variant={status.mcp?.enabled ? "default" : "outline"}>
                    {status.mcp?.configured ? (status.mcp.enabled ? "enabled" : "disabled") : "off"}
                  </Badge>
                </div>
                <div className="text-muted-foreground truncate text-xs">
                  {status.mcp?.configured
                    ? `白名单 ${status.mcp.allowlist_count ?? 0}/${status.mcp.expected_readonly ?? 0}`
                    : "设置 GHOUL_C2_MCP_URL 后启用"}
                </div>
                {status.mcp?.missing_tools?.length ? (
                  <div className="mt-1 truncate text-xs text-amber-600" title={status.mcp.missing_tools.join(", ")}>
                    缺少 {status.mcp.missing_tools.length} 个只读工具
                  </div>
                ) : null}
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-xs text-muted-foreground">会话</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="text-lg font-semibold tabular-nums">{status.counts?.sessions ?? sessions.length}</div>
                <div className="text-muted-foreground text-xs">{sessions.filter((item) => item.binding).length} 已绑定</div>
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-xs text-muted-foreground">监听器</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="text-lg font-semibold tabular-nums">{status.counts?.listeners ?? listeners.length}</div>
                <div className="text-muted-foreground text-xs">{listeners.filter((item) => item.status === "running").length} 运行中</div>
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-xs text-muted-foreground">最后同步</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="truncate text-sm font-medium">{fmtTime(status.last_sync_at || instance?.last_sync_at)}</div>
                {status.last_error ? <div className="mt-1 truncate text-xs text-red-500">{status.last_error}</div> : null}
              </CardContent>
            </Card>
          </div>

          <div className="grid gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.65fr)]">
            <Card className="min-w-0">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm">会话</CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                <div className="max-h-[520px] overflow-auto">
                  <table className="w-full min-w-[720px] text-sm">
                    <thead className="bg-muted/60 text-muted-foreground sticky top-0">
                      <tr>
                        <th className="px-3 py-2 text-left font-medium">主机</th>
                        <th className="px-3 py-2 text-left font-medium">用户</th>
                        <th className="px-3 py-2 text-left font-medium">系统</th>
                        <th className="px-3 py-2 text-left font-medium">状态</th>
                        <th className="px-3 py-2 text-left font-medium">绑定</th>
                      </tr>
                    </thead>
                    <tbody>
                      {sessions.map((item) => (
                        <tr
                          key={item.session_id}
                          onClick={() => setSelectedId(item.session_id)}
                          className={`cursor-pointer border-t transition-colors hover:bg-muted/50 ${
                            selectedId === item.session_id ? "bg-muted/70" : ""
                          }`}
                        >
                          <td className="px-3 py-2">
                            <div className="font-medium">{item.hostname || "—"}</div>
                            <div className="text-muted-foreground font-mono text-xs">{item.session_id}</div>
                          </td>
                          <td className="px-3 py-2">{item.username || "—"}</td>
                          <td className="px-3 py-2">{item.os} / {item.arch}</td>
                          <td className="px-3 py-2">
                            <Badge variant={item.status === "active" ? "default" : "outline"}>{item.status || "unknown"}</Badge>
                          </td>
                          <td className="px-3 py-2">
                            {item.binding ? <Badge variant="secondary">任务 #{item.binding.task_id}</Badge> : "—"}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </CardContent>
            </Card>

            <Card className="h-fit">
              <CardHeader className="pb-3">
                <CardTitle className="flex items-center gap-2 text-sm">
                  <Server className="size-4" />
                  会话详情
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                {!selected ? (
                  <div className="py-12 text-center text-sm text-muted-foreground">选择会话</div>
                ) : (
                  <>
                    <div className="space-y-1 text-sm">
                      <div className="font-semibold">{selected.hostname || selected.session_id}</div>
                      <div className="text-muted-foreground font-mono text-xs">{selected.session_id}</div>
                      <div className="text-muted-foreground text-xs">
                        {selected.username} · {selected.os}/{selected.arch} · {selected.remote_addr || "—"}
                      </div>
                    </div>
                    {selected.binding ? (
                      <div className="space-y-2 rounded-md border p-3">
                        <div className="flex items-center justify-between gap-2">
                          <div className="text-sm font-medium">已绑定任务 #{selected.binding.task_id}</div>
                          <Button variant="outline" size="sm" onClick={() => void unbindSession()}>
                            <Unlink />
                            解绑
                          </Button>
                        </div>
                        <div className="text-muted-foreground text-xs">{selected.binding.binding_reason}</div>
                      </div>
                    ) : (
                      <div className="space-y-3 rounded-md border p-3">
                        <div className="space-y-1.5">
                          <Label htmlFor="c2-task-id">任务 ID</Label>
                          <Input id="c2-task-id" value={taskId} onChange={(event) => setTaskId(event.target.value)} />
                        </div>
                        <div className="space-y-1.5">
                          <Label htmlFor="c2-asset-id">资产 ID</Label>
                          <Input id="c2-asset-id" value={assetId} onChange={(event) => setAssetId(event.target.value)} placeholder="可选" />
                        </div>
                        <div className="space-y-1.5">
                          <Label htmlFor="c2-bind-reason">绑定原因</Label>
                          <Input id="c2-bind-reason" value={reason} onChange={(event) => setReason(event.target.value)} />
                        </div>
                        <Button className="w-full" onClick={() => void bindSession()} disabled={binding}>
                          {binding ? <Loader2 className="animate-spin" /> : <Link2 />}
                          绑定会话
                        </Button>
                      </div>
                    )}
                    <div className="space-y-2 rounded-md border p-3">
                      <div className="flex items-center justify-between gap-2">
                        <div className="text-sm font-medium">人工 C2 操作</div>
                        <Badge variant="outline">actor=user / source=manual</Badge>
                      </div>
                      <select
                        className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                        value={manualTool}
                        onChange={(event) => setManualTool(event.target.value)}
                      >
                        <option value="c2_recon">系统信息 / 只读侦察</option>
                        <option value="c2_file_list">文件列表</option>
                        <option value="c2_process_list">进程列表</option>
                        <option value="c2_screenshot">截图</option>
                        <option value="c2_tunnel">隧道</option>
                        <option value="c2_exec">命令执行（Danger）</option>
                      </select>
                      {manualTool === "c2_recon" || manualTool === "c2_tunnel" ? (
                        <select
                          className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                          value={manualAction}
                          onChange={(event) => setManualAction(event.target.value)}
                        >
                          {manualTool === "c2_recon" ? (
                            <>
                              <option value="system_info">system_info</option>
                              <option value="user_info">user_info</option>
                              <option value="net_info">net_info</option>
                              <option value="net_connections">net_connections</option>
                              <option value="env_vars">env_vars</option>
                              <option value="scheduled_tasks">scheduled_tasks</option>
                            </>
                          ) : (
                            <>
                              <option value="list">list</option>
                              <option value="start">start</option>
                              <option value="stop">stop</option>
                            </>
                          )}
                        </select>
                      ) : null}
                      {manualTool === "c2_file_list" ||
                      manualTool === "c2_exec" ||
                      (manualTool === "c2_tunnel" && manualAction !== "list") ? (
                        <Input
                          value={manualValue}
                          onChange={(event) => setManualValue(event.target.value)}
                          placeholder={
                            manualTool === "c2_file_list"
                              ? "目录路径"
                              : manualTool === "c2_exec"
                                ? "命令"
                                : "本地端口"
                          }
                        />
                      ) : null}
                      <Button
                        className="w-full"
                        variant="outline"
                        onClick={() => void createManualOperation()}
                        disabled={manualBusy || !selected.binding}
                      >
                        {manualBusy ? <Loader2 className="animate-spin" /> : <ClipboardCheck />}
                        创建操作
                      </Button>
                    </div>
                    <Tabs defaultValue="tasks">
                      <TabsList>
                        <TabsTrigger value="tasks">任务</TabsTrigger>
                        <TabsTrigger value="events">事件</TabsTrigger>
                      </TabsList>
                      <TabsContent value="tasks" className="mt-3 space-y-2">
                        {tasks.length === 0 ? <div className="text-muted-foreground text-sm">暂无任务</div> : null}
                        {tasks.map((task) => (
                          <div key={task.id} className="rounded-md border px-3 py-2">
                            <div className="flex items-center justify-between gap-2">
                              <span className="text-sm font-medium">#{task.task_id} {task.task_type}</span>
                              <Badge variant="outline">{task.status}</Badge>
                            </div>
                            <div className="text-muted-foreground mt-1 line-clamp-2 text-xs">{task.output_summary || task.command || "—"}</div>
                          </div>
                        ))}
                      </TabsContent>
                      <TabsContent value="events" className="mt-3 space-y-2">
                        {events.map((event) => (
                          <div key={event.id} className="rounded-md border px-3 py-2">
                            <div className="flex items-center justify-between gap-2">
                              <span className="text-sm font-medium">{event.type}</span>
                              <span className="text-muted-foreground text-xs">{fmtTime(event.time)}</span>
                            </div>
                          </div>
                        ))}
                      </TabsContent>
                    </Tabs>
                  </>
                )}
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="flex items-center gap-2 text-sm">
                <Activity className="size-4" />
                监听器
              </CardTitle>
            </CardHeader>
            <CardContent className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
              {listeners.map((item) => (
                <div key={item.listener_id} className="rounded-md border px-3 py-2">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-sm font-medium">{item.name || item.listener_id}</span>
                    <Badge variant={item.status === "running" ? "default" : "outline"}>{item.status}</Badge>
                  </div>
                  <div className="text-muted-foreground mt-1 text-xs">
                    {item.type}/{item.protocol} · {item.bind_addr}:{item.bind_port} · {item.connections} 连接
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
