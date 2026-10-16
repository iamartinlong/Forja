import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export interface AcRow {
  given: string;
  when: string;
  then: string;
}

export interface StoryDraft {
  story_id: string;
  title: string;
  description: string;
  acceptance_criteria: AcRow[];
  priority: string;
}

const EMPTY: StoryDraft = {
  story_id: "",
  title: "",
  description: "",
  acceptance_criteria: [{ given: "", when: "", then: "" }],
  priority: "P1",
};

function parseStory(content: string): StoryDraft {
  try {
    const obj = JSON.parse(content);
    if (!obj || typeof obj !== "object" || Array.isArray(obj)) return { ...EMPTY };
    const ac = Array.isArray(obj.acceptance_criteria)
      ? (obj.acceptance_criteria as Record<string, unknown>[]).map((r) => ({
          given: typeof r?.given === "string" ? r.given : "",
          when: typeof r?.when === "string" ? r.when : "",
          then: typeof r?.then === "string" ? r.then : "",
        }))
      : [];
    return {
      story_id: typeof obj.story_id === "string" ? obj.story_id : "",
      title: typeof obj.title === "string" ? obj.title : "",
      description: typeof obj.description === "string" ? obj.description : "",
      acceptance_criteria: ac.length > 0 ? ac : [{ given: "", when: "", then: "" }],
      priority: typeof obj.priority === "string" ? obj.priority : "P1",
    };
  } catch {
    return { ...EMPTY };
  }
}

function serializeStory(d: StoryDraft): string {
  // story_id 留空时省略键，后端自动生成 {SLUG}-{序号}
  const out: Record<string, unknown> = {
    title: d.title,
    description: d.description,
    acceptance_criteria: d.acceptance_criteria,
    priority: d.priority,
  };
  if (d.story_id.trim()) out.story_id = d.story_id.trim();
  return JSON.stringify(out, null, 2);
}

export function StoryForm({ content, onChange }: { content: string; onChange: (v: string) => void }) {
  const [draft, setDraft] = useState<StoryDraft>(() => parseStory(content));
  const [showAdvanced, setShowAdvanced] = useState(false);
  const lastEmitted = useRef<string | null>(null);

  useEffect(() => {
    if (lastEmitted.current !== null && content === lastEmitted.current) return;
    setDraft(parseStory(content));
    lastEmitted.current = null;
  }, [content]);

  const update = (patch: Partial<StoryDraft>) => {
    const next = { ...draft, ...patch };
    setDraft(next);
    const s = serializeStory(next);
    lastEmitted.current = s;
    onChange(s);
  };

  const updateAc = (i: number, patch: Partial<AcRow>) => {
    const ac = draft.acceptance_criteria.map((row, idx) => (idx === i ? { ...row, ...patch } : row));
    update({ acceptance_criteria: ac });
  };

  return (
    <div className="space-y-3">
      <div className="flex flex-col gap-2">
        <label className="text-xs">标题</label>
        <Input
          value={draft.title}
          onChange={(e) => update({ title: e.target.value })}
          placeholder="用户想完成什么？"
          className="h-8"
        />
      </div>
      <div className="flex flex-col gap-2">
        <label className="text-xs">描述</label>
        <Input
          value={draft.description}
          onChange={(e) => update({ description: e.target.value })}
          placeholder="作为…，我想…，以便…"
          className="h-8"
        />
      </div>
      <div>
        <div className="flex justify-between items-center">
          <label className="text-xs">验收标准（Given / When / Then）</label>
          <Button
            size="sm"
            variant="outline"
            type="button"
            className="h-6"
            onClick={() => update({ acceptance_criteria: [...draft.acceptance_criteria, { given: "", when: "", then: "" }] })}
          >
            + 添加一条
          </Button>
        </div>
        <div className="space-y-2 mt-1">
          {draft.acceptance_criteria.map((row, i) => (
            <div key={i} className="grid grid-cols-7 gap-1 items-center">
              <Input
                value={row.given}
                onChange={(e) => updateAc(i, { given: e.target.value })}
                placeholder="前置条件"
                className="h-7 text-xs col-span-2"
              />
              <Input
                value={row.when}
                onChange={(e) => updateAc(i, { when: e.target.value })}
                placeholder="用户操作"
                className="h-7 text-xs col-span-2"
              />
              <Input
                value={row.then}
                onChange={(e) => updateAc(i, { then: e.target.value })}
                placeholder="预期结果"
                className="h-7 text-xs col-span-2"
              />
              <Button
                size="sm"
                variant="ghost"
                type="button"
                className="h-7"
                onClick={() =>
                  update({
                    acceptance_criteria:
                      draft.acceptance_criteria.length > 1
                        ? draft.acceptance_criteria.filter((_, idx) => idx !== i)
                        : [{ given: "", when: "", then: "" }],
                  })
                }
                aria-label="删除该条验收标准"
              >
                ×
              </Button>
            </div>
          ))}
        </div>
      </div>
      <div className="flex flex-col gap-2">
        <label className="text-xs">优先级</label>
        <select
          value={draft.priority}
          onChange={(e) => update({ priority: e.target.value })}
          className="border rounded-md h-8 px-2 text-sm w-full bg-background"
        >
          <option>P0</option>
          <option>P1</option>
          <option>P2</option>
        </select>
      </div>

      {draft.story_id && !showAdvanced && (
        <div className="text-xs text-muted-foreground flex gap-2 items-center">
          编号：<span className="font-mono text-foreground">{draft.story_id}</span>
        </div>
      )}
      <div>
        <Button size="sm" variant="ghost" type="button" className="h-6 px-2" onClick={() => setShowAdvanced(!showAdvanced)}>
          {showAdvanced ? "收起高级选项" : "高级选项"}
        </Button>
        {showAdvanced && (
          <div className="flex flex-col gap-2 mt-1">
            <label className="text-xs">编号（留空由系统自动生成）</label>
            <Input
              value={draft.story_id}
              onChange={(e) => update({ story_id: e.target.value })}
              placeholder="如 S-MANUAL 或留空"
              className="h-8 font-mono"
            />
          </div>
        )}
      </div>
    </div>
  );
}
