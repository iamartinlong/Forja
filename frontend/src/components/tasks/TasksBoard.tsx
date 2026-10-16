import { useEffect, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Tasks } from "@/lib/api";
import { useForjaStore } from "@/lib/store";
import { can } from "@/lib/auth";
import { forge } from "@/tokens/colors";
import { DndContext, closestCenter, DragOverlay, useDroppable } from "@dnd-kit/core";
import type { DragEndEvent, DragOverEvent, DragStartEvent } from "@dnd-kit/core";
import { SortableContext, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { toast } from "sonner";
import { useMinLoading } from "@/hooks/useMinLoading";
import { CreateTaskDialog } from "@/components/tasks/CreateTaskDialog";
import { ReleaseDialog } from "@/components/tasks/ReleaseDialog";
import { RejectDialog } from "@/components/tasks/RejectDialog";
import { InfoTooltip } from "@/components/shared/InfoTooltip";
import { GLOSSARY } from "@/lib/glossary";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { ChevronLeft, ChevronRight, Flame, RefreshCw, Loader2 } from "lucide-react";

function SortableTask({ t, col, onRelease, onReject, onRerun }: { t: any; col: string; onRelease?: (t: any) => void; onReject?: (t: any) => void; onRerun?: (t: any) => void }) {
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({ id: t.id, data: { col, task: t } });
  const navigate = useNavigate();
  const style: any = { transform: CSS.Transform.toString(transform), transition };
  return (
    <div ref={setNodeRef} style={style} {...attributes} {...listeners} className="border rounded-md p-3 text-xs bg-card cursor-grab hover:shadow">
      <div className="font-medium truncate">{t.title}</div>
      <div className="flex gap-1 mt-1 items-center">
        <Badge variant="outline" className="text-[10px]">P{t.priority}</Badge><Badge variant="secondary" className="text-[10px]">{t.estimated_hours || "-"}h</Badge>
        <Button
          variant="ghost" size="sm" className="h-5 px-1.5 text-[10px] ml-auto text-muted-foreground"
          // dnd-kit 的 pointerdown 监听在卡片根上：不截停会被拖拽抢先激活、
          // 真机 click 到不了按钮（合成事件单测测不出，2026-09-09 发布/打回点不动实录）
          onPointerDown={(e) => e.stopPropagation()}
          onClick={(e) => { e.stopPropagation(); navigate({ to: "/forge/$taskId", params: { taskId: String(t.id) } }); }}
        >
          <Flame className="size-3 mr-0.5" />流水线
        </Button>
      </div>
      <div className="text-[10px] text-muted-foreground mt-1">deps: {(t.depends_on || []).map((d: string) => d.slice(0, 6)).join(", ") || "-"}</div>
      {col === "PENDING" && <div className="text-[10px] text-muted-foreground mt-1">拖至 RUNNING 触发 Approve</div>}
      {col === "RELEASE_PENDING" && (
        <div className="flex gap-1 mt-2">
          {can("task.release") && <Button size="sm" variant="forge" className="h-5 px-1.5 text-[10px]" onClick={e => { e.stopPropagation(); onRelease?.(t); }} onPointerDown={(e) => e.stopPropagation()}>发布</Button>}
          {can("task.release") && <Button size="sm" variant="outline" className="h-5 px-1.5 text-[10px]" onClick={e => { e.stopPropagation(); onReject?.(t); }} onPointerDown={(e) => e.stopPropagation()}>打回</Button>}
        </div>
      )}
      {["FAILED", "ESCALATED"].includes(col) && can("task.run") && (
        <Button size="sm" variant="outline" className="h-5 px-1.5 text-[10px] mt-2" onClick={e => { e.stopPropagation(); onRerun?.(t); }} onPointerDown={(e) => e.stopPropagation()}>重跑验证</Button>
      )}
    </div>
  );
}

// U-5 任务看板：Fizzy vertical-tab 参考实现 (frontend/src/components/tasks/TasksBoard.tsx)
// 布局规约：
// - ≥1280 (xl): 4 主栏 240px + 2 异常栏折叠为右沿 32px 竖签 (flex-nowrap 一行不换行，overflow-x-auto 兜底)
// - 1024-1279 (lg): 退化为 3+3 两行 grid (grid-cols-3)
// - <1024: 单栏 + 顶部列选择器
// 交互：竖签纵向文字 + 计数徽章（有任务时弹动），点击就地展开/折叠，拖入自动展开，localStorage 持久化

const MAIN_COLS = ["PENDING", "RUNNING", "RELEASE_PENDING", "RELEASED"] as const;
const EXCEPTION_COLS = ["FAILED", "ESCALATED"] as const;
const ALL_COLS = ["PENDING", "RUNNING", "RELEASE_PENDING", "RELEASED", "FAILED", "ESCALATED"] as const;
type ColId = typeof ALL_COLS[number];
type ExceptionCol = typeof EXCEPTION_COLS[number];

const LS_KEY = "forja_tasks_collapsed";

function loadCollapsed(): Record<ExceptionCol, boolean> {
  try {
    if (typeof window === "undefined" || typeof (globalThis as any).localStorage === "undefined") return { FAILED: true, ESCALATED: true };
    const raw = localStorage.getItem(LS_KEY);
    if (raw) {
      const parsed = JSON.parse(raw);
      return {
        FAILED: parsed.FAILED ?? true,
        ESCALATED: parsed.ESCALATED ?? true,
      };
    }
  } catch {}
  return { FAILED: true, ESCALATED: true };
}

function useViewportTier(): "xl" | "lg" | "mobile" {
  const getTier = (): "xl" | "lg" | "mobile" => {
    if (typeof window === "undefined") return "xl";
    const w = window.innerWidth;
    if (w >= 1280) return "xl";
    if (w >= 1024) return "lg";
    return "mobile";
  };
  const [tier, setTier] = useState<"xl" | "lg" | "mobile">(getTier);
  useEffect(() => {
    const onResize = () => setTier(getTier());
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  return tier;
}

const COL_TIPS: Record<string, string> = {
  PENDING: GLOSSARY.spec.desc,
  RUNNING: "已批准并开始锻造：Agent 按 Spec 产出 diff 与测试，最多 3 轮自动修复。",
  RELEASE_PENDING: GLOSSARY.release_pending.desc,
  RELEASED: "已签核发布，commit 已回流项目仓。",
  FAILED: "锻造失败（Agent 不可达或门禁未过），可重跑。",
  ESCALATED: "三轮修复均未通过，需人工介入分析失败原因。",
};

/** 列提示对应的文档词条外链（无词条的列不挂链接） */
const COL_DOCS: Record<string, string | undefined> = {
  PENDING: GLOSSARY.spec.docUrl,
  RUNNING: GLOSSARY.forge.docUrl,
  RELEASE_PENDING: GLOSSARY.release_pending.docUrl,
};

const COL_COLORS: Record<string, string> = {
  PENDING: forge.flame,
  RUNNING: forge.ember,
  RELEASE_PENDING: forge.ember,
  RELEASED: forge.success,
  FAILED: "var(--forge-danger)",
  ESCALATED: "var(--forge-warning)",
};

function DroppableCol({ id, children, title, color, tip, className, onCollapse, showCollapse, tier }: any) {
  const { setNodeRef, isOver } = useDroppable({ id });
  const isException = (EXCEPTION_COLS as readonly string[]).includes(id);
  return (
    <div ref={setNodeRef} className={cn("border rounded-lg bg-card flex flex-col", isOver ? "ring-2 ring-orange-300" : "", className)}>
      <div
        className="px-3 py-2 text-xs font-semibold border-b flex justify-between items-center shrink-0"
        style={{ background: color, color: id === "PENDING" ? "#000" : id === "ESCALATED" ? "#000" : "#fff", borderRadius: "8px 8px 0 0" }}
      >
        <span className="truncate">{title}</span>
        <span className="flex items-center gap-1 shrink-0">
          {tip && <span className="shrink-0 [&_svg]:text-current/70"><InfoTooltip text={tip} docUrl={COL_DOCS[id]} /></span>}
          {isException && showCollapse && tier === "xl" && (
            <button
              type="button"
              aria-label={`折叠 ${id}`}
              title={`折叠 ${id}`}
              onClick={(e) => { e.stopPropagation(); onCollapse?.(); }}
              className="ml-1 inline-flex size-5 items-center justify-center rounded hover:bg-black/10 dark:hover:bg-white/10 transition-colors"
            >
              <ChevronRight className="size-3.5" />
            </button>
          )}
        </span>
      </div>
      <div className="p-2 space-y-2 min-h-[160px] flex-1 overflow-auto">{children}</div>
    </div>
  );
}

function CollapsedVerticalTab({ id, count, onExpand, tip }: { id: ExceptionCol; count: number; onExpand: () => void; tip?: string }) {
  const { setNodeRef, isOver } = useDroppable({ id });
  const hasTasks = count > 0;
  return (
    <div
      ref={setNodeRef}
      role="button"
      tabIndex={0}
      aria-label={`${id} ${count} 项，点击展开`}
      title={`${id} (${count}) — 点击展开`}
      onClick={onExpand}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onExpand(); } }}
      className={cn(
        "w-8 shrink-0 border rounded-lg flex flex-col items-center py-3 gap-2 cursor-pointer select-none transition-colors min-h-[160px]",
        isOver ? "ring-2 ring-orange-300 bg-accent" : "bg-card hover:bg-accent",
      )}
    >
      <span
        className="text-[11px] font-semibold tracking-widest whitespace-nowrap select-none"
        style={{ writingMode: "vertical-rl", textOrientation: "mixed" } as React.CSSProperties}
      >
        {id}
      </span>
      <span
        className={cn(
          "inline-flex items-center justify-center rounded-full text-[10px] font-bold min-w-[20px] h-5 px-1 border",
          hasTasks
            ? id === "FAILED"
              ? "bg-destructive text-destructive-foreground border-transparent animate-bounce"
              : "bg-amber-500 text-black border-transparent animate-bounce"
            : "bg-secondary text-secondary-foreground",
        )}
      >
        {count}
      </span>
      {hasTasks && <span className="text-[8px] text-muted-foreground" style={{ writingMode: "vertical-rl" } as React.CSSProperties}>有任务</span>}
      {tip && <span className="mt-auto opacity-60 [&_svg]:size-3"><InfoTooltip text={tip} docUrl={COL_DOCS[id]} /></span>}
      <ChevronLeft className="size-3 text-muted-foreground mt-1" />
    </div>
  );
}

