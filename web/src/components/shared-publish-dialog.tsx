"use client";

import * as React from "react";
import { toast } from "sonner";

import { api } from "@/lib/api";
import type { SharedAsset, SharedPublishInput } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

export function SharedPublishDialog({
  open,
  onOpenChange,
  sourcePath,
  isDirectory = false,
  asset,
  onPublished,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sourcePath?: string;
  isDirectory?: boolean;
  asset?: SharedAsset | null;
  onPublished?: () => void;
}) {
  const [saving, setSaving] = React.useState(false);
  const [form, setForm] = React.useState<SharedPublishInput>({
    name: "", display_name: "", kind: "script", description: "", tags: [], language: "python",
    entrypoint: "", usage: "", dependencies: [], source_path: "",
  });

  React.useEffect(() => {
    if (!open) return;
    const filename = sourcePath?.split("/").pop() ?? "";
    const stem = filename.replace(/\.[^.]+$/, "");
    setForm({
      name: asset?.name ?? stem.replace(/[^A-Za-z0-9._-]/g, "-"),
      display_name: asset?.display_name ?? stem,
      kind: asset?.kind ?? (isDirectory ? "tool" : "script"),
      description: asset?.description ?? "",
      tags: asset?.tags ?? [],
      language: asset?.language ?? (filename.endsWith(".py") || isDirectory ? "python" : ""),
      entrypoint: asset?.entrypoint ?? (isDirectory ? "" : filename),
      usage: asset?.usage ?? "",
      dependencies: asset?.dependencies ?? [],
      source_path: asset ? "" : sourcePath ?? "",
      asset_id: asset?.id,
      source_task_id: asset?.source_task_id,
      source_intent_id: asset?.source_intent_id,
    });
  }, [open, sourcePath, isDirectory, asset]);

  const set = <K extends keyof SharedPublishInput>(key: K, value: SharedPublishInput[K]) => setForm((f) => ({ ...f, [key]: value }));
  const publish = async () => {
    setSaving(true);
    try {
      await api.sharedPublish(form);
      toast.success(asset ? "新版本已发布" : "资产已发布到共享空间");
      onOpenChange(false);
      onPublished?.();
    } catch (e) {
      toast.error(`发布失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[90vh] flex-col overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{asset ? `发布 ${asset.display_name || asset.name} 的新版本` : "发布到共享空间"}</DialogTitle>
          <DialogDescription>发布前请移除目标地址、Cookie、Token 及其他任务敏感信息。发布版本不可变，后续任务通过复制使用。</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="grid gap-2">
            <Label htmlFor="shared-display-name">显示名称</Label>
            <Input id="shared-display-name" value={form.display_name} onChange={(e) => set("display_name", e.target.value)} placeholder="瑞数绕过工具" />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="shared-name">稳定路径标识</Label>
            <Input id="shared-name" value={form.name} onChange={(e) => set("name", e.target.value)} disabled={Boolean(asset)} placeholder="rs-bypass" className="font-mono text-xs" />
          </div>
          <div className="grid gap-2">
            <Label>类型</Label>
            <Select value={form.kind} onValueChange={(v) => set("kind", v as SharedPublishInput["kind"])} disabled={Boolean(asset)}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="script">单文件脚本</SelectItem>
                <SelectItem value="tool">工具目录</SelectItem>
                <SelectItem value="library">语言库</SelectItem>
                <SelectItem value="skill">Skill</SelectItem>
                <SelectItem value="template">模板</SelectItem>
                <SelectItem value="ctf_report">CTF 赛题报告</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {(!sourcePath || asset) && (
            <div className="grid gap-2 sm:col-span-2">
              <Label htmlFor="shared-source">新版本来源路径</Label>
              <Input id="shared-source" value={form.source_path} onChange={(e) => set("source_path", e.target.value)} placeholder="tasks/4/i12/rs_bypass.py" className="font-mono text-xs" />
            </div>
          )}
          <div className="grid gap-2">
            <Label htmlFor="shared-language">语言</Label>
            <Input id="shared-language" value={form.language} onChange={(e) => set("language", e.target.value)} placeholder="python / javascript / shell" />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="shared-entry">入口文件</Label>
            <Input id="shared-entry" value={form.entrypoint} onChange={(e) => set("entrypoint", e.target.value)} placeholder="main.py" className="font-mono text-xs" />
          </div>
          <div className="grid gap-2 sm:col-span-2">
            <Label htmlFor="shared-description">用途说明</Label>
            <Textarea id="shared-description" value={form.description} onChange={(e) => set("description", e.target.value)} placeholder="适用场景、输入输出、限制与验证情况" />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="shared-tags">标签</Label>
            <Input id="shared-tags" value={form.tags.join(", ")} onChange={(e) => set("tags", e.target.value.split(",").map((v) => v.trim()).filter(Boolean))} placeholder="瑞数, bypass, 验证码" />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="shared-deps">Python 依赖</Label>
            <Input id="shared-deps" value={form.dependencies.join(", ")} onChange={(e) => set("dependencies", e.target.value.split(",").map((v) => v.trim()).filter(Boolean))} placeholder="ddddocr, requests" />
          </div>
          <div className="grid gap-2 sm:col-span-2">
            <Label htmlFor="shared-usage">调用示例</Label>
            <Textarea id="shared-usage" value={form.usage} onChange={(e) => set("usage", e.target.value)} placeholder="$GHOUL_PYTHON main.py --url https://target.example" className="min-h-20 font-mono text-xs" />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
          <Button onClick={publish} disabled={saving || !form.name.trim() || !form.display_name.trim() || !form.description.trim() || !form.source_path.trim() || !form.entrypoint.trim()}>
            {saving ? "发布中…" : "审核并发布"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
