"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  ArrowLeftIcon,
  CheckIcon,
  ClipboardIcon,
  DownloadIcon,
  ExternalLinkIcon,
  FileIcon,
  FolderIcon,
  PencilIcon,
  PlusIcon,
  PowerIcon,
  RefreshCwIcon,
} from "lucide-react";
import { toast } from "sonner";

import { SharedAssetEditDialog } from "@/components/shared-asset-edit-dialog";
import { SharedPublishDialog } from "@/components/shared-publish-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Separator } from "@/components/ui/separator";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type {
  SharedAsset,
  SharedAssetEvent,
  SharedAssetFile,
  SharedAssetFileEntry,
  SharedAssetVersion,
} from "@/lib/types";

const kindLabel: Record<SharedAsset["kind"], string> = {
  script: "脚本",
  tool: "工具",
  library: "语言库",
  skill: "Skill",
  template: "模板",
  ctf_report: "CTF 报告",
};

const eventLabel: Record<string, string> = {
  update_metadata: "更新资产资料",
  publish_version: "发布新版本",
  status: "变更资产状态",
};

function fmtSize(size: number): string {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

function fmtTime(value: string): string {
  return new Date(value).toLocaleString("zh-CN");
}

function statusLabel(status: SharedAsset["status"]): string {
  if (status === "published") return "已发布";
  if (status === "disabled") return "已停用";
  if (status === "archived") return "已归档";
  return "待审核";
}

function SharedAssetDetail() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const assetID = Number(searchParams.get("id") ?? 0);
  const [asset, setAsset] = React.useState<SharedAsset | null>(null);
  const [versions, setVersions] = React.useState<SharedAssetVersion[]>([]);
  const [events, setEvents] = React.useState<SharedAssetEvent[]>([]);
  const [selectedVersion, setSelectedVersion] = React.useState(0);
  const [path, setPath] = React.useState("");
  const [entries, setEntries] = React.useState<SharedAssetFileEntry[]>([]);
  const [preview, setPreview] = React.useState<SharedAssetFile | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [filesLoading, setFilesLoading] = React.useState(false);
  const [previewLoading, setPreviewLoading] = React.useState(false);
  const [tab, setTab] = React.useState("overview");
  const [editOpen, setEditOpen] = React.useState(false);
  const [publishOpen, setPublishOpen] = React.useState(false);

  const loadDetail = React.useCallback(async () => {
    if (!Number.isSafeInteger(assetID) || assetID <= 0) {
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const [nextAsset, nextVersions, nextEvents] = await Promise.all([
        api.sharedAsset(assetID),
        api.sharedVersions(assetID),
        api.sharedEvents(assetID),
      ]);
      setAsset(nextAsset);
      setVersions(nextVersions);
      setEvents(nextEvents);
      setSelectedVersion(nextAsset.current_version);
    } catch (error) {
      toast.error(`读取资产详情失败：${(error as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, [assetID]);

  React.useEffect(() => {
    void loadDetail();
  }, [loadDetail]);

  const loadFiles = React.useCallback(
    async (version: number, nextPath: string) => {
      if (!assetID || !version) return;
      setFilesLoading(true);
      try {
        const result = await api.sharedFiles(assetID, version, nextPath);
        setPath(result.path);
        setEntries(result.entries);
      } catch (error) {
        toast.error(`读取版本文件失败：${(error as Error).message}`);
      } finally {
        setFilesLoading(false);
      }
    },
    [assetID],
  );

  React.useEffect(() => {
    if (!selectedVersion) return;
    setPath("");
    setEntries([]);
    setPreview(null);
    void loadFiles(selectedVersion, "");
  }, [loadFiles, selectedVersion]);

  const openFile = async (entry: SharedAssetFileEntry) => {
    if (entry.dir) {
      setPreview(null);
      await loadFiles(selectedVersion, entry.path);
      return;
    }
    setPreviewLoading(true);
    try {
      setPreview(await api.sharedFile(assetID, selectedVersion, entry.path));
    } catch (error) {
      toast.error(`读取文件失败：${(error as Error).message}`);
    } finally {
      setPreviewLoading(false);
    }
  };

  const toggleStatus = async () => {
    if (!asset) return;
    const next = asset.status === "published" ? "disabled" : "published";
    try {
      await api.sharedStatus(asset.id, next);
      toast.success(next === "published" ? "资产已重新启用" : "资产已停用");
      await loadDetail();
    } catch (error) {
      toast.error(`状态更新失败：${(error as Error).message}`);
    }
  };

  const copyPath = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      toast.success("路径已复制");
    } catch {
      toast.error("复制失败");
    }
  };

  if (loading) {
    return <p className="py-16 text-center text-muted-foreground text-sm">正在读取共享资产…</p>;
  }
  if (!asset) {
    return (
      <div className="flex flex-col items-center gap-3 py-16">
        <p className="text-muted-foreground text-sm">共享资产不存在或已被删除。</p>
        <Button variant="outline" onClick={() => router.push("/function/shared")}>返回资产库</Button>
      </div>
    );
  }

  const activeVersion = versions.find((item) => item.version === selectedVersion) ?? versions[0];
  const isPublished = asset.status === "published";
  const sourcePath = activeVersion?.source_path ?? "";
  const sourceDir = sourcePath.includes("/") ? sourcePath.slice(0, sourcePath.lastIndexOf("/")) : "";
  const breadcrumbs = path ? path.split("/") : [];

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <Button variant="ghost" size="sm" className="-ml-2 mb-1" onClick={() => router.push("/function/shared")}>
            <ArrowLeftIcon /> 资产库
          </Button>
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="break-words font-semibold text-xl">{asset.display_name || asset.name}</h1>
            <Badge variant="outline">v{asset.current_version}</Badge>
            <Badge variant={isPublished ? "default" : "secondary"}>{statusLabel(asset.status)}</Badge>
          </div>
          <p className="mt-1 break-all font-mono text-muted-foreground text-xs">{asset.name}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" onClick={() => setEditOpen(true)}>
            <PencilIcon /> 编辑资料
          </Button>
          <Button variant="outline" onClick={() => setPublishOpen(true)}>
            <PlusIcon /> 发布新版本
          </Button>
          <Button variant={isPublished ? "destructive" : "default"} onClick={toggleStatus}>
            <PowerIcon /> {isPublished ? "停用" : "重新启用"}
          </Button>
        </div>
      </div>

      <Separator />

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="overview">概览</TabsTrigger>
          <TabsTrigger value="files">文件</TabsTrigger>
          <TabsTrigger value="versions">版本</TabsTrigger>
          <TabsTrigger value="events">变更记录</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="pt-4">
          <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_320px]">
            <div className="flex min-w-0 flex-col gap-5">
              <section>
                <h2 className="mb-2 font-medium text-sm">用途说明</h2>
                <p className="whitespace-pre-wrap text-muted-foreground text-sm">{asset.description || "未填写"}</p>
              </section>
              <section>
                <h2 className="mb-2 font-medium text-sm">调用示例</h2>
                <pre className="overflow-x-auto whitespace-pre-wrap break-words border bg-muted p-3 font-mono text-xs">{asset.usage || "未填写"}</pre>
              </section>
              <section>
                <h2 className="mb-2 font-medium text-sm">依赖</h2>
                <div className="flex flex-wrap gap-1">
                  {asset.dependencies.length
                    ? asset.dependencies.map((dependency) => <Badge key={dependency} variant="outline">{dependency}</Badge>)
                    : <span className="text-muted-foreground text-sm">无声明依赖</span>}
                </div>
              </section>
            </div>
            <aside className="flex min-w-0 flex-col gap-4 border-t pt-4 lg:border-t-0 lg:border-l lg:pt-0 lg:pl-5">
              <dl className="grid gap-3 text-sm">
                <div><dt className="text-muted-foreground text-xs">类型</dt><dd className="mt-1">{kindLabel[asset.kind]}</dd></div>
                <div><dt className="text-muted-foreground text-xs">语言</dt><dd className="mt-1">{asset.language || "通用"}</dd></div>
                <div><dt className="text-muted-foreground text-xs">入口文件</dt><dd className="mt-1 break-all font-mono text-xs">{asset.entrypoint}</dd></div>
                <div><dt className="text-muted-foreground text-xs">运行时</dt><dd className="mt-1 break-words">{asset.runtime_spec || "跟随共享运行时"}</dd></div>
                <div><dt className="text-muted-foreground text-xs">更新时间</dt><dd className="mt-1">{fmtTime(asset.updated_at)}</dd></div>
              </dl>
              <div>
                <p className="mb-1 text-muted-foreground text-xs">标签</p>
                <div className="flex flex-wrap gap-1">
                  {asset.tags.length ? asset.tags.map((tag) => <Badge key={tag} variant="outline">{tag}</Badge>) : <span className="text-muted-foreground text-sm">无标签</span>}
                </div>
              </div>
              <div className="border-t pt-4">
                <p className="mb-2 text-muted-foreground text-xs">来源</p>
                {asset.source_task_id ? (
                  <Link href={`/function/tasks/detail?id=${asset.source_task_id}`} className="inline-flex items-center gap-1 text-sm hover:underline">
                    Task {asset.source_task_id} <ExternalLinkIcon className="size-3.5" />
                  </Link>
                ) : (
                  <p className="text-sm">手工发布</p>
                )}
                {sourcePath && (
                  <div className="mt-2 flex min-w-0 items-center gap-1">
                    {sourceDir && (
                      <Link
                        href={`/function/workspace?path=${encodeURIComponent(sourceDir)}`}
                        className="truncate font-mono text-muted-foreground text-xs hover:underline"
                        title={sourcePath}
                      >
                        {sourcePath}
                      </Link>
                    )}
                    <Button variant="ghost" size="icon" className="size-7 shrink-0" title="复制来源路径" onClick={() => copyPath(sourcePath)}>
                      <ClipboardIcon />
                    </Button>
                  </div>
                )}
              </div>
            </aside>
          </div>
        </TabsContent>

        <TabsContent value="files" className="flex flex-col gap-3 pt-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              <NativeSelect value={String(selectedVersion)} onChange={(event) => setSelectedVersion(Number(event.target.value))}>
                {versions.map((version) => <NativeSelectOption key={version.id} value={version.version}>v{version.version}</NativeSelectOption>)}
              </NativeSelect>
              <Button variant="ghost" size="icon" className="size-8" title="刷新文件" onClick={() => loadFiles(selectedVersion, path)}>
                <RefreshCwIcon className={filesLoading ? "animate-spin" : ""} />
              </Button>
            </div>
            {activeVersion && <p className="text-muted-foreground text-xs">哈希 {activeVersion.content_hash.slice(0, 12)}…</p>}
          </div>
          <div className="flex flex-wrap items-center gap-1 text-sm">
            <button type="button" className="rounded px-1.5 py-0.5 hover:bg-accent" onClick={() => { setPreview(null); void loadFiles(selectedVersion, ""); }}>版本根目录</button>
            {breadcrumbs.map((part, index) => {
              const nextPath = breadcrumbs.slice(0, index + 1).join("/");
              return (
                <React.Fragment key={nextPath}>
                  <span className="text-muted-foreground">/</span>
                  <button type="button" className="rounded px-1.5 py-0.5 hover:bg-accent" onClick={() => { setPreview(null); void loadFiles(selectedVersion, nextPath); }}>{part}</button>
                </React.Fragment>
              );
            })}
          </div>
          <div className="grid min-h-[420px] gap-3 xl:grid-cols-[minmax(300px,42%)_minmax(0,1fr)]">
            <div className="overflow-hidden border">
              <Table>
                <TableHeader><TableRow><TableHead>名称</TableHead><TableHead className="w-24 text-right">大小</TableHead></TableRow></TableHeader>
                <TableBody>
                  {entries.length === 0 && <TableRow><TableCell colSpan={2} className="py-10 text-center text-muted-foreground">{filesLoading ? "加载中…" : "空目录"}</TableCell></TableRow>}
                  {entries.map((entry) => (
                    <TableRow key={entry.path} className="cursor-pointer" onClick={() => openFile(entry)}>
                      <TableCell>
                        <div className="flex min-w-0 items-center gap-2">
                          {entry.dir ? <FolderIcon className="size-4 shrink-0 text-blue-500" /> : <FileIcon className="size-4 shrink-0 text-muted-foreground" />}
                          <span className="truncate font-mono text-xs" title={entry.path}>{entry.name}</span>
                        </div>
                      </TableCell>
                      <TableCell className="text-right font-mono text-muted-foreground text-xs">{entry.dir ? "—" : fmtSize(entry.size)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className="flex min-h-[320px] min-w-0 flex-col border">
              <div className="flex min-h-10 items-center justify-between gap-2 border-b px-3 py-2">
                <span className="truncate font-mono text-xs">{preview?.path || "选择文件预览"}</span>
                {preview && (
                  <Button variant="outline" size="sm" onClick={() => api.sharedDownload(asset.id, selectedVersion, preview.path).catch((error) => toast.error((error as Error).message))}>
                    <DownloadIcon /> 下载
                  </Button>
                )}
              </div>
              {previewLoading ? (
                <p className="py-16 text-center text-muted-foreground text-sm">读取中…</p>
              ) : preview?.binary || preview?.too_large ? (
                <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
                  <p className="text-muted-foreground text-sm">{preview.too_large ? "文件超过 2 MiB，不在线预览。" : "二进制文件不提供在线预览。"}</p>
                  <Button variant="outline" onClick={() => api.sharedDownload(asset.id, selectedVersion, preview.path).catch((error) => toast.error((error as Error).message))}>
                    <DownloadIcon /> 下载文件
                  </Button>
                </div>
              ) : preview ? (
                <pre className="min-h-0 flex-1 overflow-auto whitespace-pre p-4 font-mono text-xs leading-relaxed">{preview.content}</pre>
              ) : (
                <div className="flex flex-1 items-center justify-center p-8 text-muted-foreground text-sm">从左侧选择文件查看只读内容。</div>
              )}
            </div>
          </div>
        </TabsContent>

        <TabsContent value="versions" className="pt-4">
          <div className="overflow-hidden border">
            <Table>
              <TableHeader><TableRow><TableHead>版本</TableHead><TableHead>来源路径</TableHead><TableHead className="hidden md:table-cell">内容哈希</TableHead><TableHead className="hidden sm:table-cell">发布时间</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
              <TableBody>
                {versions.map((version) => (
                  <TableRow key={version.id}>
                    <TableCell><Badge variant={version.version === asset.current_version ? "default" : "outline"}>v{version.version}</Badge></TableCell>
                    <TableCell className="max-w-72"><p className="truncate font-mono text-xs" title={version.source_path}>{version.source_path || "—"}</p></TableCell>
                    <TableCell className="hidden md:table-cell"><span className="font-mono text-muted-foreground text-xs">{version.content_hash.slice(0, 16)}…</span></TableCell>
                    <TableCell className="hidden text-muted-foreground text-xs sm:table-cell">{fmtTime(version.created_at)}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setSelectedVersion(version.version);
                          setTab("files");
                        }}
                      >
                        浏览文件
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </TabsContent>

        <TabsContent value="events" className="pt-4">
          <div className="flex flex-col border">
            {events.length === 0 && <p className="py-12 text-center text-muted-foreground text-sm">暂无变更记录</p>}
            {events.map((event) => (
              <div key={event.id} className="flex gap-3 border-b p-4 last:border-b-0">
                <CheckIcon className="mt-0.5 size-4 shrink-0 text-emerald-500" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="font-medium text-sm">{eventLabel[event.action] ?? event.action}</p>
                    <p className="text-muted-foreground text-xs">{fmtTime(event.created_at)}</p>
                  </div>
                  <p className="mt-1 text-muted-foreground text-xs">操作者：{event.actor || "web"}</p>
                  {Object.keys(event.detail).length > 0 && (
                    <details className="mt-2">
                      <summary className="cursor-pointer text-muted-foreground text-xs">查看变更内容</summary>
                      <pre className="mt-2 max-h-64 overflow-auto border bg-muted p-3 font-mono text-xs">{JSON.stringify(event.detail, null, 2)}</pre>
                    </details>
                  )}
                </div>
              </div>
            ))}
          </div>
        </TabsContent>
      </Tabs>

      <SharedAssetEditDialog
        asset={asset}
        open={editOpen}
        onOpenChange={setEditOpen}
        onSaved={(updated) => {
          setAsset(updated);
          setEditOpen(false);
          void loadDetail();
        }}
      />
      <SharedPublishDialog
        open={publishOpen}
        onOpenChange={setPublishOpen}
        asset={asset}
        onPublished={() => void loadDetail()}
      />
    </div>
  );
}

export default function SharedAssetDetailPage() {
  return (
    <React.Suspense fallback={<p className="py-16 text-center text-muted-foreground text-sm">正在读取共享资产…</p>}>
      <SharedAssetDetail />
    </React.Suspense>
  );
}
