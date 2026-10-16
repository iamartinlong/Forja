import { Suspense, lazy } from "react";
import { ResizableEditor } from "./ResizableEditor";
import { InlineLoader } from "@/components/ui/page-loader";

const ERDPreview = lazy(() => import("@/components/ERDPreview"));

export function DesignForm({ content, onChange }: { content: string; onChange: (v: string) => void }) {
  return (
    <div className="space-y-2">
      <ResizableEditor value={content} onChange={onChange} language="json" initialHeight={260} />
      <Suspense fallback={<InlineLoader text="ERD 预览加载中…" />}>
        <ERDPreview content={content} />
      </Suspense>
    </div>
  );
}
