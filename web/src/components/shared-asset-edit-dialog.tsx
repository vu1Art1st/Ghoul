"use client";

import * as React from "react";
import { SaveIcon } from "lucide-react";
import { toast } from "sonner";

import { api } from "@/lib/api";
import type { SharedAsset } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

type EditForm = {
  display_name: string;
  description: string;
  tags: string;
  language: string;
  usage: string;
  dependencies: string;
  runtime_spec: string;
};

function formFromAsset(asset: SharedAsset): EditForm {
  return {
    display_name: asset.display_name || asset.name,
    description: asset.description,
    tags: asset.tags.join(", "),
    language: asset.language,
    usage: asset.usage,
    dependencies: asset.dependencies.join(", "),
    runtime_spec: asset.runtime_spec ?? "",
  };
}

function splitList(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function SharedAssetEditDialog({
  asset,
  open,
  onOpenChange,
  onSaved,
}: {
  asset: SharedAsset | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved: (asset: SharedAsset) => void;
}) {
  const [form, setForm] = React.useState<EditForm>({
    display_name: "",
    description: "",
    tags: "",
    language: "",
    usage: "",
    dependencies: "",
    runtime_spec: "",
  });
  const [saving, setSaving] = React.useState(false);

  React.useEffect(() => {
    if (open && asset) setForm(formFromAsset(asset));
  }, [asset, open]);

  const set = <K extends keyof EditForm>(key: K, value: EditForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }));
  };

  const save = async () => {
    if (!asset) return;
    setSaving(true);
    try {
      const updated = await api.sharedPatchAsset(asset.id, {
        display_name: form.display_name.trim(),
        description: form.description.trim(),
        tags: splitList(form.tags),
        language: form.language.trim(),
        usage: form.usage.trim(),
        dependencies: splitList(form.dependencies),
        runtime_spec: form.runtime_spec.trim(),
        expected_updated_at: asset.updated_at,
      });
      toast.success("资产资料已更新");
      onOpenChange(false);
      onSaved(updated);
    } catch (error) {
      toast.error(`保存失败：${(error as Error).message}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[90vh] flex-col overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>编辑资产资料</DialogTitle>
          <DialogDescription>
            资料修改立即生效，不会创建新版本，也不会改变已发布文件。稳定路径标识保持为
            <code className="mx-1 rounded bg-muted px-1 py-0.5 font-mono text-xs">{asset?.name}</code>。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="grid gap-2 sm:col-span-2">
            <Label htmlFor="shared-edit-display-name">显示名称</Label>
            <Input
              id="shared-edit-display-name"
              value={form.display_name}
              onChange={(event) => set("display_name", event.target.value)}
              placeholder="例如：瑞数风控绕过工具"
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="shared-edit-language">语言</Label>
            <Input
              id="shared-edit-language"
              value={form.language}
              onChange={(event) => set("language", event.target.value)}
              placeholder="python / javascript / shell"
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="shared-edit-runtime">运行时说明</Label>
            <Input
              id="shared-edit-runtime"
              value={form.runtime_spec}
              onChange={(event) => set("runtime_spec", event.target.value)}
              placeholder="Python 3.12 + ddddocr"
            />
          </div>
          <div className="grid gap-2 sm:col-span-2">
            <Label htmlFor="shared-edit-description">用途说明</Label>
            <Textarea
              id="shared-edit-description"
              value={form.description}
              onChange={(event) => set("description", event.target.value)}
              placeholder="适用场景、输入输出、限制与验证情况"
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="shared-edit-tags">标签</Label>
            <Input
              id="shared-edit-tags"
              value={form.tags}
              onChange={(event) => set("tags", event.target.value)}
              placeholder="瑞数, bypass, 验证码"
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="shared-edit-dependencies">依赖</Label>
            <Input
              id="shared-edit-dependencies"
              value={form.dependencies}
              onChange={(event) => set("dependencies", event.target.value)}
              placeholder="ddddocr, requests"
            />
          </div>
          <div className="grid gap-2 sm:col-span-2">
            <Label htmlFor="shared-edit-usage">调用示例</Label>
            <Textarea
              id="shared-edit-usage"
              value={form.usage}
              onChange={(event) => set("usage", event.target.value)}
              placeholder="$GHOUL_PYTHON main.py --url https://target.example"
              className="min-h-24 font-mono text-xs"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={save} disabled={saving || !form.display_name.trim()}>
            <SaveIcon />
            {saving ? "保存中…" : "保存资料"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