export function TasksBoard() {
  const [items, setItems] = useState<any[]>([]);
  const [dag, setDag] = useState<any>(null);
  const [active, setActive] = useState<any>(null);
  const [releaseTask, setReleaseTask] = useState<any>(null);
  const [rejectTask, setRejectTask] = useState<any>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const projectId = useForjaStore(s => s.projectId);
  const tier = useViewportTier();
  const [collapsed, setCollapsed] = useState<Record<ExceptionCol, boolean>>(() => loadCollapsed());
  const [mobileCol, setMobileCol] = useState<ColId>("PENDING");
  const [refreshing, setRefreshing] = useState(false);
  const showRefreshing = useMinLoading(refreshing, 400);
  // 请求代际：projectId 切换与手动刷新可重入——旧响应用代际丢弃，
  // 禁止用 refreshing 布尔守卫跳过新请求（2026-09-08 实录：进板加载中
  // 切换项目，新请求被吞，Header 显示新项目、看板停留旧数据，需手动再切一次）。
  const epochRef = useRef(0);

  useEffect(() => { try { localStorage.setItem(LS_KEY, JSON.stringify(collapsed)); } catch {} }, [collapsed]);

  const load = async () => {
    const ep = ++epochRef.current;
    setRefreshing(true);
    try {
      const r: any = await Tasks.list(projectId);
      if (ep !== epochRef.current) return;
      setItems(r.items || []);
      const d: any = await Tasks.dag(projectId);
      if (ep !== epochRef.current) return;
      setDag(d);
    } catch (e: any) {
      if (ep !== epochRef.current) return;
      toast.error(e.message);
    } finally {
      if (ep === epochRef.current) setRefreshing(false);
    }
  };
  useEffect(() => { load(); }, [projectId]);
  const cols: Record<string, any[]> = { PENDING: [], RUNNING: [], FAILED: [], ESCALATED: [], RELEASE_PENDING: [], RELEASED: [] };
  // 存量 PASSED（A5 前）归入待发布列展示，后端 release/reject 同样兼容
  items.forEach((t: any) => { const s = t.status === "PASSED" ? "RELEASE_PENDING" : (t.status in cols ? t.status : "PENDING"); cols[s].push(t); });
  const doRerun = async (t: any) => { try { await Tasks.converge(String(t.id)); toast.success(`已触发重跑：${t.title}`); load(); } catch (err: any) { toast.error(err.message); } };
  const handleDragStart = (e: DragStartEvent) => setActive(e.active.data.current?.task);
  const handleDragOver = (e: DragOverEvent) => {
    const overId = e.over ? String(e.over.id) : null;
    if (!overId) return;
    // 拖入折叠的异常栏自动展开（仅 xl 生效，lg/mobile 已展开）
    if (tier === "xl" && (EXCEPTION_COLS as readonly string[]).includes(overId)) {
      const ec = overId as ExceptionCol;
      if (collapsed[ec]) setCollapsed(prev => ({ ...prev, [ec]: false }));
    } else if (tier === "xl") {
      // 拖入任务卡片所属的折叠列也自动展开
      for (const k of Object.keys(cols)) {
        if ((EXCEPTION_COLS as readonly string[]).includes(k) && collapsed[k as ExceptionCol]) {
          const colTasks: any[] = cols[k];
          if (colTasks.some(x => String(x.id) === overId)) {
            setCollapsed(prev => ({ ...prev, [k as ExceptionCol]: false }));
            break;
          }
        }
      }
    }
  };
  const handleDragEnd = async (e: DragEndEvent) => {
    setActive(null); const { active: a, over } = e; if (!over) return;
    const activeCol = (a.data.current as any)?.col; const overId = String(over.id);
    let targetCol = overId; if (["PENDING", "RUNNING", "PASSED"].indexOf(overId) === -1) { for (const k of Object.keys(cols)) if (cols[k].some(x => x.id === overId)) { targetCol = k; break; } }
    if (activeCol === targetCol) {
      if (activeCol === "PENDING") {
        const list = cols.PENDING; const oldIdx = list.findIndex(x => x.id === a.id); const newIdx = list.findIndex(x => x.id === overId);
        if (oldIdx >= 0 && newIdx >= 0 && oldIdx !== newIdx) { const prio = list.length - newIdx; try { await Tasks.patch(String(a.id), { priority: prio }); load(); } catch (err: any) { toast.error(err.message); } }
      } return;
    }
    if (activeCol === "PENDING" && targetCol === "RUNNING") { try { await Tasks.approve(String(a.id)); load(); } catch (err: any) { toast.error(err.message); } }
    else toast.warning(`仅支持 PENDING→RUNNING，本次 ${activeCol}→${targetCol} 已忽略`);
  };

  const isLg = tier === "lg";
  const isMobile = tier === "mobile";

  const renderBoardContent = () => {
    if (isMobile) {
      // <1024 单栏 + 顶部选择器已在外层渲染，此处仅渲染选中列
      const col = mobileCol;
      return (
        <DroppableCol
          key={col}
          id={col}
          title={`${col} (${cols[col]?.length || 0})`}
          color={COL_COLORS[col]}
          tip={COL_TIPS[col]}
          className="w-full"
          tier={tier}
        >
          <SortableContext items={(cols[col] || []).map((x: any) => x.id)} strategy={verticalListSortingStrategy}>
            {(cols[col] || []).map((t: any) => <SortableTask key={t.id} t={t} col={col} onRelease={setReleaseTask} onReject={setRejectTask} onRerun={doRerun} />)}
          </SortableContext>
        </DroppableCol>
      );
    }
    if (isLg) {
      // 1024-1279：3+3 两行 grid，异常栏强制展开忽略 collapsed
      return (
        <div className="grid grid-cols-3 gap-3">
          {(ALL_COLS as readonly ColId[]).map(col => (
            <DroppableCol
              key={col}
              id={col}
              title={`${col} (${cols[col]?.length || 0})`}
              color={COL_COLORS[col]}
              tip={COL_TIPS[col]}
              className="min-w-0"
              tier={tier}
            >
              <SortableContext items={(cols[col] || []).map((x: any) => x.id)} strategy={verticalListSortingStrategy}>
                {(cols[col] || []).map((t: any) => <SortableTask key={t.id} t={t} col={col} onRelease={setReleaseTask} onReject={setRejectTask} onRerun={doRerun} />)}
              </SortableContext>
            </DroppableCol>
          ))}
        </div>
      );
    }
    // xl: ≥1280 4 主栏展开 240px + 2 异常栏按 collapsed 决定 32px 竖签或 240px 展开，flex-nowrap 一行不换行
    // 4 栏视口守恒：任一异常展开时隐藏 RELEASED，保证展开后不超屏可交互
    const shouldHideReleased = !collapsed.FAILED || !collapsed.ESCALATED;
    const visibleMainCols = shouldHideReleased ? (MAIN_COLS as readonly ColId[]).filter(c => c !== "RELEASED") : MAIN_COLS;
    return (
      <div className="flex flex-nowrap items-start gap-3 overflow-x-auto pb-2 min-w-0">
        {visibleMainCols.map(col => (
          <DroppableCol
            key={col}
            id={col}
            title={`${col} (${cols[col]?.length || 0})`}
            color={COL_COLORS[col]}
            tip={COL_TIPS[col]}
            className="w-[240px] shrink-0 min-w-[240px]"
            tier={tier}
          >
            <SortableContext items={(cols[col] || []).map((x: any) => x.id)} strategy={verticalListSortingStrategy}>
              {(cols[col] || []).map((t: any) => <SortableTask key={t.id} t={t} col={col} onRelease={setReleaseTask} onReject={setRejectTask} onRerun={doRerun} />)}
            </SortableContext>
          </DroppableCol>
        ))}
        {(EXCEPTION_COLS as readonly ExceptionCol[]).map(col => {
          const isCollapsed = collapsed[col];
          if (isCollapsed) {
            return <CollapsedVerticalTab key={col} id={col} count={cols[col]?.length || 0} tip={COL_TIPS[col]} onExpand={() => {
              setCollapsed(prev => ({ ...prev, [col]: false }));
            }} />;
          }
          return (
            <DroppableCol
              key={col}
              id={col}
              title={`${col} (${cols[col]?.length || 0})`}
              color={COL_COLORS[col]}
              tip={COL_TIPS[col]}
              className="w-[240px] shrink-0 min-w-[240px]"
              onCollapse={() => setCollapsed(prev => ({ ...prev, [col]: true }))}
              showCollapse
              tier={tier}
            >
              <SortableContext items={(cols[col] || []).map((x: any) => x.id)} strategy={verticalListSortingStrategy}>
                {(cols[col] || []).map((t: any) => <SortableTask key={t.id} t={t} col={col} onRelease={setReleaseTask} onReject={setRejectTask} onRerun={doRerun} />)}
              </SortableContext>
            </DroppableCol>
          );
        })}
      </div>
    );
  };

  return (
    <div className="space-y-4">
      <div className="flex gap-2 items-center flex-wrap"><Button variant="outline" size="sm" onClick={load} disabled={showRefreshing} className="h-8">{showRefreshing ? <Loader2 className="size-3.5 animate-spin mr-1" /> : <RefreshCw className="size-3.5 mr-1" />}刷新</Button><Button variant="forge" size="sm" onClick={() => setCreateOpen(true)} className="h-8">派生锻造任务</Button><span className="text-xs text-muted-foreground">并行就绪 {dag?.ready?.length || 0} 个任务</span></div>
      {items.length === 0 && (
        <Card>
          <CardContent className="py-8 text-center space-y-2">
            <div className="text-sm font-medium">看板还是空的</div>
            <div className="text-xs text-muted-foreground">先在 Spec 编辑器完成并验证规格（VALIDATED），再从这里派生锻造任务；任务卡片拖入 RUNNING 即开始锻造。</div>
            <Button variant="forge" size="sm" className="mt-1" onClick={() => setCreateOpen(true)}>派生首个锻造任务</Button>
          </CardContent>
        </Card>
      )}
      {/* <1024 顶部列选择器：单栏切换 */}
      {isMobile && (
        <div className="flex gap-1.5 overflow-x-auto pb-1 -mx-1 px-1">
          {(ALL_COLS as readonly ColId[]).map(col => {
            const cnt = cols[col]?.length || 0;
            const isActive = mobileCol === col;
            const isException = (EXCEPTION_COLS as readonly string[]).includes(col);
            const hasTasks = isException && cnt > 0;
            return (
              <button
                key={col}
                type="button"
                onClick={() => setMobileCol(col)}
                className={cn(
                  "shrink-0 inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium transition-colors",
                  isActive ? "bg-primary text-primary-foreground border-transparent" : "bg-card hover:bg-accent",
                )}
              >
                <span>{col}</span>
                <span
                  className={cn(
                    "inline-flex items-center justify-center rounded-full min-w-[18px] h-[18px] px-1 text-[10px] font-bold",
                    isActive ? "bg-white/20 text-current" : hasTasks ? "bg-destructive text-destructive-foreground animate-bounce" : "bg-secondary text-secondary-foreground",
                    hasTasks && col === "ESCALATED" && !isActive ? "bg-amber-500 text-black animate-bounce" : "",
                  )}
                >
                  {cnt}
                </span>
              </button>
            );
          })}
        </div>
      )}
      <div className="relative">
        {showRefreshing && (
          <div className="absolute inset-0 bg-background/60 backdrop-blur-[1px] z-10 flex items-center justify-center rounded-lg">
            <span className="inline-flex items-center gap-2 text-xs text-muted-foreground bg-background border rounded-full px-3 py-1.5 shadow-sm">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> 正在刷新…
            </span>
          </div>
        )}
        <div className={showRefreshing ? "opacity-60 pointer-events-none transition-opacity" : "transition-opacity"}>
        <DndContext collisionDetection={closestCenter} onDragStart={handleDragStart} onDragOver={handleDragOver} onDragEnd={handleDragEnd}>
          {renderBoardContent()}
          <DragOverlay>{active ? <div className="border rounded-md p-3 text-xs bg-card shadow-lg w-56"><div className="font-medium truncate">{active.title}</div></div> : null}</DragOverlay>
        </DndContext>
        </div>
      </div>
      <Card><CardHeader className="pb-2"><CardTitle className="text-xs">任务依赖 DAG</CardTitle></CardHeader><CardContent className="text-xs text-muted-foreground">节点 {dag?.nodes?.length || 0} · 边 {dag?.edges?.length || 0} · 并行就绪 {dag?.ready?.length || 0}（图形视图见甘特页）</CardContent></Card>
      <CreateTaskDialog open={createOpen} onClose={() => setCreateOpen(false)} onDone={() => load()} />
      <ReleaseDialog task={releaseTask} onClose={() => setReleaseTask(null)} onDone={load} />
      <RejectDialog task={rejectTask} onClose={() => setRejectTask(null)} onDone={load} />
    </div>
  );
}

export default TasksBoard;
