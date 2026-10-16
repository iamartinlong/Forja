import { z } from "zod"

// 密码策略（S1.3）：与后端 auth.ValidatePasswordStrength 严格对齐
// —— ≥8 位、同时包含字母与数字、不含空格。双侧规则变更须同步。
export const passwordSchema = z
  .string()
  .min(8, "密码至少 8 位")
  .regex(/[A-Za-z]/, "密码须包含字母")
  .regex(/\d/, "密码须包含数字")
  .refine(v => !v.includes(" "), "密码不能包含空格")

// 编辑表单可选密码：留空表示不修改
export const optionalPasswordSchema = z
  .string()
  .optional()
  .refine(v => !v || passwordSchema.safeParse(v).success, "密码至少 8 位且须包含字母和数字")
