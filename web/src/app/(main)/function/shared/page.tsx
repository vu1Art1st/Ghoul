"use client";

import * as React from "react";
import {
  ArrowRightIcon,
  PencilIcon,
  PackagePlusIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  TerminalIcon,
  Trash2Icon,
} from "lucide-react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import { api } from "@/lib/api";
import type { RuntimeDoctor, SharedAsset } from "@/lib/types";
import { HostInstallJobs } from "@/components/host-install-jobs";
import { SharedAssetEditDialog } from "@/components/shared-asset-edit-dialog";
import { SharedPublishDialog } from "@/components/shared-publish-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

const kindLabel = {
  script: "脚本",
  tool: "工具",
  library: "语言库",
  skill: "Skill",
  template: "模板",
  ctf_report: "CTF 报告",
};

export default function SharedPage() {
  const router = useRouter();
  const [assets, setAssets] = React.useState<SharedAsset[]>([]);
  const [packages, setPackages] = React.useState<{ name: string; version?: string }[]>([]);
  const [query, setQuery] = React.useState("");
  const [editAsset, setEditAsset] = React.useState<SharedAsset | null>(null);
  const [publishOpen, setPublishOpen] = React.useState(false);
  const [versionAsset, setVersionAsset] = React.useState<SharedAsset | null>(null);
  const [packageSpec, setPackageSpec] = React.useState("");
  const [refreshKey, setRefreshKey] = React.useState(0);
  const [runtimeStatus, setRuntimeStatus] = React.useState<RuntimeDoctor | null>(null);

  const loadAssets = React.useCallback(() => {
    api.sharedAssets(query).then(setAssets).catch((e) => toast.error(`读取共享资产失败：${(e as Error).message}`));
  }, [query]);
  const loadPackages = React.useCallback(() => {
    api.sharedPackages().then((r) => setPackages(r.packages)).catch((e) => toast.error((e as Error).message));
  }, []);
  const loadRuntime = React.useCallback(() => {
    api.sharedRuntimeDoctor().then(setRuntimeStatus).catch((e) => toast.error((e as Error).message));
  }, []);
  React.useEffect(() => {
    loadAssets();
  }, [loadAssets]);
  React.useEffect(loadPackages, [loadPackages]);
  React.useEffect(loadRuntime, [loadRuntime]);

  const installPackage = async () => {
    try {
      await api.sharedPackageInstall(packageSpec.trim());
      toast.success("依赖安装已进入队列");
      setPackageSpec("");
      setRefreshKey((v) => v + 1);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  const installSystemPackage = async () => {
    try {
      await api.sharedPackageInstallSystem(packageSpec.trim());
      toast.warning("系统 Python 回退安装已进入队列；该依赖在容器重建后可能丢失");
      setPackageSpec("");
      setRefreshKey((v) => v + 1);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="font-semibold text-lg">共享空间</h1>
          <p className="text-muted-foreground text-sm">跨任务复用经过审核的脚本、工具、语言库与 Python 依赖。</p>
        </div>
        <Button onClick={() => setPublishOpen(true)}><PlusIcon /> 发布资产</Button>
      </div>

      <Tabs defaultValue="assets">
        <TabsList>
          <TabsTrigger value="assets">资产库</TabsTrigger>
          <TabsTrigger value="packages">Python 依赖</TabsTrigger>
          <TabsTrigger value="runtime">运行时</TabsTrigger>
          <TabsTrigger value="jobs">变更记录</TabsTrigger>
        </TabsList>
        <TabsContent value="assets" className="flex flex-col gap-3">
          <div className="relative max-w-sm">
            <SearchIcon className="absolute top-2.5 left-2.5 size-4 text-muted-foreground" />
            <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="搜索名称、用途或标签" className="pl-8" />
          </div>
          <div className="overflow-x-auto border">
            <Table className="table-fixed">
              <TableHeader><TableRow><TableHead className="w-[34%] sm:w-auto">资产</TableHead><TableHead className="hidden sm:table-cell">类型</TableHead><TableHead>用途</TableHead><TableHead className="hidden md:table-cell">版本</TableHead><TableHead className="hidden sm:table-cell">状态</TableHead><TableHead className="hidden lg:table-cell">来源</TableHead><TableHead className="w-28 text-right">操作</TableHead></TableRow></TableHeader>
              <TableBody>
                {assets.length === 0 && <TableRow><TableCell colSpan={7} className="py-12 text-center text-muted-foreground">暂无共享资产</TableCell></TableRow>}
                {assets.map((asset) => (
                  <TableRow key={asset.id} className="cursor-pointer" onClick={() => router.push(`/function/shared/detail?id=${asset.id}`)}>
                    <TableCell className="overflow-hidden">
                      <span className="block truncate font-medium text-sm" title={asset.display_name}>{asset.display_name || asset.name}</span>
                      <div className="flex min-w-0 items-center gap-1">
                        <span className="truncate font-mono text-muted-foreground text-xs" title={asset.name}>{asset.name}</span>
                        {asset.language && <span className="shrink-0 text-muted-foreground text-xs">· {asset.language}</span>}
                        <Badge className="ml-1 shrink-0 sm:hidden" variant={asset.status === "published" ? "default" : "secondary"}>{asset.status === "published" ? "已发布" : "已停用"}</Badge>
                      </div>
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">{kindLabel[asset.kind]}</TableCell>
                    <TableCell className="min-w-0"><p className="line-clamp-2 break-words text-sm">{asset.description}</p><div className="mt-1 flex flex-wrap gap-1">{asset.tags.map((tag) => <Badge key={tag} variant="outline">{tag}</Badge>)}</div></TableCell>
                    <TableCell className="hidden font-mono text-xs md:table-cell">v{asset.current_version}</TableCell>
                    <TableCell className="hidden sm:table-cell"><Badge variant={asset.status === "published" ? "default" : "secondary"}>{asset.status === "published" ? "已发布" : "已停用"}</Badge></TableCell>
                    <TableCell className="hidden text-muted-foreground text-xs lg:table-cell">{asset.source_task_id ? `Task ${asset.source_task_id}` : "手工发布"}</TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8"
                          title="编辑资料"
                          onClick={(event) => {
                            event.stopPropagation();
                            setEditAsset(asset);
                          }}
                        >
                          <PencilIcon />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8"
                          title="查看详情"
                          onClick={(event) => {
                            event.stopPropagation();
                            router.push(`/function/shared/detail?id=${asset.id}`);
                          }}
                        >
                          <ArrowRightIcon />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </TabsContent>

        <TabsContent value="packages" className="flex flex-col gap-4">
          <div className="grid max-w-2xl gap-2">
            <Label htmlFor="package-spec">安装 Python Package</Label>
            <div className="flex flex-wrap gap-2">
              <Input id="package-spec" value={packageSpec} onChange={(e) => setPackageSpec(e.target.value)} placeholder="ddddocr==1.5.6" className="font-mono" onKeyDown={(e) => e.key === "Enter" && packageSpec.trim() && installPackage()} />
              <Button onClick={installPackage} disabled={!packageSpec.trim()}><PackagePlusIcon /> 安装</Button>
              <Button
                variant="outline"
                onClick={installSystemPackage}
                disabled={!packageSpec.trim()}
                title="仅在该包向共享 uv 环境安装失败后可用"
              >
                <TerminalIcon /> 系统 Python 回退
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              优先使用 uv 安装到共享虚拟环境。只有同一包规格的共享安装任务失败后，系统 Python
              回退才会被允许；回退安装带有 ephemeral 审计标记。
            </p>
          </div>
          <div className="max-w-4xl border">
            <Table>
              <TableHeader><TableRow><TableHead>Package</TableHead><TableHead>版本</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
              <TableBody>
                {packages.length === 0 && <TableRow><TableCell colSpan={3} className="py-10 text-center text-muted-foreground">共享环境尚未初始化或尚无依赖</TableCell></TableRow>}
                {packages.map((pkg) => (
                  <TableRow key={pkg.name}>
                    <TableCell className="font-mono text-sm">{pkg.name}</TableCell>
                    <TableCell className="font-mono text-xs">{pkg.version ?? "未锁定"}</TableCell>
                    <TableCell className="text-right"><Button variant="ghost" size="icon" title={`卸载 ${pkg.name}`} onClick={() => api.sharedPackageRemove(pkg.name).then(() => { toast.success("卸载任务已进入队列"); setRefreshKey((v) => v + 1); }).catch((e) => toast.error((e as Error).message))}><Trash2Icon /></Button></TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </TabsContent>
        <TabsContent value="runtime" className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-muted-foreground text-sm">uv 负责 Python 与包，mise 负责 Java、Node.js、Go；共享运行时不可用时允许系统回退。</p>
            <Button variant="outline" onClick={loadRuntime}><RefreshCwIcon /> 刷新</Button>
          </div>
          {runtimeStatus ? (
            <div className="flex flex-col gap-4">
              {runtimeStatus.task_runtime?.warning && (
                <div className="border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-amber-800 text-sm dark:text-amber-200">
                  {runtimeStatus.task_runtime.warning}
                </div>
              )}
              <dl className="grid gap-x-6 gap-y-3 border-y py-4 text-sm sm:grid-cols-2 lg:grid-cols-3">
                <div><dt className="text-muted-foreground text-xs">Python 来源</dt><dd className="mt-1"><Badge variant={runtimeStatus.task_runtime?.python_source === "shared" ? "default" : "secondary"}>{runtimeStatus.task_runtime?.python_source || "unknown"}</Badge></dd></div>
                <div><dt className="text-muted-foreground text-xs">共享 venv</dt><dd className="mt-1 break-all font-mono text-xs">{runtimeStatus.shared_python.available ? "可用" : "不可用"}</dd></div>
                <div><dt className="text-muted-foreground text-xs">实际解释器</dt><dd className="mt-1 break-all font-mono text-xs">{runtimeStatus.task_runtime?.python || "-"}</dd></div>
                <div><dt className="text-muted-foreground text-xs">uv</dt><dd className="mt-1 break-all font-mono text-xs">{runtimeStatus.shared_python.uv || "-"}</dd></div>
                <div><dt className="text-muted-foreground text-xs">mise</dt><dd className="mt-1 break-all font-mono text-xs">{runtimeStatus.shared_python.mise || "-"}</dd></div>
                <div><dt className="text-muted-foreground text-xs">系统 Python</dt><dd className="mt-1 break-all font-mono text-xs">{runtimeStatus.shared_python.system_python || "-"}</dd></div>
              </dl>
              <details>
                <summary className="cursor-pointer text-muted-foreground text-sm">原始诊断信息</summary>
                <pre className="mt-2 max-h-[480px] overflow-auto border bg-muted p-4 font-mono text-xs">
                  {JSON.stringify(runtimeStatus, null, 2)}
                </pre>
              </details>
            </div>
          ) : (
            <p className="py-8 text-muted-foreground text-sm">读取中…</p>
          )}
        </TabsContent>
        <TabsContent value="jobs"><HostInstallJobs refreshKey={refreshKey} onSettled={loadPackages} /></TabsContent>
      </Tabs>

      <SharedPublishDialog open={publishOpen} onOpenChange={setPublishOpen} sourcePath="" onPublished={loadAssets} />
      <SharedPublishDialog open={Boolean(versionAsset)} onOpenChange={(open) => !open && setVersionAsset(null)} asset={versionAsset} onPublished={loadAssets} />
      <SharedAssetEditDialog
        asset={editAsset}
        open={Boolean(editAsset)}
        onOpenChange={(open) => !open && setEditAsset(null)}
        onSaved={() => {
          setEditAsset(null);
          loadAssets();
        }}
      />
    </div>
  );
}
