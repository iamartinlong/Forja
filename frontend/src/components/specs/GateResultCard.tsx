import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { GateResult } from "@/lib/api";
import { forge } from "@/tokens/colors";

export function GateResultCard({ result, loading }: { result: GateResult | null; loading?: boolean }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm flex gap-2 items-center">
          门禁结果
          {loading && <Badge variant="secondary">检查中…</Badge>}
          {result && !loading && (
            <Badge variant={result.passed ? "success" : "destructive"}>
              {result.passed ? "PASSED" : "FAILED"}
            </Badge>
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-1">
        {result?.checks?.map((c) => (
          <div
            key={c.rule}
            className="flex gap-2 items-center text-xs border rounded-md px-2 py-1"
            style={{ borderColor: c.passed ? forge.success : c.blocking ? forge.danger : forge.warning }}
          >
            <span className="font-mono w-28 shrink-0">{c.rule}</span>
            <span className={c.passed ? "text-green-600" : "text-red-600"}>
              {c.passed ? "✓" : "✗"} {c.message}
            </span>
            {c.blocking && !c.passed && <Badge variant="destructive" className="ml-auto text-[10px]">BLOCK</Badge>}
          </div>
        ))}
        {!result && !loading && (
          <div className="text-xs text-muted-foreground">保存或选择规格后点击「校验」，查看门禁清单</div>
        )}
      </CardContent>
    </Card>
  );
}
