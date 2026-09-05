import { useState } from "react";
import { getWebsiteUrl, saveWebsiteUrl } from "../tauri-bridge";

export function WebsiteSettingsCard({ lang }: { lang: "zh" | "en" }) {
  const [url, setUrl] = useState(getWebsiteUrl);
  const [message, setMessage] = useState("");
  const zh = lang === "zh";
  return <section className="card" style={{ padding:24, marginBottom:20 }}>
    <h2 style={{ fontSize:18, marginBottom:8 }}>{zh ? "网站主页" : "Website home"}</h2>
    <p style={{ color:"var(--text-muted)", marginBottom:14 }}>{zh ? "托盘右下角打开官网。未登录去登录页，已登录去首页；登录后本机保存一个月凭证。可留空使用默认网站。" : "The tray link opens the official site. Sign-in if needed, otherwise the homepage. This machine keeps the session for one month. Leave blank to use the default site."}</p>
    <form onSubmit={event => { event.preventDefault(); try { saveWebsiteUrl(url.trim()); setMessage(zh ? "网站地址已保存" : "Website URL saved"); } catch (error) { setMessage(String(error)); } }} style={{ display:"flex", gap:10 }}>
      <input type="url" aria-label={zh ? "网站主页地址" : "Website URL"} placeholder="http://127.0.0.1:3000" value={url} onChange={event => setUrl(event.target.value)} style={{ flex:1, minWidth:0, padding:10, borderRadius:8, border:"1px solid var(--border)", background:"var(--bg-base)", color:"var(--text-main)" }} />
      <button className="header-btn" type="submit">{zh ? "保存" : "Save"}</button>
    </form>
    {message && <p role="status" style={{ marginTop:10 }}>{message}</p>}
  </section>;
}
