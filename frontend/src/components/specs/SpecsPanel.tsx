import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import * as z from "zod";
import { toast } from "sonner";
import { Specs } from "@/lib/api";
import type { GateResult, Pact } from "@/lib/api";
import { useForjaStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { InlineLoader } from "@/components/ui/page-loader";
import { Layers, Link2, FileCode } from "lucide-react";
import { StoryForm } from "./StoryForm";
import { ContractForm } from "./ContractForm";
import { DesignForm } from "./DesignForm";
import { TechForm } from "./TechForm";
import { GateResultCard } from "./GateResultCard";

const LAYER_LABELS: Record<string, string> = {
  BS: "用户故事",
  IS: "接口契约",
  DS: "数据设计",
  TS: "技术方案",
};

// anchor path validation mirrors backend/internal/api/spec.go:validateAnchors (frontend/src/components/specs/SpecsPanel.tsx:22)
const anchorPathRe = /^[a-zA-Z0-9_.\-/]+$/;
const specSchema = z.object({
  layer: z.enum(["BS", "IS", "DS", "TS"]),
  title: z.string().min(1, "标题必填").max(256, "标题过长"),
  content: z
    .string()
    .min(1, "内容必填")
    .refine((v) => {
      try {
        JSON.parse(v);
        return true;
      } catch {
        return false;
      }
    }, "内容需为合法 JSON"),
  refs: z.array(z.string().uuid("须为合法 UUID")).max(20, "关联 Spec 最多20个").default([]),
  anchors: z
    .array(
      z
        .string()
        .trim()
        .min(1, "锚点必填")
        .max(512, "路径过长")
        .refine((v) => !v.includes("..") && !v.includes("\\"), "锚点不得包含 '..' 或反斜杠")
        .refine((v) => !v.startsWith("/"), "锚点须为相对路径（不能以 / 开头）")
        .refine((v) => v.includes("/"), "锚点须包含 '/' 如 frontend/src/components/UserBoard.tsx")
        .refine((v) => anchorPathRe.test(v), "锚点含非法字符，仅允许字母数字 _ . - /"),
    )
    .max(20, "锚点最多20个")
    .default([]),
});
type SpecForm = z.infer<typeof specSchema>;
type SpecFormInput = z.input<typeof specSchema>;

const CREATE_DEFAULTS: SpecForm = {
  layer: "BS",
  title: "",
  content: JSON.stringify(
    { title: "", description: "", acceptance_criteria: [{ given: "", when: "", then: "" }], priority: "P1" },
    null,
    2,
  ),
  refs: [],
  anchors: [],
};

export default function SpecsPanel() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const projectId = useForjaStore((s) => s.projectId);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [activeSpecId, setActiveSpecId] = useState<string | null>(null);
  const [checks, setChecks] = useState<GateResult | null>(null);
  const [pact, setPact] = useState<Pact | null>(null);

  const form = useForm<SpecFormInput, unknown, SpecForm>({
    resolver: zodResolver(specSchema),
    defaultValues: CREATE_DEFAULTS,
  });
  const layer = form.watch("layer");
  const content = form.watch("content");
  const refs = form.watch("refs") ?? [];
  const anchors = form.watch("anchors") ?? [];

  const listQuery = useQuery({
    queryKey: ["specs", projectId, layer],
    queryFn: () => Specs.list(layer, projectId),
    enabled: !!projectId,
  });

  // all specs for refs multi-select (frontend/src/components/specs/SpecsPanel.tsx:78)
  const allSpecsQuery = useQuery({
    queryKey: ["specs-all", projectId],
    queryFn: () => Specs.list(undefined, projectId, 1, 100),
    enabled: !!projectId,
  });

  const refDetailsQuery = useQuery({
    queryKey: ["spec-refs", activeSpecId],
    queryFn: () => Specs.refs(activeSpecId as string),
    enabled: !!activeSpecId,
  });

  const saveMutation = useMutation({
    mutationFn: async (values: SpecForm) => {
      const body = {
        title: values.title,
        content: JSON.parse(values.content),
        refs: values.refs ?? [],
        anchors: values.anchors ?? [],
      };
      return editingId ? Specs.update(editingId, body) : Specs.create({ layer: values.layer, ...body });
    },
    onSuccess: (r) => {
      setActiveSpecId(r.id);
      if (editingId) {
        toast.success(`已保存，新版本 ${r.version}`);
      } else {
        setEditingId(r.id);
        toast.success(`创建成功（${LAYER_LABELS[layer] ?? layer}），版本 ${r.version}`);
      }
      setChecks(null);
      setPact(null);
      queryClient.invalidateQueries({ queryKey: ["specs"] });
      queryClient.invalidateQueries({ queryKey: ["specs-all"] });
      queryClient.invalidateQueries({ queryKey: ["spec-refs"] });
    },
  });

  const validateMutation = useMutation({
    mutationFn: () => Specs.validate(activeSpecId as string),
    onSuccess: (r) => {
      setChecks(r);
      toast.success(r.passed ? "门禁全部通过" : `门禁未通过：${r.checks.filter((c) => c.blocking && !c.passed).length} 项阻塞`);
      queryClient.invalidateQueries({ queryKey: ["specs"] });
    },
  });

  const pactMutation = useMutation({
    mutationFn: () => Specs.pact(activeSpecId as string),
    onSuccess: (r) => {
      setPact(r.pact);
      toast.success(`契约已生成，共 ${r.pact.interactions.length} 个交互`);
    },
  });

  const startEdit = async (id: string) => {
    try {
      const d = await Specs.get(id);
      setEditingId(id);
      setActiveSpecId(id);
      setPact(null);
      setChecks(null);
      form.reset({
        layer: (["BS", "IS", "DS", "TS"].includes(d.layer) ? d.layer : "BS") as SpecForm["layer"],
        title: d.title,
        content: JSON.stringify(d.content ?? {}, null, 2),
        refs: (d.refs ?? []) as string[],
        anchors: (d.anchors ?? []) as string[],
      });
    } catch {
      // 错误由 http 拦截器统一 toast (frontend/src/components/specs/SpecsPanel.tsx:115)
    }
  };

  const exitEdit = () => {
    setEditingId(null);
    setActiveSpecId(null);
    form.reset(CREATE_DEFAULTS);
  };

  const onSubmit = form.handleSubmit((values) => saveMutation.mutate(values));
  const setContent = (v: string) => form.setValue("content", v, { shouldValidate: true });

  const toggleRef = (id: string) => {
    const cur = form.getValues("refs") ?? [];
    if (cur.includes(id)) {
      form.setValue("refs", cur.filter((x) => x !== id), { shouldValidate: true });
    } else {
      if (cur.length >= 20) {
        toast.error("关联 Spec 最多20个");
        return;
      }
      form.setValue("refs", [...cur, id], { shouldValidate: true });
    }
  };

  const addAnchor = () => {
    const cur = form.getValues("anchors") ?? [];
    if (cur.length >= 20) {
      toast.error("锚点最多20个");
      return;
    }
    form.setValue("anchors", [...cur, ""], { shouldValidate: true });
  };

  const updateAnchor = (idx: number, val: string) => {
    const cur = [...(form.getValues("anchors") ?? [])];
    cur[idx] = val;
    form.setValue("anchors", cur, { shouldValidate: true, shouldDirty: true });
  };

  const removeAnchor = (idx: number) => {
    const cur = [...(form.getValues("anchors") ?? [])];
    cur.splice(idx, 1);
    form.setValue("anchors", cur, { shouldValidate: true });
  };

  const items = listQuery.data?.items ?? [];
  const allSpecs = allSpecsQuery.data?.items ?? [];

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="pt-6">
          <form onSubmit={onSubmit} className="space-y-3">
            <div className="flex gap-2 items-start flex-wrap">
              <div className="flex flex-col gap-2">
                <select
                  {...form.register("layer")}
                  disabled={!!editingId}
                  className="border rounded-md px-2 py-2 text-sm bg-background h-10 disabled:opacity-60"
                >
                  {Object.entries(LAYER_LABELS).map(([k, label]) => (
                    <option key={k} value={k}>
                      {label}（{k}）
                    </option>
                  ))}
                </select>
                {form.formState.errors.layer && (
                  <span className="text-xs text-destructive">{form.formState.errors.layer.message}</span>
                )}
              </div>
              <div className="flex-1 min-w-[180px] flex flex-col gap-2">
                <Input {...form.register("title")} placeholder={editingId ? "修改标题" : "给这个规格起个名字"} />
                {form.formState.errors.title && (
                  <span className="text-xs text-destructive">{form.formState.errors.title.message}</span>
                )}
              </div>
              <Button variant="forge" type="submit" disabled={saveMutation.isPending}>
                {saveMutation.isPending ? "保存中…" : editingId ? "保存修改" : "创建"}
              </Button>
              <Button
                variant="outline"
                type="button"
                disabled={!activeSpecId || validateMutation.isPending}
                onClick={() => validateMutation.mutate()}
              >
                {validateMutation.isPending ? "校验中…" : "校验门禁"}
              </Button>
              <Button
                variant="secondary"
                type="button"
                disabled={!activeSpecId || pactMutation.isPending}
                onClick={() => pactMutation.mutate()}
              >
                {pactMutation.isPending ? "生成中…" : "生成契约"}
              </Button>
              {editingId && (
                <Button variant="ghost" type="button" onClick={exitEdit}>
                  新建另一条
                </Button>
              )}
            </div>

            {/* §2b 增量 Spec 三要素: refs + anchors (frontend/src/components/specs/SpecsPanel.tsx:145) */}
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 pt-2 border-t">
              {/* refs multi-select */}
              <div className="flex flex-col gap-2">
                <label className="text-xs font-medium flex items-center gap-1.5">
                  <Link2 className="size-3.5" />
                  关联 Spec (refs) — 增量需求指向的老 Spec
                  <span className="text-muted-foreground font-normal">纯增量不改既有 AC → 新 Spec refs 老 Spec</span>
                </label>
                {allSpecs.length === 0 ? (
                  <div className="text-xs text-muted-foreground">暂无可选 Spec</div>
                ) : (
                  <div className="border rounded-md p-2 max-h-40 overflow-auto space-y-1 bg-muted/20">
                    {allSpecs
                      .filter((s) => s.id !== editingId)
                      .map((s) => {
                        const selected = refs.includes(s.id);
                        return (
                          <label
                            key={s.id}
                            className={`flex items-center gap-2 text-xs px-2 py-1 rounded cursor-pointer hover:bg-muted ${selected ? "bg-primary/10 border border-primary/30" : ""}`}
                          >
                            <input
                              type="checkbox"
                              checked={selected}
                              onChange={() => toggleRef(s.id)}
                              className="size-3.5"
                            />
                            <Badge variant="outline" className="text-[10px] shrink-0">
                              {s.layer}
                            </Badge>
                            <span className="truncate flex-1">{s.title}</span>
                            <span className="font-mono text-[10px] text-muted-foreground">{s.id.slice(0, 8)}</span>
                          </label>
                        );
                      })}
                  </div>
                )}
                {refs.length > 0 && (
                  <div className="flex gap-1 flex-wrap">
                    {refs.map((id) => {
                      const spec = allSpecs.find((x) => x.id === id);
                      return (
                        <Badge key={id} variant="secondary" className="font-mono text-[10px] gap-1">
                          {spec ? spec.title.slice(0, 12) : id.slice(0, 8)}
                          <button type="button" onClick={() => toggleRef(id)} className="ml-1 hover:text-destructive">
                            ×
                          </button>
                        </Badge>
                      );
                    })}
                  </div>
                )}
                {form.formState.errors.refs && (
                  <span className="text-xs text-destructive">{form.formState.errors.refs.message as string}</span>
                )}
                {/* refs as links preview when active */}
                {refDetailsQuery.data?.refs && refDetailsQuery.data.refs.length > 0 && (
                  <div className="text-xs space-y-1 border rounded p-2 bg-background">
                    <div className="font-medium flex items-center gap-1">
                      <Link2 className="size-3" /> 已关联 {refDetailsQuery.data.refs.length} 个 Spec
                    </div>
                    {refDetailsQuery.data.refs.map((r) => (
                      <div key={r.id} className="flex items-center gap-2">
                        <Badge variant="outline" className="text-[10px]">
                          {r.layer}
                        </Badge>
                        <button
                          type="button"
                          className="text-xs underline truncate flex-1 text-left"
                          onClick={() => startEdit(r.id)}
                        >
                          {r.title}
                        </button>
                        <span className="font-mono text-[10px] text-muted-foreground">{r.id.slice(0, 8)}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>

              {/* anchors file path inputs */}
              <div className="flex flex-col gap-2">
                <label className="text-xs font-medium flex items-center gap-1.5">
                  <FileCode className="size-3.5" />
                  代码锚点 (anchors) — 允许修改的文件范围
                  <span className="text-muted-foreground font-normal">超范围需在 change_log 说明</span>
                </label>
                <div className="space-y-2">
                  {anchors.length === 0 && (
                    <div className="text-xs text-muted-foreground">未设置锚点（将按常规检索）</div>
                  )}
                  {anchors.map((val, idx) => (
                    <div key={idx} className="flex flex-col gap-2">
                      <div className="flex gap-1 items-center">
                        <Input
                          value={val}
                          onChange={(e) => updateAnchor(idx, e.target.value)}
                          placeholder="如 frontend/src/components/UserBoard.tsx"
                          className="flex-1 h-8 font-mono text-xs"
                        />
                        <Button variant="ghost" size="sm" type="button" className="h-8 px-2" onClick={() => removeAnchor(idx)}>
                          ×
                        </Button>
                      </div>
                      {/* per-element inline error (frontend/src/components/specs/SpecsPanel.tsx:230) */}
                      {(() => {
                        const arrErr = form.formState.errors.anchors as unknown as { [k: number]: { message?: string } } | undefined;
                        const msg = arrErr?.[idx]?.message;
                        if (msg) return <span className="text-xs text-destructive">{msg}</span>;
                        // also handle generic array error
                        const genMsg = (form.formState.errors.anchors as unknown as { message?: string })?.message;
                        if (idx === 0 && genMsg && anchors.length === 0) return <span className="text-xs text-destructive">{genMsg}</span>;
                        return null;
                      })()}
                    </div>
                  ))}
                </div>
                <div className="flex gap-1">
                  <Button variant="outline" size="sm" type="button" className="h-7" onClick={addAnchor}>
                    + 添加锚点
                  </Button>
                  <span className="text-[11px] text-muted-foreground self-center">示例：backend/internal/api/user.go</span>
                </div>
                {form.formState.errors.anchors && typeof form.formState.errors.anchors.message === "string" && (
                  <span className="text-xs text-destructive">{form.formState.errors.anchors.message}</span>
                )}
                {/* anchors as code paths preview */}
                {anchors.length > 0 && (
                  <div className="flex gap-1 flex-wrap">
                    {anchors.filter((a) => a.trim()).map((a) => (
                      <code key={a} className="text-[10px] bg-muted px-1.5 py-0.5 rounded font-mono">
                        {a}
                      </code>
                    ))}
                  </div>
                )}
              </div>
            </div>

            {form.formState.errors.content && (
              <div className="text-xs text-destructive mt-1">{form.formState.errors.content.message}</div>
            )}
            {activeSpecId && (
              <div className="text-xs text-muted-foreground mt-2 flex gap-2 items-center">
                当前规格：<span className="font-mono truncate max-w-[240px]">{activeSpecId}</span>
                {editingId && <Badge variant="secondary">编辑中</Badge>}
              </div>
            )}
          </form>
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">{LAYER_LABELS[layer] ?? layer}</CardTitle>
          </CardHeader>
          <CardContent>
            {layer === "BS" && <StoryForm content={content} onChange={setContent} />}
            {layer === "IS" && <ContractForm content={content} onChange={setContent} />}
            {layer === "DS" && <DesignForm content={content} onChange={setContent} />}
            {layer === "TS" && <TechForm content={content} onChange={setContent} />}
            {layer === "BS" && (
              <details className="mt-3">
                <summary className="cursor-pointer text-xs text-muted-foreground">查看数据（JSON）</summary>
                <pre className="text-[10px] whitespace-pre-wrap bg-muted p-2 rounded mt-1 max-h-48 overflow-auto">{content}</pre>
              </details>
            )}
          </CardContent>
        </Card>

        <div className="space-y-3">
          <GateResultCard result={checks} loading={validateMutation.isPending} />

          {pact && (
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm">契约预览（{pact.interactions.length} 个交互）</CardTitle>
              </CardHeader>
              <CardContent>
                <pre className="text-[10px] whitespace-pre-wrap bg-muted p-2 rounded max-h-64 overflow-auto">
                  {JSON.stringify(pact, null, 2)}
                </pre>
              </CardContent>
            </Card>
          )}

          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm flex items-center gap-2">
                {LAYER_LABELS[layer] ?? layer} 列表
                <Badge variant="outline">{items.length}</Badge>
              </CardTitle>
            </CardHeader>
            <CardContent>
              {listQuery.isLoading ? (
                <InlineLoader />
              ) : items.length === 0 ? (
                <div className="text-xs text-muted-foreground">还没有{LAYER_LABELS[layer] ?? layer}，填好后点「创建」</div>
              ) : (
                <div className="space-y-1">
                  {items.map((s) => (
                    <div
                      key={s.id}
                      className={`w-full text-left border rounded-md px-2 py-1.5 hover:border-ring transition-colors ${
                        s.id === activeSpecId ? "border-ring bg-muted/50" : ""
                      }`}
                    >
                      <div className="flex items-center gap-2">
                        <button
                          type="button"
                          onClick={() => startEdit(s.id)}
                          className="flex items-center gap-2 flex-1 min-w-0 text-left"
                        >
                          <Badge variant="outline">{s.layer}</Badge>
                          <span className="text-xs font-medium truncate flex-1">{s.title}</span>
                          <Badge variant={s.status === "VALIDATED" || s.status === "PASSED" ? "success" : "secondary"}>
                            {s.status === "VALIDATED" ? "已校验" : s.status === "DRAFT" ? "草稿" : s.status}
                          </Badge>
                        </button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-6 shrink-0"
                          aria-label={`查看 ${s.title} 生命周期`}
                          title="查看生命周期"
                          onClick={(e) => {
                            e.stopPropagation();
                            navigate({ to: "/specs/$specId", params: { specId: s.id } });
                          }}
                        >
                          <Layers className="size-3.5" />
                        </Button>
                      </div>
                      <div className="text-[10px] text-muted-foreground mt-0.5 flex gap-2 flex-wrap items-center">
                        <span>版本 {s.current_version}</span>
                        {s.refs && s.refs.length > 0 && (
                          <span className="flex items-center gap-1">
                            <Link2 className="size-3" /> refs: {s.refs.length}
                            <span className="flex gap-1 ml-1">
                              {s.refs.slice(0, 3).map((id) => (
                                <button
                                  key={id}
                                  type="button"
                                  className="font-mono text-[10px] underline"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    startEdit(id);
                                  }}
                                  title={id}
                                >
                                  {id.slice(0, 6)}
                                </button>
                              ))}
                              {s.refs.length > 3 && <span>+{s.refs.length - 3}</span>}
                            </span>
                          </span>
                        )}
                        {s.anchors && s.anchors.length > 0 && (
                          <span className="flex items-center gap-1">
                            <FileCode className="size-3" /> {s.anchors.length}锚点
                          </span>
                        )}
                      </div>
                      {s.anchors && s.anchors.length > 0 && (
                        <div className="flex gap-1 flex-wrap mt-1">
                          {s.anchors.slice(0, 2).map((a) => (
                            <code key={a} className="text-[10px] bg-muted px-1 py-0.5 rounded font-mono truncate max-w-[160px]">
                              {a}
                            </code>
                          ))}
                          {s.anchors.length > 2 && <span className="text-[10px] text-muted-foreground">+{s.anchors.length - 2}</span>}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
              {listQuery.isError && <div className="text-xs text-destructive mt-2">列表加载失败，请稍后重试</div>}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
