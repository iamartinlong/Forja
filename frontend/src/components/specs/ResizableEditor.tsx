import { useCallback, useRef, useState } from "react";
import Editor from "@monaco-editor/react";

const MIN_H = 160;
const MAX_H = 720;

export function ResizableEditor({
  value,
  onChange,
  language = "json",
  initialHeight = 240,
}: {
  value: string;
  onChange: (v: string) => void;
  language?: string;
  initialHeight?: number;
}) {
  const [height, setHeight] = useState(initialHeight);
  const drag = useRef({ active: false, startY: 0, startH: 0 });

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      drag.current = { active: true, startY: e.clientY, startH: height };
      const move = (ev: MouseEvent) => {
        if (!drag.current.active) return;
        const next = drag.current.startH + (ev.clientY - drag.current.startY);
        setHeight(Math.min(MAX_H, Math.max(MIN_H, next)));
      };
      const up = () => {
        drag.current.active = false;
        window.removeEventListener("mousemove", move);
        window.removeEventListener("mouseup", up);
      };
      window.addEventListener("mousemove", move);
      window.addEventListener("mouseup", up);
    },
    [height],
  );

  return (
    <div className="border rounded-md overflow-hidden relative" style={{ height }}>
      <Editor
        height="100%"
        language={language}
        value={value}
        onChange={(v) => onChange(v || "")}
        options={{ minimap: { enabled: false }, fontSize: 13, wordWrap: "on", scrollBeyondLastLine: false }}
      />
      <div
        onMouseDown={onMouseDown}
        title="拖动调整高度"
        className="absolute bottom-0 inset-x-0 h-1.5 cursor-row-resize hover:bg-border transition-colors"
      />
    </div>
  );
}
