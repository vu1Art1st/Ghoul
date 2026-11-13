"use client";

import * as React from "react";
import { BookMarkedIcon, PlusIcon, SearchIcon } from "lucide-react";
import { toast } from "sonner";

import { api } from "@/lib/api";
import type { PocRecord } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";

const statusLabel: Record<PocRecord["validation_status"], string> = {
  draft: "草稿",
  reproduced: "已复现",
  verified: "已验证",
  stale: "已失效",
  retired: "已退役",
};

export default function PocPage() {
  const [items, setItems] = React.useState<PocRecord[]>([]);
  const [query, setQuery] = React.useState("");
  const [selected, setSelected] = React.useState<PocRecord | null>(null);
  const [createOpen, setCreateOpen] = React.useState(false);
  const [form, setForm] = React.useState({
    name: "",
    scope_type: "component" as PocRecord["scope_type"],
    target_key: "",
    product: "",
    component: "",
    affected_versions: "",
    exploit_method: "",
    impact: "",
  });
  const [ctfMetadata, setCtfMetadata] = React.useState({
    event: "",
    challenge: "",
    flag: "",
    poa: "",
    writeup: "",
  });

  const load = React.useCallback(() => {
    api.knowledgePocs(query).then(setItems).catch((e) => toast.error((e as Error).message));
  }, [query]);

  React.useEffect(load, [load]);

  const create = async () => {
    try {
      const created = await api.knowledgePocCreate({
        ...form,
        metadata:
          form.scope_type === "ctf"
            ? Object.fromEntries(Object.entries(ctfMetadata).filter(([, value]) => value.trim() !== ""))
            : {},
        validation_status: "draft",
        confidence: "observed",
        artifact_version: 0,
        created_at: "",
        updated_at: "",
      } as PocRecord);
      toast.success("PoC 草稿已创建");
      setCreateOpen(false);
      setCtfMetadata({ event: "", challenge: "", flag: "", poa: "", writeup: "" });
      setSelected(created);
      load();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="font-semibold text-lg">PoC 与赛题</h1>
          <p className="text-muted-foreground text-sm">按目标、产品、组件、版本和漏洞编号检索任务沉淀；CTF 档案保留完整 Flag 与 POA。</p>
        </div>
        <Button onClick={() => setCreateOpen(true)}><PlusIcon /> 新建 PoC</Button>
      </div>

      <div className="relative max-w-md">
        <SearchIcon className="absolute top-2.5 left-2.5 size-4 text-muted-foreground" />
        <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="目标、产品、组件、CVE、CTF 题目" className="pl-8" />
      </div>

      <div className="overflow-x-auto border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>名称</TableHead>
              <TableHead>范围</TableHead>
              <TableHead>产品 / 组件</TableHead>
              <TableHead>版本</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>来源任务</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.length === 0 && <TableRow><TableCell colSpan={6} className="py-12 text-center text-muted-foreground">暂无 PoC 记录</TableCell></TableRow>}
            {items.map((item) => (
              <TableRow key={item.id} className="cursor-pointer" onClick={() => setSelected(item)}>
                <TableCell className="font-mono text-sm">{item.name}</TableCell>
                <TableCell><Badge variant="outline">{item.scope_type}</Badge></TableCell>
                <TableCell className="text-sm">{[item.product, item.component].filter(Boolean).join(" / ") || item.target_key || "-"}</TableCell>
                <TableCell className="font-mono text-xs">{item.affected_versions || "-"}</TableCell>
                <TableCell><Badge variant={item.validation_status === "verified" ? "default" : "secondary"}>{statusLabel[item.validation_status]}</Badge></TableCell>
                <TableCell className="text-xs">{item.source_task_id ? `Task ${item.source_task_id}` : "-"}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader><DialogTitle>新建 PoC</DialogTitle></DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-1.5"><Label>名称</Label><Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></div>
            <div className="grid gap-1.5">
              <Label>范围</Label>
              <Select
                value={form.scope_type}
                onValueChange={(value) => setForm({ ...form, scope_type: value as PocRecord["scope_type"] })}
              >
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="target">目标</SelectItem>
                  <SelectItem value="product">产品</SelectItem>
                  <SelectItem value="component">组件</SelectItem>
                  <SelectItem value="ctf">CTF</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-1.5"><Label>目标</Label><Input value={form.target_key} onChange={(e) => setForm({ ...form, target_key: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label>产品</Label><Input value={form.product} onChange={(e) => setForm({ ...form, product: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label>组件</Label><Input value={form.component} onChange={(e) => setForm({ ...form, component: e.target.value })} /></div>
            <div className="grid gap-1.5"><Label>版本</Label><Input value={form.affected_versions} onChange={(e) => setForm({ ...form, affected_versions: e.target.value })} /></div>
            <div className="grid gap-1.5 sm:col-span-2"><Label>利用方式</Label><Textarea value={form.exploit_method} onChange={(e) => setForm({ ...form, exploit_method: e.target.value })} /></div>
            <div className="grid gap-1.5 sm:col-span-2"><Label>影响</Label><Textarea value={form.impact} onChange={(e) => setForm({ ...form, impact: e.target.value })} /></div>
            {form.scope_type === "ctf" && (
              <>
                <div className="grid gap-1.5"><Label>赛事</Label><Input value={ctfMetadata.event} onChange={(e) => setCtfMetadata({ ...ctfMetadata, event: e.target.value })} /></div>
                <div className="grid gap-1.5"><Label>题目</Label><Input value={ctfMetadata.challenge} onChange={(e) => setCtfMetadata({ ...ctfMetadata, challenge: e.target.value })} /></div>
                <div className="grid gap-1.5 sm:col-span-2"><Label>Flag</Label><Input className="font-mono" value={ctfMetadata.flag} onChange={(e) => setCtfMetadata({ ...ctfMetadata, flag: e.target.value })} /></div>
                <div className="grid gap-1.5 sm:col-span-2"><Label>POA</Label><Textarea value={ctfMetadata.poa} onChange={(e) => setCtfMetadata({ ...ctfMetadata, poa: e.target.value })} /></div>
                <div className="grid gap-1.5 sm:col-span-2"><Label>完整 Writeup</Label><Textarea className="min-h-28" value={ctfMetadata.writeup} onChange={(e) => setCtfMetadata({ ...ctfMetadata, writeup: e.target.value })} /></div>
              </>
            )}
          </div>
          <DialogFooter><Button variant="outline" onClick={() => setCreateOpen(false)}>取消</Button><Button disabled={!form.name.trim()} onClick={create}>创建草稿</Button></DialogFooter>
        </DialogContent>
      </Dialog>

      <Sheet open={Boolean(selected)} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent className="overflow-y-auto sm:max-w-xl">
          {selected && (
            <>
              <SheetHeader>
                <SheetTitle className="flex items-center gap-2"><BookMarkedIcon />{selected.name}</SheetTitle>
                <SheetDescription>{selected.scope_type} · {statusLabel[selected.validation_status]}</SheetDescription>
              </SheetHeader>
              <div className="flex flex-col gap-4 p-4 text-sm">
                <section><h3 className="mb-1 font-medium">目标</h3><p className="text-muted-foreground">{selected.target_key || "-"}</p></section>
                <section><h3 className="mb-1 font-medium">产品 / 组件 / 版本</h3><p className="text-muted-foreground">{[selected.product, selected.component, selected.affected_versions].filter(Boolean).join(" / ") || "-"}</p></section>
                <section><h3 className="mb-1 font-medium">CVE / CWE</h3><div className="flex flex-wrap gap-1">{selected.cve_ids?.map((id) => <Badge key={id}>{id}</Badge>)}{selected.cwe_ids?.map((id) => <Badge key={id} variant="outline">{id}</Badge>)}</div></section>
                <section><h3 className="mb-1 font-medium">利用方式</h3><pre className="whitespace-pre-wrap break-words border bg-muted p-3 font-mono text-xs">{selected.exploit_method || "-"}</pre></section>
                <section><h3 className="mb-1 font-medium">影响</h3><p className="whitespace-pre-wrap text-muted-foreground">{selected.impact || "-"}</p></section>
                {selected.scope_type === "ctf" && (
                  <>
                    <section><h3 className="mb-1 font-medium">Flag</h3><pre className="whitespace-pre-wrap break-words border bg-muted p-3 font-mono text-xs">{String(selected.metadata?.flag || "-")}</pre></section>
                    <section><h3 className="mb-1 font-medium">POA</h3><pre className="whitespace-pre-wrap break-words border bg-muted p-3 font-mono text-xs">{String(selected.metadata?.poa || "-")}</pre></section>
                    <section><h3 className="mb-1 font-medium">Writeup</h3><pre className="whitespace-pre-wrap break-words border bg-muted p-3 font-mono text-xs">{String(selected.metadata?.writeup || "-")}</pre></section>
                  </>
                )}
                <Button variant="outline" onClick={() => api.knowledgePocVerify(selected.id).then((r) => { setSelected(r); load(); toast.success("已验证"); }).catch((e) => toast.error((e as Error).message))}>标记已验证</Button>
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}
