// 术语单一事实源（与 site-docs/glossary.md 对齐，docUrl 指向文档站词条锚点）
/** 文档站地址：site-docs 本地预览（npm run docs:preview，默认 :4173），不进 CI/CD */
export const DOCS_BASE = "http://localhost:4173";
export interface GlossaryTerm { en: string; zh: string; desc: string; docUrl?: string }

const doc = (anchor: string) => `${DOCS_BASE}/glossary#${anchor}`;

export const GLOSSARY: Record<string, GlossaryTerm> = {
  inbox: { en: "Inbox", zh: "入堆", desc: "原始需求的收集池：粘贴文本或上传 PDF/Word，等待结构化。", docUrl: doc("inbox") },
  spec: { en: "Spec", zh: "规格", desc: "结构化、分层的需求数据，是锻造的唯一依据。", docUrl: doc("spec") },
  converge: { en: "converge", zh: "收敛", desc: "多版本 Spec 合并出最新权威版本的过程；漂移表示实现与规格不一致。", docUrl: doc("converge") },
  gate: { en: "gate", zh: "门禁", desc: "进入下一状态的评审关卡（如 go vet + go test + 覆盖率）。", docUrl: doc("gate") },
  pact: { en: "pact", zh: "契约", desc: "接口行为约定，前后端共同遵守并由契约测试守护。", docUrl: doc("pact") },
  forge: { en: "forge", zh: "锻造", desc: "Agent 依据 Spec 生成代码与测试的过程，最多 3 轮自动修复。", docUrl: doc("forge") },
  attempt: { en: "attempt", zh: "轮次", desc: "锻造重试序号；失败上下文会回喂下一轮。", docUrl: doc("attempt") },
  release_pending: { en: "RELEASE_PENDING", zh: "待签核", desc: "锻造完成等待人工发布的终审状态，仅架构师/管理员可签核。", docUrl: doc("release_pending") },
};

export function term(key: keyof typeof GLOSSARY): GlossaryTerm {
  return GLOSSARY[key];
}
