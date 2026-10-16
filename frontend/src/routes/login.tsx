import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { Anvil, AlertCircle, FileText, Flame } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Auth } from "@/lib/api"
import { ApiError, consumeRedirect } from "@/lib/http"

export default function Login() {
  const navigate = useNavigate()
  const [remember, setRemember] = useState(() => localStorage.getItem("forja_remember_user") === "1")
  const [u, setU] = useState(() => (localStorage.getItem("forja_remember_user") === "1" ? localStorage.getItem("forja_username") || "" : ""))
  const [p, setP] = useState("")
  const [msg, setMsg] = useState("")

  const login = async () => {
    try {
      const r: any = await Auth.login(u, p)
      if (remember) {
        localStorage.setItem("forja_remember_user", "1")
        localStorage.setItem("forja_username", u)
      } else {
        localStorage.removeItem("forja_remember_user")
        localStorage.removeItem("forja_username")
      }
      localStorage.setItem("forja_token", r.token)
      // S1.2：保存 refresh token 供 401 静默续期
      if (r.refresh_token) localStorage.setItem("forja_refresh", r.refresh_token)
      localStorage.setItem("forja_role", r.role)
      localStorage.setItem("forja_user", r.username)
      navigate({ to: consumeRedirect() || "/inbox" })
    } catch (e: any) {
      // 禁用账户（密码正确后才告知）：FORBIDDEN/ACCOUNT_DISABLED 优先于通用错密提示
      if (e?.errCode === "ACCOUNT_DISABLED" || e?.detail?.includes("ACCOUNT_DISABLED") || e?.message?.includes("ACCOUNT_DISABLED")) {
        setMsg("账户已被禁用，请联系管理员")
      } else if (e?.bizCode === 40005 || e?.errCode === "UNAUTHORIZED") {
        setMsg("用户名或密码错误")
      } else if (e instanceof ApiError && e.code === 0) {
        setMsg("网络连接失败，请检查网络后重试")
      } else {
        setMsg(e?.detail || e?.message || "登录失败，请稍后重试")
      }
    }
  }

  return (
    <div className="grid min-h-svh lg:grid-cols-2">
      <div className="relative hidden overflow-hidden bg-forge-anvil lg:block">
        <div className="absolute inset-0 bg-gradient-to-br from-forge-anvil via-forge-ember-deep to-forge-ember opacity-90" />
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-5 text-white/90 px-8">
          <Flame className="size-16 drop-shadow-lg" />
          <span className="font-bold tracking-widest text-2xl">FORJA</span>
          <span className="text-sm text-white/60">源自西班牙语"锻造炉" · 炉火不灭，锻造不止</span>
          <div className="flex items-center gap-3 w-full max-w-xs mt-4">
            <div className="flex-1 h-px bg-white/20" />
            <span className="text-xs tracking-[0.3em] text-white/50">三条铁律</span>
            <div className="flex-1 h-px bg-white/20" />
          </div>
          <div className="flex flex-col gap-3 w-full max-w-xs">
            {[
              { icon: FileText, title: "图纸即契约", desc: "没有图纸不开炉" },
              { icon: Flame, title: "炉火即验证", desc: "不经淬炼不出坊" },
              { icon: Anvil, title: "铁砧即纪律", desc: "每一锤都可追溯" },
            ].map(({ icon: Icon, title, desc }) => (
              <div key={title} className="flex items-center gap-3">
                <Icon className="size-4 shrink-0 text-white/70" />
                <span className="text-sm text-white/90">{title}</span>
                <span className="text-xs text-white/50">· {desc}</span>
              </div>
            ))}
          </div>
          <p className="italic text-sm text-white/80 mt-4">你只管定蓝图，FORJA 负责铸交付</p>
        </div>
      </div>
      <div className="flex flex-col gap-4 p-6 md:p-10">
        <div className="flex justify-center gap-2 md:justify-start">
          <a href="#" onClick={e => e.preventDefault()} className="flex items-center gap-2 font-medium">
            <div className="flex size-6 items-center justify-center rounded-md bg-primary text-primary-foreground">
              <Flame className="size-4" />
            </div>
            FORJA
          </a>
        </div>
        <div className="flex flex-1 items-center justify-center">
          <div className="w-full max-w-xs">
            <form
              className="flex flex-col gap-6"
              onSubmit={e => { e.preventDefault(); login() }}
            >
              <FieldGroup>
                <div className="flex flex-col items-center gap-1 text-center">
                  <h1 className="text-2xl font-bold">登录 FORJA</h1>
                  <p className="text-sm text-balance text-muted-foreground">
                    规格入炉，交付出炉
                  </p>
                </div>
                <Field>
                  <FieldLabel htmlFor="username">用户名</FieldLabel>
                  <Input id="username" placeholder="admin" required autoFocus value={u} onChange={e => setU(e.target.value)} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="password">密码</FieldLabel>
                  <Input id="password" type="password" required value={p} onChange={e => setP(e.target.value)} />
                </Field>
                <Field orientation="horizontal">
                  <Checkbox id="remember" checked={remember} onCheckedChange={v => setRemember(v === true)} />
                  <FieldLabel htmlFor="remember" className="font-normal">记住用户名</FieldLabel>
                </Field>
                <Field>
                  <Button type="submit" variant="forge" className="w-full">登录</Button>
                  {msg && (
                    <Alert variant="destructive">
                      <AlertCircle className="h-4 w-4" />
                      <AlertDescription>{msg}</AlertDescription>
                    </Alert>
                  )}
                </Field>
                <FieldDescription className="text-center">
                  演示账号：admin/admin · architect/architect · dev/dev · viewer/viewer
                </FieldDescription>
              </FieldGroup>
            </form>
          </div>
        </div>
      </div>
    </div>
  )
}
