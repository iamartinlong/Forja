import type { ReactNode } from "react";
import { Info } from "lucide-react";

/** 零依赖悬停术语提示（CSS group-hover），词表见 lib/glossary.ts；docUrl 有值时附「详见文档」外链 */
export function InfoTooltip({ text, docUrl, children }: { text: string; docUrl?: string; children?: ReactNode }) {
  return (
    <span className="relative inline-flex items-center gap-0.5 group/tip">
      {children}
      <Info className="h-3 w-3 text-muted-foreground/60 cursor-help" />
      <span className="pointer-events-none hidden group-hover/tip:block absolute left-1/2 -translate-x-1/2 top-full mt-1 z-50 w-60 rounded-md border bg-popover p-2 text-[11px] leading-relaxed text-popover-foreground shadow-md">
        {text}
        {docUrl && (
          <>
            {" "}
            <a href={docUrl} target="_blank" rel="noreferrer" className="pointer-events-auto text-primary underline underline-offset-2 hover:opacity-80">
              详见文档
            </a>
          </>
        )}
      </span>
    </span>
  );
}
