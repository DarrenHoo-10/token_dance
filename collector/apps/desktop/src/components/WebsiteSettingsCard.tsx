import { useState } from "react";
import { getWebsiteUrl, saveWebsiteUrl } from "../tauri-bridge";

export function WebsiteSettingsCard({ lang }: { lang: "zh" | "en" }) {
  const [url, setUrl] = useState(getWebsiteUrl);
  const [message, setMessage] = useState("");
  const zh = lang === "zh";
  return <section className="card" style={{ padding:24, marginBottom:20 }}>
    <h2 style={{ fontSize:18, marginBottom:8 }}>{zh ? "网站主页" : "Website home"}</h2>
    <p style={{ color:"var(--text-muted)", marginBottom:14 }}>{zh ? "托盘右下角的入口会使用默认浏览器打开此地址，前往网站查看排名。" : "The bottom-right tray link opens this address in your default browser to view rankings."}</p>
    <form onSubmit={event => { event.preventDefault(); try { saveWebsiteUrl(url.trim()); setMessage(zh ? "主页地址已保存" : "Website URL saved"); } catch (error) { setMessage(String(error)); } }} style={{ display:"flex", gap:10 }}>
      <input type="url" required aria-label={zh ? "网站主页地址" : "Website URL"} placeholder="https://…" value={url} onChange={event => setUrl(event.target.value)} style={{ flex:1, minWidth:0, padding:10, borderRadius:8, border:"1px solid var(--border)", background:"var(--bg-base)", color:"var(--text-main)" }} />
      <button className="header-btn" type="submit">{zh ? "保存" : "Save"}</button>
    </form>
    {message && <p role="status" style={{ marginTop:10 }}>{message}</p>}
  </section>;
}
