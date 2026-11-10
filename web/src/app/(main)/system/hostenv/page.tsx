"use client";

import * as React from "react";
import { CheckIcon, DownloadIcon, LoaderCircleIcon, RefreshCwIcon, SearchIcon } from "lucide-react";
import { toast } from "sonner";

import { api } from "@/lib/api";
import type { HostTool, HostenvStatus } from "@/lib/types";
import { HostInstallJobs } from "@/components/host-install-jobs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

const categories: Record<string, string> = {
  network: "网络与主机",
  dns: "子域名与 DNS",
  web: "Web 应用",
  runtime: "通用与运行时",
  middleware: "中间件协议",
  material: "资料与字典",
};

export default function HostenvPage() {
  const [status, setStatus] = React.useState<HostenvStatus | null>(null);
  const [query, setQuery] = React.useState("");
  const [selected, setSelected] = React.useState<Set<string>>(new Set());
  const [proxy, setProxy] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [refreshKey, setRefreshKey] = React.useState(0);

  const load = React.useCallback(() => {
    api.hostenvStatus().then((s) => {
      setStatus(s);
      setProxy(s.installer.proxy);
    }).catch((e) => toast.error(`环境体检失败：${(e as Error).message}`));
  }, []);
  React.useEffect(load, [load]);

  const tools = React.useMemo(() => {
    const q = query.trim().toLowerCase();
    return (status?.tools ?? []).filter((t) => !q || `${t.name} ${t.purpose} ${t.category}`.toLowerCase().includes(q));
  }, [status, query]);

  const install = async (ids: string[]) => {
    if (!ids.length) return;
    setBusy(true);
    try {
      if (ids.length === 1) await api.hostenvInstall(ids[0]);
      else {
        const result = await api.hostenvInstallBatch(ids);
        if (result.skipped.length) toast.warning(`${result.skipped.length} 项未发起安装`);
      }
      toast.success("安装任务已进入队列");
      setSelected(new Set());
      setRefreshKey((v) => v + 1);
    } catch (e) {
      toast.error(`安装失败：${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  const toggle = (tool: HostTool, checked: boolean) => {
    const next = new Set(selected);
    if (checked) next.add(tool.id);
    else next.delete(tool.id);
    setSelected(next);
  };

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">宿主环境</h1>
          <p className="text-muted-foreground text-sm">
            {status ? `${status.deploy_mode === "docker" ? "Agent 执行容器" : "当前运行主机"} · ${status.os}/${status.arch}` : "正在检测执行环境"}
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={load}>
          <RefreshCwIcon /> 重新体检
        </Button>
      </div>

      <section className="grid gap-3 border p-4 md:grid-cols-[1fr_auto]">
        <div className="grid gap-2">
          <Label htmlFor="install-proxy">安装代理</Label>
          <Input
            id="install-proxy"
            value={proxy}
            onChange={(e) => setProxy(e.target.value)}
            placeholder="http://127.0.0.1:7890，留空直连"
            className="font-mono text-xs"
          />
          <p className="text-muted-foreground text-xs">仅用于系统包、GitHub 和 Python 依赖安装，与目标流量代理独立。</p>
        </div>
        <div className="flex items-end gap-2">
          <Button variant="outline" onClick={() => api.hostenvProxyCheck(proxy).then((r) => r.ok ? toast.success(`代理可用${r.latency_ms ? ` · ${r.latency_ms}ms` : ""}`) : toast.error(r.error ?? `连接失败 ${r.status ?? ""}`)).catch((e) => toast.error((e as Error).message))}>
            测试
          </Button>
          <Button onClick={() => api.hostenvSaveProxy(proxy).then(() => toast.success("安装代理已保存")).catch((e) => toast.error((e as Error).message))}>
            保存代理
          </Button>
        </div>
      </section>

      <section className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="relative min-w-56 flex-1 sm:max-w-sm">
            <SearchIcon className="text-muted-foreground absolute left-2.5 top-2.5 size-4" />
            <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="搜索工具或用途" className="pl-8" />
          </div>
          <Button disabled={!selected.size || busy} onClick={() => install([...selected])}>
            {busy ? <LoaderCircleIcon className="animate-spin" /> : <DownloadIcon />}
            安装所选 {selected.size ? `(${selected.size})` : ""}
          </Button>
        </div>
        <div className="overflow-x-auto border">
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="hidden w-10 sm:table-cell" />
                <TableHead>工具</TableHead>
                <TableHead className="hidden md:table-cell">分类</TableHead>
                <TableHead>用途</TableHead>
                <TableHead className="w-24">状态</TableHead>
                <TableHead className="hidden w-20 text-right sm:table-cell">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tools.map((tool) => (
                <TableRow key={tool.id}>
                  <TableCell className="hidden sm:table-cell">
                    {!tool.available && tool.installable && (
                      <Checkbox checked={selected.has(tool.id)} onCheckedChange={(v) => toggle(tool, v === true)} aria-label={`选择 ${tool.name}`} />
                    )}
                  </TableCell>
                  <TableCell className="overflow-hidden">
                    <span className="font-mono text-sm font-medium">{tool.name}</span>
                    <span className="text-muted-foreground ml-2 text-xs">{tool.kind}</span>
                  </TableCell>
                  <TableCell className="hidden text-xs md:table-cell">{categories[tool.category] ?? tool.category}</TableCell>
                  <TableCell className="max-w-md text-sm">{tool.purpose}</TableCell>
                  <TableCell>
                    {tool.available ? <Badge><CheckIcon /> 可用</Badge> : <Badge variant="secondary">缺失</Badge>}
                    {tool.path && <p className="text-muted-foreground mt-1 hidden max-w-56 truncate font-mono text-[10px] md:block" title={tool.path}>{tool.path}</p>}
                    {!tool.available && tool.installable && <Button className="mt-1 sm:hidden" size="sm" variant="outline" onClick={() => install([tool.id])}>安装</Button>}
                  </TableCell>
                  <TableCell className="hidden text-right sm:table-cell">
                    {!tool.available && tool.installable && <Button size="sm" variant="outline" onClick={() => install([tool.id])}>安装</Button>}
                    {!tool.available && !tool.installable && <span className="text-muted-foreground text-xs" title={tool.install}>手工安装</span>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </section>

      <HostInstallJobs refreshKey={refreshKey} />
    </div>
  );
}
