"use client";

import * as React from "react";
import { CheckCircle2, FilePlus2, ImageIcon, Loader2, ShieldCheck } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { FindingEvidenceTimeline } from "@/lib/types";

function isImageEvidence(kind: string): boolean {
  const normalized = kind.trim().toLowerCase().replace(/^c2_/, "");
  return normalized === "screenshot" || normalized === "image";
}

function evidenceNote(metadata: Record<string, unknown>): string {
  const note = metadata?.note;
  return typeof note === "string" ? note.trim() : "";
}

function evidenceSourceURL(metadata: Record<string, unknown>): string {
  const sourceUrl = metadata?.source_url;
  return typeof sourceUrl === "string" ? sourceUrl.trim() : "";
}

function evidenceProvenance(metadata: Record<string, unknown>): string {
  const provenance = metadata?.provenance;
  return typeof provenance === "string" ? provenance : "";
}

function evidenceTrusted(metadata: Record<string, unknown>): boolean {
  const provenance = evidenceProvenance(metadata);
  if (["native_action_rewrite", "native_page", "same_origin_panel"].includes(provenance)) return true;
  return metadata?.source === "c2";
}

function fileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error ?? new Error("读取截图失败"));
    reader.readAsDataURL(file);
  });
}

export function FindingVerificationPanel({
  findingId,
  readOnly,
  onChanged,
}: {
  findingId: string;
  readOnly?: boolean;
  onChanged?: () => void;
}) {
  const [timeline, setTimeline] = React.useState<FindingEvidenceTimeline | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [kind, setKind] = React.useState("command");
  const [title, setTitle] = React.useState("");
  const [content, setContent] = React.useState("");
  const [screenshot, setScreenshot] = React.useState<File | null>(null);
  const screenshotInput = React.useRef<HTMLInputElement>(null);
  const [verdict, setVerdict] = React.useState<"verified" | "false_positive" | "inconclusive">("verified");
  const [summary, setSummary] = React.useState("");
  const [evidence, setEvidence] = React.useState("");
  const [counterEvidence, setCounterEvidence] = React.useState("");
  const [tags, setTags] = React.useState("");
  const [pivotHints, setPivotHints] = React.useState<string[]>([]);
  const screenshots = React.useMemo(
    () => (timeline?.generic ?? []).filter((item) => isImageEvidence(item.kind)),
    [timeline?.generic],
  );
  const otherEvidence = React.useMemo(
    () => (timeline?.generic ?? []).filter((item) => !isImageEvidence(item.kind)),
    [timeline?.generic],
  );
  const addingScreenshot = isImageEvidence(kind);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const data = await api.findingEvidence(findingId);
      setTimeline(data);
      setPivotHints(data.verification?.pivot_hints ?? data.pivot_hints ?? []);
    } catch (error) {
      toast.error(`加载证据失败: ${error}`);
    } finally {
      setLoading(false);
    }
  }, [findingId]);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function addEvidence() {
    setSaving(true);
    try {
      let evidenceContent = content;
      if (addingScreenshot) {
        if (!screenshot && !content.trim()) {
          toast.error("请选择截图或填写图片 data URL");
          return;
        }
        if (screenshot) {
          if (screenshot.size > 10 << 20) {
            toast.error("截图不能超过 10 MiB");
            return;
          }
          evidenceContent = await fileAsDataURL(screenshot);
        }
      } else if (!content.trim()) {
        toast.error("请填写证据内容");
        return;
      }
      await api.addFindingEvidence(findingId, {
        kind: kind.trim() || (screenshot ? "screenshot" : "manual"),
        title: title.trim() || screenshot?.name || kind.trim() || "证据",
        content: evidenceContent,
        expected_version: timeline?.version,
        reason: "人工补充 Finding 证据",
      });
      setTitle("");
      setContent("");
      setScreenshot(null);
      if (screenshotInput.current) screenshotInput.current.value = "";
      toast.success("证据已保存");
      await load();
      onChanged?.();
    } catch (error) {
      toast.error(`保存失败: ${error}`);
    } finally {
      setSaving(false);
    }
  }

  async function saveVerification() {
    const counter = counterEvidence
      .split("\n")
      .map((value) => value.trim())
      .filter(Boolean);
    if (!summary.trim() || !evidence.trim() || counter.length === 0) {
      toast.error("请填写结论、证据和至少一条反证排查项");
      return;
    }
    setSaving(true);
    try {
      const result = await api.verifyFinding(findingId, {
        verdict,
        summary: summary.trim(),
        evidence,
        counter_evidence: counter,
        tags: tags
          .split(/[,\s]+/)
          .map((value) => value.trim())
          .filter(Boolean),
        expected_version: timeline?.version,
      });
      setPivotHints(result.pivot_hints);
      toast.success("验证结论已保存");
      await load();
      onChanged?.();
    } catch (error) {
      toast.error(`验证失败: ${error}`);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle className="flex items-center gap-2 text-sm">
          <ImageIcon className="size-4" />
          关键证据与验证
        </CardTitle>
        <div className="flex items-center gap-2">
          {timeline ? <Badge variant="outline">证据 v{timeline.version}</Badge> : null}
          {loading ? <Loader2 className="size-4 animate-spin" /> : null}
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="space-y-3">
          <div className="flex items-center justify-between gap-3">
            <div className="text-sm font-medium">关键截图</div>
            <Badge variant="outline">{screenshots.length} 张</Badge>
          </div>
          {screenshots.length === 0 ? (
            <div className="text-muted-foreground rounded-md border border-dashed px-3 py-6 text-center text-sm">
              {otherEvidence.length > 0
                ? `已有 ${otherEvidence.length} 条文字证据，但还没有可预览的关键截图。`
                : "暂无关键截图。Web 漏洞确认后应复现并绑定漏洞页面截图。"}
            </div>
          ) : (
            <div className="space-y-4">
              {screenshots.map((item) => {
                const note = evidenceNote(item.metadata);
                const sourceUrl = evidenceSourceURL(item.metadata);
                const provenance = evidenceProvenance(item.metadata);
                const trusted = evidenceTrusted(item.metadata);
                return (
                  <figure key={item.id} className="overflow-hidden rounded-md border bg-muted/20">
                    <a
                      href={api.findingEvidenceContentUrl(findingId, item.id)}
                      target="_blank"
                      rel="noreferrer"
                      title="打开原图"
                      className="block bg-black/90"
                    >
                      <img
                        src={api.findingEvidenceContentUrl(findingId, item.id)}
                        alt={item.title}
                        loading="lazy"
                        decoding="async"
                        className="mx-auto max-h-[62vh] w-auto max-w-full object-contain"
                      />
                    </a>
                    <figcaption className="space-y-1 border-t px-3 py-2">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="text-sm font-medium">{item.title}</div>
                        <Badge variant={trusted ? "default" : "outline"}>
                          {trusted
                            ? provenance === "native_action_rewrite"
                              ? "原始页面受控截图"
                              : "受控截图"
                            : "待核验截图"}
                        </Badge>
                      </div>
                      {note ? <div className="text-muted-foreground text-xs leading-relaxed">{note}</div> : null}
                      {sourceUrl ? (
                        <a
                          href={sourceUrl}
                          target="_blank"
                          rel="noreferrer"
                          className="text-primary block truncate text-xs hover:underline"
                          title={sourceUrl}
                        >
                          来源：{sourceUrl}
                        </a>
                      ) : null}
                      <div className="text-muted-foreground font-mono text-[11px]">
                        evidence #{item.id}
                        {item.sha256 ? ` · sha256:${item.sha256.slice(0, 16)}…` : ""}
                      </div>
                    </figcaption>
                  </figure>
                );
              })}
            </div>
          )}
        </div>

        <div className="space-y-2">
          <div className="text-sm font-medium">通用证据</div>
          {!otherEvidence.length ? (
            <div className="text-muted-foreground rounded-md border px-3 py-6 text-center text-sm">暂无通用证据</div>
          ) : (
            <div className="divide-y rounded-md border">
              {otherEvidence.map((item) => (
                <div key={item.id} className="px-3 py-2">
                  <div className="flex items-center justify-between gap-2">
                    <div className="truncate text-sm font-medium">{item.title}</div>
                    <Badge variant="secondary">{item.kind}</Badge>
                  </div>
                  <div className="text-muted-foreground mt-1 text-xs">
                    {item.sha256 ? `sha256:${item.sha256.slice(0, 16)}…` : item.storage_ref || `${item.size} bytes`}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {!readOnly ? (
          <div className="grid gap-3 md:grid-cols-[140px_minmax(0,1fr)]">
            <div className="space-y-1.5">
              <Label htmlFor="evidence-kind">类型</Label>
              <select
                id="evidence-kind"
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
                value={kind}
                onChange={(event) => setKind(event.target.value)}
              >
                <option value="command">command</option>
                <option value="screenshot">screenshot</option>
                <option value="log">log</option>
                <option value="data">data</option>
                <option value="manual">manual</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="evidence-title">标题</Label>
              <Input id="evidence-title" value={title} onChange={(event) => setTitle(event.target.value)} />
            </div>
            <div className="space-y-1.5 md:col-span-2">
              <Label htmlFor="evidence-content">{addingScreenshot ? "图片 data URL（选择文件时留空）" : "证据内容"}</Label>
              <Textarea
                id="evidence-content"
                rows={4}
                value={content}
                onChange={(event) => setContent(event.target.value)}
                placeholder={addingScreenshot ? "可选：粘贴 data:image/png;base64,..." : "粘贴命令输出、请求响应或人工整理材料"}
              />
            </div>
            {addingScreenshot ? (
              <div className="space-y-1.5 md:col-span-2">
                <Label htmlFor="evidence-screenshot">截图文件</Label>
                <Input
                  ref={screenshotInput}
                  id="evidence-screenshot"
                  type="file"
                  accept="image/png,image/jpeg,image/gif"
                  onChange={(event) => setScreenshot(event.target.files?.[0] ?? null)}
                />
                <p className="text-muted-foreground text-xs">PNG/JPEG/GIF，最大 10 MiB；保存后会直接显示在上方关键截图中。</p>
              </div>
            ) : null}
            <div className="md:col-span-2 flex justify-end">
              <Button size="sm" onClick={() => void addEvidence()} disabled={saving}>
                <FilePlus2 />
                添加证据
              </Button>
            </div>
          </div>
        ) : null}

        <div className="space-y-3 border-t pt-4">
          <div className="flex items-center gap-2">
            <ShieldCheck className="size-4" />
            <span className="text-sm font-medium">验证结论</span>
            {timeline?.verification?.verdict ? (
              <Badge variant="outline">
                <CheckCircle2 />
                {timeline.verification.verdict}
              </Badge>
            ) : null}
          </div>
          <div className="grid gap-3 md:grid-cols-[160px_minmax(0,1fr)]">
            <div className="space-y-1.5">
              <Label htmlFor="verify-verdict">结论</Label>
              <select
                id="verify-verdict"
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
                value={verdict}
                onChange={(event) => setVerdict(event.target.value as typeof verdict)}
                disabled={readOnly}
              >
                <option value="verified">verified</option>
                <option value="false_positive">false_positive</option>
                <option value="inconclusive">inconclusive</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="verify-tags">类别标签</Label>
              <Input id="verify-tags" value={tags} onChange={(event) => setTags(event.target.value)} placeholder="sqli, auth-bypass" disabled={readOnly} />
            </div>
            <div className="space-y-1.5 md:col-span-2">
              <Label htmlFor="verify-summary">结论摘要</Label>
              <Input id="verify-summary" value={summary} onChange={(event) => setSummary(event.target.value)} disabled={readOnly} />
            </div>
            <div className="space-y-1.5 md:col-span-2">
              <Label htmlFor="verify-evidence">机械证据</Label>
              <Textarea id="verify-evidence" rows={5} value={evidence} onChange={(event) => setEvidence(event.target.value)} disabled={readOnly} />
            </div>
            <div className="space-y-1.5 md:col-span-2">
              <Label htmlFor="verify-counter">反证清单</Label>
              <Textarea
                id="verify-counter"
                rows={4}
                value={counterEvidence}
                onChange={(event) => setCounterEvidence(event.target.value)}
                placeholder="每行一条实际排查项与结果"
                disabled={readOnly}
              />
            </div>
          </div>
          {pivotHints.length > 0 ? (
            <div className="rounded-md border bg-muted/30 px-3 py-2">
              <div className="text-sm font-medium">转向提示</div>
              <ul className="text-muted-foreground mt-1 space-y-1 text-xs">
                {pivotHints.map((hint) => (
                  <li key={hint}>{hint}</li>
                ))}
              </ul>
            </div>
          ) : null}
          {!readOnly ? (
            <div className="flex justify-end">
              <Button onClick={() => void saveVerification()} disabled={saving}>
                {saving ? <Loader2 className="animate-spin" /> : <ShieldCheck />}
                保存验证
              </Button>
            </div>
          ) : null}
        </div>
      </CardContent>
    </Card>
  );
}
