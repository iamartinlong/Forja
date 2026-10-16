import { Suspense, lazy } from "react";
import { ResizableEditor } from "./ResizableEditor";
import { InlineLoader } from "@/components/ui/page-loader";

const SwaggerPreview = lazy(() => import("@/components/SwaggerPreview"));

export function ContractForm({ content, onChange }: { content: string; onChange: (v: string) => void }) {
  return (
    <div className="space-y-2">
      <ResizableEditor value={content} onChange={onChange} language="json" initialHeight={260} />
      <Suspense fallback={<InlineLoader text="契约预览加载中…" />}>
        <SwaggerPreview content={content} />
      </Suspense>
    </div>
  );
}
