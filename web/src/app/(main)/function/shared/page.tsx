"use client";

import * as React from "react";
import { LibraryBigIcon, PackagePlusIcon, PlusIcon, SearchIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { api } from "@/lib/api";
import type { SharedAsset } from "@/lib/types";
import { HostInstallJobs } from "@/components/host-install-jobs";
import { SharedPublishDialog } from "@/components/shared-publish-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

const kindLabel = { script: "脚本", tool: "工具", library: "语言库" };

export default function SharedPage() {
  const [assets, setAssets] = React.useState<SharedAsset[]>([]);
  const [packages, setPackages] = React.useState<{ name: string; version?: string }[]>([]);
  const [query, setQuery] = React.useState("");
  const [selected, setSelected] = React.useState<SharedAsset | null>(null);
  const [publishOpen, setPublishOpen] = React.useState(false);
  const [versionAsset, setVersionAsset] = React.useState<SharedAsset | null>(null);
  const [packageSpec, setPackageSpec] = React.useState("");
  const [refreshKey, setRefreshKey] = React.useState(0);

  const loadAssets = React.useCallback(() => {
    api.sharedAssets(query).then(setAssets).catch((e) => toast.error(`读取共享资产失败：${(e as Error).message}`));
  }, [query]);
  const loadPackages = React.useCallback(() => {
    api.sharedPackages().then((r) => setPackages(r.packages)).catch((e) => toast.error((e as Error).message));
  }, []);
  React.useEffect(() => {
    loadAssets();
  }, [loadAssets]);
  React.useEffect(loadPackages, [loadPackages]);

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

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">共享空间</h1>
          <p className="text-muted-foreground text-sm">跨任务复用经过审核的脚本、工具、语言库与 Python 依赖。</p>
        </div>
        <Button onClick={() => setPublishOpen(true)}><PlusIcon /> 发布资产</Button>
      </div>

      <Tabs defaultValue="assets">
        <TabsList>
          <TabsTrigger value="assets">资产库</TabsTrigger>
          <TabsTrigger value="packages">Python 依赖</TabsTrigger>
          <TabsTrigger value="jobs">变更记录</TabsTrigger>
        </TabsList>
        <TabsContent value="assets" className="flex flex-col gap-3">
          <div className="relative max-w-sm">
            <SearchIcon className="text-muted-foreground absolute left-2.5 top-2.5 size-4" />
            <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="搜索名称、用途或标签" className="pl-8" />
          </div>
          <div className="overflow-x-auto border">
            <Table className="table-fixed">
              <TableHeader><TableRow><TableHead className="w-[42%] sm:w-auto">名称</TableHead><TableHead className="hidden sm:table-cell">类型</TableHead><TableHead>用途</TableHead><TableHead className="hidden md:table-cell">版本</TableHead><TableHead className="hidden sm:table-cell">状态</TableHead><TableHead className="hidden lg:table-cell">来源</TableHead></TableRow></TableHeader>
              <TableBody>
                {assets.length === 0 && <TableRow><TableCell colSpan={6} className="text-muted-foreground py-12 text-center">暂无共享资产</TableCell></TableRow>}
                {assets.map((asset) => (
                  <TableRow key={asset.id} className="cursor-pointer" onClick={() => setSelected(asset)}>
                    <TableCell className="overflow-hidden">
                      <span className="block truncate font-mono text-sm font-medium" title={asset.name}>{asset.name}</span>
                      <div className="flex items-center gap-1"><p className="text-muted-foreground text-xs">{asset.language}</p><Badge className="sm:hidden" variant={asset.status === "published" ? "default" : "secondary"}>{asset.status === "published" ? "已发布" : "已停用"}</Badge></div>
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">{kindLabel[asset.kind]}</TableCell>
                    <TableCell className="min-w-0"><p className="line-clamp-2 break-words text-sm">{asset.description}</p><div className="mt-1 flex flex-wrap gap-1">{asset.tags.map((tag) => <Badge key={tag} variant="outline">{tag}</Badge>)}</div></TableCell>
                    <TableCell className="hidden font-mono text-xs md:table-cell">v{asset.current_version}</TableCell>
                    <TableCell className="hidden sm:table-cell"><Badge variant={asset.status === "published" ? "default" : "secondary"}>{asset.status === "published" ? "已发布" : "已停用"}</Badge></TableCell>
                    <TableCell className="text-muted-foreground hidden text-xs lg:table-cell">{asset.source_task_id ? `Task ${asset.source_task_id}` : "手工发布"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </TabsContent>

        <TabsContent value="packages" className="flex flex-col gap-4">
          <div className="grid max-w-2xl gap-2">
            <Label htmlFor="package-spec">安装 Python Package</Label>
            <div className="flex gap-2">
              <Input id="package-spec" value={packageSpec} onChange={(e) => setPackageSpec(e.target.value)} placeholder="ddddocr==1.5.6" className="font-mono" onKeyDown={(e) => e.key === "Enter" && packageSpec.trim() && installPackage()} />
              <Button onClick={installPackage} disabled={!packageSpec.trim()}><PackagePlusIcon /> 安装</Button>
            </div>
            <p className="text-muted-foreground text-xs">安装到共享虚拟环境，不修改系统 Python。变更串行执行，成功后保存完整依赖锁文件。</p>
          </div>
          <div className="max-w-4xl border">
            <Table>
              <TableHeader><TableRow><TableHead>Package</TableHead><TableHead>版本</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
              <TableBody>
                {packages.length === 0 && <TableRow><TableCell colSpan={3} className="text-muted-foreground py-10 text-center">共享环境尚未初始化或尚无依赖</TableCell></TableRow>}
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
        <TabsContent value="jobs"><HostInstallJobs refreshKey={refreshKey} onSettled={loadPackages} /></TabsContent>
      </Tabs>

      <SharedPublishDialog open={publishOpen} onOpenChange={setPublishOpen} sourcePath="" onPublished={loadAssets} />
      <SharedPublishDialog open={Boolean(versionAsset)} onOpenChange={(open) => !open && setVersionAsset(null)} asset={versionAsset} onPublished={loadAssets} />
      <Sheet open={Boolean(selected)} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent className="overflow-y-auto sm:max-w-xl">
          {selected && <>
            <SheetHeader>
              <SheetTitle className="flex items-center gap-2 font-mono"><LibraryBigIcon />{selected.name}<Badge>v{selected.current_version}</Badge></SheetTitle>
              <SheetDescription>{kindLabel[selected.kind]} · {selected.language || "通用"}</SheetDescription>
            </SheetHeader>
            <div className="flex flex-col gap-5 p-4 text-sm">
              <section><h3 className="mb-2 font-medium">用途</h3><p className="whitespace-pre-wrap text-muted-foreground">{selected.description}</p></section>
              <section><h3 className="mb-2 font-medium">入口</h3><code className="block break-all border bg-muted p-2 text-xs">{selected.entrypoint}</code></section>
              <section><h3 className="mb-2 font-medium">调用示例</h3><pre className="whitespace-pre-wrap break-words border bg-muted p-3 font-mono text-xs">{selected.usage || "未填写"}</pre></section>
              <section><h3 className="mb-2 font-medium">依赖</h3><div className="flex flex-wrap gap-1">{selected.dependencies.length ? selected.dependencies.map((d) => <Badge key={d} variant="outline">{d}</Badge>) : <span className="text-muted-foreground">无声明依赖</span>}</div></section>
              <section className="text-muted-foreground text-xs">来源：{selected.source_task_id ? `Task ${selected.source_task_id}${selected.source_intent_id ? ` / Intent ${selected.source_intent_id}` : ""}` : "手工发布"}<br />更新：{new Date(selected.updated_at).toLocaleString("zh-CN")}</section>
              <div className="flex flex-wrap gap-2 border-t pt-4">
                <Button variant="outline" onClick={() => { setVersionAsset(selected); setSelected(null); }}>发布新版本</Button>
                <Button variant={selected.status === "published" ? "destructive" : "default"} onClick={() => api.sharedStatus(selected.id, selected.status === "published" ? "disabled" : "published").then(() => { setSelected(null); loadAssets(); }).catch((e) => toast.error((e as Error).message))}>{selected.status === "published" ? "停用" : "重新启用"}</Button>
              </div>
            </div>
          </>}
        </SheetContent>
      </Sheet>
    </div>
  );
}
