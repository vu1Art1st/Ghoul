"use client";

import * as React from "react";
import { Activity, Link2, Loader2, Monitor, RefreshCw, Server, Unlink } from "lucide-react";
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
  const [reason, setReason] = React.useState("确认会话归属后绑定");

  const selected = sessions.find((item) => item.session_id === selectedId);

  const load = React.useCallback(async () => {
    setLoading(true);
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
      if (!selectedId && sessionData.items.length > 0) {
        setSelectedId(sessionData.items[0].session_id);
      }
    } catch (error) {
      toast.error(`加载 C2 状态失败: ${error}`);
    } finally {
      setLoading(false);
    }
  }, [selectedId]);

  React.useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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

  const instance = status?.instance;

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Monitor className="size-5" />
          <h1 className="text-lg font-semibold">C2 会话</h1>
          <Badge variant={status?.enabled ? "default" : "secondary"}>{status?.enabled ? status.status : "未启用"}</Badge>
          {status?.cursor?.status ? <Badge variant="outline">事件 {status.cursor.status}</Badge> : null}
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
          <div className="grid gap-3 md:grid-cols-4">
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-xs text-muted-foreground">实例</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="truncate text-lg font-semibold">{instance?.name || "Toshell"}</div>
                <div className="text-muted-foreground truncate text-xs">{instance?.base_url}</div>
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
