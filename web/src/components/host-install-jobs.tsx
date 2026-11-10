"use client";

import * as React from "react";
import { RefreshCwIcon, SquareIcon } from "lucide-react";
import { toast } from "sonner";

import { api } from "@/lib/api";
import type { HostInstallJob } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";

const stateLabel: Record<HostInstallJob["state"], string> = {
  queued: "排队中",
  running: "执行中",
  done: "已完成",
  failed: "失败",
  cancelled: "已取消",
  interrupted: "已中断",
};

export function HostInstallJobs({ refreshKey = 0, onSettled }: { refreshKey?: number; onSettled?: () => void }) {
  const [jobs, setJobs] = React.useState<HostInstallJob[]>([]);
  const [selected, setSelected] = React.useState<string>("");

  const load = React.useCallback(() => {
    api.hostenvJobs().then(setJobs).catch((e) => toast.error(`读取安装任务失败：${(e as Error).message}`));
  }, []);

  React.useEffect(() => {
    load();
  }, [load, refreshKey]);

  React.useEffect(() => {
    if (!jobs.some((j) => j.state === "queued" || j.state === "running")) return;
    const timer = window.setInterval(load, 2000);
    return () => window.clearInterval(timer);
  }, [jobs, load]);

  const running = jobs.some((j) => j.state === "queued" || j.state === "running");
  const previousRunning = React.useRef(false);
  React.useEffect(() => {
    if (previousRunning.current && !running) onSettled?.();
    previousRunning.current = running;
  }, [running, onSettled]);

  const active = jobs.find((j) => j.id === selected) ?? jobs[0];
  return (
    <div className="grid min-h-0 gap-3 lg:grid-cols-[minmax(240px,0.8fr)_minmax(0,1.4fr)]">
      <div className="min-w-0 border">
        <div className="flex h-10 items-center justify-between border-b px-3">
          <span className="text-sm font-medium">安装任务</span>
          <Button variant="ghost" size="icon" className="size-7" title="刷新任务" onClick={load}>
            <RefreshCwIcon className="size-3.5" />
          </Button>
        </div>
        <ScrollArea className="h-64">
          {jobs.length === 0 && <p className="text-muted-foreground p-4 text-sm">暂无安装记录</p>}
          {jobs.map((job) => (
            <button
              type="button"
              key={job.id}
              onClick={() => setSelected(job.id)}
              className={`flex w-full items-center justify-between gap-2 border-b px-3 py-2 text-left text-sm last:border-0 hover:bg-accent ${active?.id === job.id ? "bg-accent" : ""}`}
            >
              <span className="min-w-0">
                <span className="block truncate font-mono text-xs">{job.target}</span>
                <span className="text-muted-foreground text-xs">{new Date(job.created_at).toLocaleString("zh-CN")}</span>
              </span>
              <Badge variant={job.state === "done" ? "default" : job.state === "failed" ? "destructive" : "secondary"}>
                {stateLabel[job.state]}
              </Badge>
            </button>
          ))}
        </ScrollArea>
      </div>
      <div className="flex min-w-0 flex-col border">
        <div className="flex h-10 items-center justify-between border-b px-3">
          <span className="truncate font-mono text-xs">{active ? `${active.kind} / ${active.target}` : "任务日志"}</span>
          {active && (active.state === "queued" || active.state === "running") && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => api.hostenvCancel(active.id).then(load).catch((e) => toast.error((e as Error).message))}
            >
              <SquareIcon /> 取消
            </Button>
          )}
        </div>
        <ScrollArea className="h-64 bg-zinc-950">
          <pre className="min-w-0 whitespace-pre-wrap break-words p-3 font-mono text-xs text-zinc-200">
            {active?.log || active?.error || "选择任务查看日志"}
          </pre>
        </ScrollArea>
      </div>
    </div>
  );
}
