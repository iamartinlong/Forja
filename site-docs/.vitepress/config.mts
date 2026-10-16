import { defineConfig } from "vitepress";

// DOCS_BASE：GitHub Pages 项目站点需 `/Forja/`（工作流注入）；本地默认 `/`
const base = process.env.DOCS_BASE || "/";
// DOCS_PUBLIC=1：公开快照构建（排除内部运维手册 ops.md 及其导航）
const isPublic = process.env.DOCS_PUBLIC === "1";

export default defineConfig({
  lang: "zh-CN",
  base,
  title: "FORJA 文档",
  description: "FORJA 需求锻造平台 · 用户手册与运维指南",
  srcExclude: isPublic ? ["ops.md"] : [],
  themeConfig: {
    search: { provider: "local" },
    outline: { level: [2, 3], label: "本页目录" },
    docFooter: { prev: "上一篇", next: "下一篇" },
    nav: [
      { text: "快速开始", link: "/guide/quickstart" },
      { text: "用户手册", link: "/guide/handbook" },
      { text: "功能总览", link: "/features" },
      { text: "项目介绍", link: "/overview" },
      { text: "术语表", link: "/glossary" },
      { text: "API", link: "/api" },
      ...(isPublic ? [] : [{ text: "运维", link: "/ops" }])
    ],
    sidebar: [
      {
        text: "起步",
        items: [{ text: "快速开始", link: "/guide/quickstart" }]
      },
      {
        text: "用户手册",
        items: [
          { text: "按角色使用", link: "/guide/handbook" },
          { text: "概念与术语", link: "/glossary" }
        ]
      },
      {
        text: "参考",
        items: [
          { text: "API 参考", link: "/api" },
          { text: "常见问题", link: "/faq" },
          ...(isPublic ? [] : [{ text: "运维手册", link: "/ops" }])
        ]
      }
    ],
    returnToTopLabel: "回到顶部",
    sidebarMenuLabel: "菜单"
  }
});
