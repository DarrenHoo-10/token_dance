import { useEffect, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { isTauriEnvironment } from "../tauri-bridge";

type Status = { active: boolean; totalSources: number; completedSources: number };
export function RebuildData({ zh }: { zh: boolean }) {
  const [status, setStatus] = useState<Status | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    const refresh = () => {
      if (!isTauriEnvironment()) return;
      void invoke<Status>("get_rebuild_status").then(s => { if (live) setStatus(s); }).catch(() => {});
    };
    refresh(); const timer = window.setInterval(refresh, 2000);
    return () => { live = false; clearInterval(timer); };
  }, []);
  const rebuild = async () => {
    setBusy(true); setError("");
    try { setStatus(await invoke<Status>("rebuild_local_data")); setConfirm(false); }
    catch (e) { setError(String(e)); }
    finally { setBusy(false); }
  };
  return <section className="settings-section">
    <div className="settings-section-heading"><h2>{zh ? "重建数据" : "Rebuild data"}</h2></div>
    <div className="settings-sheet"><div className="settings-row"><div>
      <h3>{status?.active ? (zh ? "正在重建" : "Rebuilding") : (zh ? "重新计算本机用量" : "Recalculate device usage")}</h3>
      <p>{status?.active ? `${status.completedSources} / ${status.totalSources} ${zh ? "个来源已扫描，统计计算完成后结束" : "sources scanned; finishing statistics"}` : (zh ? "清空本机用量和采集进度，从 Agent 原始记录重新计算。登录、设备身份和设置保留；已删除的原始记录无法恢复。" : "Clear local usage and collection progress, then rescan agent records. Keep sign-in, device identity and settings. Deleted source records cannot be recovered.")}</p>
      {error && <p role="alert">{error}</p>}
    </div><button disabled={busy || status?.active || !isTauriEnvironment()} onClick={() => setConfirm(true)}>{zh ? "重建数据" : "Rebuild"}</button></div>
    {confirm && <div className="settings-row" role="group" aria-label={zh ? "确认重建" : "Confirm rebuild"}>
      <span>{busy ? (zh ? "正在结束当前任务…" : "Finishing current work…") : (zh ? "确认清空本机用量并重新计算？" : "Clear local usage and recalculate?")}</span>
      <button disabled={busy} onClick={() => void rebuild()}>{zh ? "确认重建" : "Confirm rebuild"}</button>
      <button disabled={busy} onClick={() => setConfirm(false)}>{zh ? "取消" : "Cancel"}</button>
    </div>}</div>
  </section>;
}
