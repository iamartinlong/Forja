import { ResizableEditor } from "./ResizableEditor";

export function TechForm({ content, onChange }: { content: string; onChange: (v: string) => void }) {
  return <ResizableEditor value={content} onChange={onChange} language="json" initialHeight={320} />;
}
