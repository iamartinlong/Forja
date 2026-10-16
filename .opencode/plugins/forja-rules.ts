import type { Plugin } from "@opencode-ai/plugin"
export const ForjaRules: Plugin = async ({ client }) => ({
  "tool.execute.before": async (input: any, output: any) => {
    if (input.tool === "write" && output.args?.filePath?.includes(".env")) {
      throw new Error("FORJA: 禁止写 .env")
    }
  },
  event: async ({ event }: any) => {
    if (event.type === "session.idle") {
      await client.app.log({ body: { service: "forja", level: "info", message: "session idle", extra: { event } } })
    }
  },
})
