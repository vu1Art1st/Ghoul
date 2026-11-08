"use client";

import * as React from "react";
import { Database, Loader2, RefreshCw, Save, Search, Square, TriangleAlert } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import type { VulnIntelHit, VulnIntelStatus } from "@/lib/types";

function fmtTime(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export default function VulnIntelPage() {
  const [status, setStatus] = React.useState<VulnIntelStatus | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);
  const [syncing, setSyncing] = React.useState(false);
  const [searching, setSearching] = React.useState(false);
  const [proxy, setProxy] = React.useState("");
  const [nvdKey, setNvdKey] = React.useState("");
  const [githubToken, setGithubToken] = React.useState("");
  const [pocEnabled, setPocEnabled] = React.useState(false);
  const [backfillDays, setBackfillDays] = React.useState(0);
  const [maxRuntime, setMaxRuntime] = React.useState(30);
  const [product, setProduct] = React.useState("");
  const [vendor, setVendor] = React.useState("");
  const [version, setVersion] = React.useState("");
  const [hits, setHits] = React.useState<VulnIntelHit[]>([]);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const data = await api.vulnintelStatus();
      setStatus(data);
      setProxy(data.config?.proxy ?? "");
      setPocEnabled(Boolean(data.config?.poc_enabled));
      setBackfillDays(data.config?.backfill_days ?? 0);
      setMaxRuntime(data.config?.max_runtime_minutes ?? 30);
      setNvdKey("");
      setGithubToken("");
    } catch (error) {
      toast.error(`加载漏洞情报失败: ${error}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  React.useEffect(() => {
    if (!status?.sync_running) return;
    const timer = window.setInterval(() => void load(), 4000);
    return () => window.clearInterval(timer);
  }, [load, status?.sync_running]);

  async function saveConfig() {
    setSaving(true);
    try {
      await api.vulnintelSaveConfig({
        enabled: status?.enabled,
        proxy,
        poc_enabled: pocEnabled,
        backfill_days: backfillDays,
        max_runtime_minutes: maxRuntime,
        ...(nvdKey.trim() ? { nvd_api_key: nvdKey.trim() } : {}),
        ...(githubToken.trim() ? { github_token: githubToken.trim() } : {}),
      });
      toast.success("配置已保存");
      await load();
    } catch (error) {
      toast.error(`保存失败: ${error}`);
    } finally {
      setSaving(false);
    }
  }

  async function toggleEnabled(enabled: boolean) {
    try {
      await api.vulnintelSaveConfig({ enabled });
      await load();
    } catch (error) {
      toast.error(`切换失败: ${error}`);
    }
  }

  async function syncNow() {
    setSyncing(true);
    try {
      await api.vulnintelSyncNow();
      toast.success("同步已启动");
      await load();
    } catch (error) {
      toast.error(`启动失败: ${error}`);
    } finally {
      setSyncing(false);
    }
  }

  async function cancelSync() {
    try {
      await api.vulnintelSyncCancel();
      toast.success("已请求中断");
      await load();
    } catch (error) {
      toast.error(`中断失败: ${error}`);
    }
  }

  async function search() {
    if (!product.trim()) {
      toast.error("请输入组件名");
      return;
    }
    setSearching(true);
    try {
      const result = await api.vulnintelSearch({
        product: product.trim(),
        vendor: vendor.trim() || undefined,
        version: version.trim() || undefined,
        limit: 30,
      });
      setHits(result.items);
    } catch (error) {
      toast.error(`检索失败: ${error}`);
    } finally {
      setSearching(false);
    }
  }

  const counts = status?.counts;

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Database className="size-5" />
          <h1 className="text-lg font-semibold">漏洞情报</h1>
          {status ? <Badge variant={status.enabled ? "default" : "secondary"}>{status.enabled ? "已启用" : "已停用"}</Badge> : null}
          {status?.sync_running ? <Badge variant="outline">{status.sync_phase || "同步中"}</Badge> : null}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
            <RefreshCw className={loading ? "animate-spin" : ""} />
            刷新
          </Button>
          <Button size="sm" onClick={() => void syncNow()} disabled={syncing || status?.sync_running}>
            <RefreshCw className={syncing ? "animate-spin" : ""} />
            立即同步
          </Button>
          <Button variant="outline" size="sm" onClick={() => void cancelSync()} disabled={!status?.sync_running}>
            <Square />
            中断
          </Button>
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-[320px_minmax(0,1fr)]">
        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">同步配置</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex items-center justify-between gap-3">
                <Label htmlFor="vulnintel-enabled">启用定时同步</Label>
                <Switch id="vulnintel-enabled" checked={status?.enabled ?? false} onCheckedChange={(value) => void toggleEnabled(value)} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="vulnintel-proxy">出网代理</Label>
                <Input id="vulnintel-proxy" value={proxy} onChange={(event) => setProxy(event.target.value)} placeholder="留空跟随全局代理" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="vulnintel-nvd">NVD API Key</Label>
                <Input id="vulnintel-nvd" type="password" value={nvdKey} onChange={(event) => setNvdKey(event.target.value)} placeholder={status?.config?.nvd_key_set ? "已配置，留空保持不变" : "可选"} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="vulnintel-github">GitHub Token</Label>
                <Input id="vulnintel-github" type="password" value={githubToken} onChange={(event) => setGithubToken(event.target.value)} placeholder={status?.config?.github_token_set ? "已配置，留空保持不变" : "可选"} />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label htmlFor="vulnintel-backfill">回补天数</Label>
                  <Input id="vulnintel-backfill" type="number" min={0} value={backfillDays} onChange={(event) => setBackfillDays(Number(event.target.value))} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="vulnintel-runtime">单轮分钟</Label>
                  <Input id="vulnintel-runtime" type="number" min={5} value={maxRuntime} onChange={(event) => setMaxRuntime(Number(event.target.value))} />
                </div>
              </div>
              <div className="flex items-center justify-between gap-3">
                <Label htmlFor="vulnintel-poc">聚合公开 PoC</Label>
                <Switch id="vulnintel-poc" checked={pocEnabled} onCheckedChange={setPocEnabled} />
              </div>
              <Button className="w-full" onClick={() => void saveConfig()} disabled={saving}>
                {saving ? <Loader2 className="animate-spin" /> : <Save />}
                保存配置
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">数据状态</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              <div className="grid grid-cols-3 gap-2">
                <div>
                  <div className="text-muted-foreground text-xs">CVE</div>
                  <div className="font-semibold tabular-nums">{counts?.cves ?? 0}</div>
                </div>
                <div>
                  <div className="text-muted-foreground text-xs">KEV</div>
                  <div className="font-semibold tabular-nums">{counts?.kev ?? 0}</div>
                </div>
                <div>
                  <div className="text-muted-foreground text-xs">PoC</div>
                  <div className="font-semibold tabular-nums">{counts?.pocs ?? 0}</div>
                </div>
              </div>
              <div className="border-t pt-3">
                <div className="text-muted-foreground text-xs">nuclei 模板</div>
                <div className="mt-1 flex items-center justify-between gap-2">
                  <span>{status?.nuclei?.templates ?? 0} 个</span>
                  {status?.nuclei?.exists ? <Badge variant="secondary">就绪</Badge> : <Badge variant="outline">未拉取</Badge>}
                </div>
              </div>
              {(status?.sources ?? []).map((source) => (
                <div key={source.source} className="border-t pt-3">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-medium">{source.source}</span>
                    {source.last_error ? (
                      <Badge variant="destructive">
                        <TriangleAlert />
                        异常
                      </Badge>
                    ) : (
                      <Badge variant="secondary">正常</Badge>
                    )}
                  </div>
                  <div className="text-muted-foreground mt-1 text-xs">最近同步 {fmtTime(source.last_sync_at)}</div>
                  {source.last_error ? <div className="mt-1 break-words text-xs text-red-500">{source.last_error}</div> : null}
                </div>
              ))}
            </CardContent>
          </Card>
        </div>

        <Card className="min-w-0">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">组件漏洞检索</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid gap-3 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_minmax(0,0.8fr)_auto]">
              <div className="space-y-1.5">
                <Label htmlFor="vulnintel-product">组件</Label>
                <Input id="vulnintel-product" value={product} onChange={(event) => setProduct(event.target.value)} placeholder="nginx / tomcat / thinkphp" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="vulnintel-vendor">厂商</Label>
                <Input id="vulnintel-vendor" value={vendor} onChange={(event) => setVendor(event.target.value)} placeholder="apache" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="vulnintel-version">版本</Label>
                <Input id="vulnintel-version" value={version} onChange={(event) => setVersion(event.target.value)} placeholder="2.4.49" />
              </div>
              <div className="flex items-end">
                <Button onClick={() => void search()} disabled={searching}>
                  {searching ? <Loader2 className="animate-spin" /> : <Search />}
                  检索
                </Button>
              </div>
            </div>

            <div className="overflow-x-auto rounded-md border">
              <table className="w-full min-w-[760px] text-sm">
                <thead className="bg-muted/60 text-muted-foreground">
                  <tr>
                    <th className="px-3 py-2 text-left font-medium">CVE</th>
                    <th className="px-3 py-2 text-left font-medium">等级</th>
                    <th className="px-3 py-2 text-left font-medium">组件</th>
                    <th className="px-3 py-2 text-left font-medium">版本</th>
                    <th className="px-3 py-2 text-left font-medium">来源</th>
                  </tr>
                </thead>
                <tbody>
                  {hits.length === 0 ? (
                    <tr>
                      <td className="text-muted-foreground px-3 py-8 text-center" colSpan={5}>
                        暂无结果
                      </td>
                    </tr>
                  ) : (
                    hits.map((hit) => (
                      <tr key={hit.cve_id} className="border-t">
                        <td className="px-3 py-2">
                          <div className="font-medium">{hit.cve_id}</div>
                          <div className="text-muted-foreground max-w-[360px] truncate text-xs">{hit.title}</div>
                        </td>
                        <td className="px-3 py-2">
                          <div className="flex flex-wrap gap-1">
                            <Badge variant={hit.severity === "CRITICAL" ? "destructive" : "outline"}>{hit.severity}</Badge>
                            {hit.kev ? <Badge>KEV</Badge> : null}
                          </div>
                        </td>
                        <td className="px-3 py-2">{hit.matched?.join(", ") || "—"}</td>
                        <td className="px-3 py-2">
                          {hit.version_in_range === true ? "受影响" : hit.version_in_range === false ? "不在区间" : "待判定"}
                        </td>
                        <td className="px-3 py-2">{hit.source || "nvd"}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
