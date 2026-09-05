import { useEffect, useRef, useState } from "react";
import { accountWebsite, getAccountSession, loginAccount, logoutAccount, openAccountWebsite } from "./account-bridge";
import type { AccountUser } from "./account-bridge";

export function DesktopAccountCard({ zh }: { zh: boolean }) {
  const [user, setUser] = useState<AccountUser | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const requestVersion = useRef(0);
  const working = useRef(false);
  const t = (cn: string, en: string) => zh ? cn : en;
  const errorText = (code: string) => {
    if (code.includes("INVALID_CREDENTIALS")) return t("邮箱或密码不正确，请重试。", "Incorrect email or password. Try again.");
    if (code.includes("TOO_MANY_ATTEMPTS")) return t("尝试次数较多，请稍后再试。", "Too many attempts. Please try again later.");
    if (code.includes("HTTPS_REQUIRED") || code.includes("INVALID_WEBSITE")) return t("当前网站地址不支持安全登录，请联系支持。", "This website address does not support secure sign-in. Contact support.");
    if (code.includes("PREVIEW_ONLY")) return t("这是界面预览，请在 TokenDance 桌面应用内登录。", "This is a preview. Sign in inside the TokenDance desktop app.");
    if (code.includes("ACCOUNT_FORBIDDEN")) return t("当前账号无法登录，请到网站检查账号状态。", "Unable to sign in. Check your account on the website.");
    return t("暂时无法连接账号服务，请检查网络后重试。", "Unable to reach the account service. Check your network and retry.");
  };

  useEffect(() => {
    const current = ++generation.current;
    setUser(null); setError(""); setPassword(""); setChecking(true);
    const refresh = async () => {
      if (document.hidden || working.current) return;
      const version = ++requestVersion.current;
      try {
        const session = await getAccountSession(accountWebsite());
        if (generation.current === current && requestVersion.current === version) { setUser(session.user); setError(""); }
      } catch (err) { if (generation.current === current && requestVersion.current === version) setError(String(err)); }
      finally { if (generation.current === current) setChecking(false); }
    };
    void refresh();
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refresh);
    return () => { generation.current++; window.removeEventListener("focus", refresh); document.removeEventListener("visibilitychange", refresh); };
  }, []);

  const submit = async (logout = false) => {
    if (working.current) return;
    working.current = true; setBusy(true); setError("");
    requestVersion.current++;
    const current = generation.current;
    try {
      const website = accountWebsite();
      const session = logout ? (await logoutAccount(website), { user: null }) : await loginAccount(website, email, password);
      if (generation.current === current) { setUser(session.user); setExpanded(false); }
    } catch (err) { if (generation.current === current) setError(String(err)); }
    finally { setPassword(""); working.current = false; setBusy(false); setChecking(false); }
  };
  const go = (path: "/register" | "/forgot-password" | "/onboarding") => { void openAccountWebsite(path).catch(err => setError(String(err))); };

  return <section className="settings-account" aria-label={t("账号", "Account")}>
    <div className="settings-account-summary">
      <span className={`settings-account-avatar ${user ? "signed-in" : ""}`} aria-hidden="true">{user ? Array.from(user.displayName || user.handle || "T")[0].toUpperCase() : "T"}</span>
      <div className="settings-account-identity"><h2>{user ? user.displayName || user.handle : t("登录 TokenDance", "Sign in to TokenDance")}</h2><p>{user ? user.handle ? `@${user.handle}` : t("已登录", "Signed in") : t("已有账号可直接在这里登录", "Use your existing account here")}</p></div>
      <div className="settings-account-actions">{user ? <button disabled={busy} onClick={() => void submit(true)}>{busy ? t("退出中…", "Signing out…") : t("退出登录", "Sign out")}</button> : <><button disabled={busy} onClick={() => go("/register")}>{t("注册", "Register")} <span aria-hidden="true">↗</span></button><button className="settings-account-primary" disabled={busy || checking} aria-expanded={expanded} aria-controls="desktop-login-form" onClick={() => { setExpanded(!expanded); setError(""); setPassword(""); }}>{checking ? t("连接中…", "Connecting…") : expanded ? t("收起", "Close") : t("登录", "Sign in")}</button></>}</div>
    </div>
    {expanded && !user && <form id="desktop-login-form" className="settings-login-form" onSubmit={event => { event.preventDefault(); void submit(); }}>
      <label htmlFor="desktop-email">{t("邮箱", "Email")}<input id="desktop-email" name="email" type="email" autoComplete="username" placeholder="you@example.com" required autoFocus disabled={busy} value={email} onChange={event => setEmail(event.target.value)} /></label>
      <label htmlFor="desktop-password">{t("密码", "Password")}<input id="desktop-password" name="password" type="password" autoComplete="current-password" placeholder={t("输入密码", "Enter your password")} required disabled={busy} value={password} onChange={event => setPassword(event.target.value)} /></label>
      <div className="settings-login-bottom"><button type="button" onClick={() => go("/forgot-password")}>{t("忘记密码", "Forgot password?")} <span aria-hidden="true">↗</span></button><button type="submit" className="settings-account-primary" disabled={busy}>{busy ? t("登录中…", "Signing in…") : t("登录", "Sign in")}</button></div>
    </form>}
    {user?.onboardingRequired && <div className="settings-account-note">{t("到网站完善资料，开始使用排行榜。", "Complete your profile on the website to use rankings.")}<button onClick={() => go("/onboarding")}>{t("完善资料 ↗", "Complete profile ↗")}</button></div>}
    {error && <p className="settings-account-error" role="alert">{errorText(error)}</p>}
  </section>;
}
