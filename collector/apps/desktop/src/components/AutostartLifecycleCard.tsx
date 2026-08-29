import React from "react";
import type { AutostartInfo } from "../tauri-bridge.ts";

interface AutostartLifecycleCardProps {
  autostartInfo: AutostartInfo | null;
  onToggleAutostart: (enabled: boolean) => void;
  lang: "zh" | "en";
}

export const AutostartLifecycleCard: React.FC<AutostartLifecycleCardProps> = ({
  autostartInfo,
  onToggleAutostart,
  lang,
}) => {
  const isZh = lang === "zh";

  return (
    <div className="card-section">
      <div className="section-head">
        <div>
          <h2>{isZh ? "开机自启动与后台常驻生命周期" : "Autostart & Background Lifecycle"}</h2>
          <p>
            {isZh
              ? "配置 Windows (注册表 Run 项) / macOS (LaunchAgents plist) 用户级静默自启；关闭窗口保持常驻"
              : "User-level autostart via Windows Registry or macOS LaunchAgents. Background service runs continuously"}
          </p>
        </div>
      </div>

      <div className="lifecycle-grid">
        <div className="feature-card">
          <div className="feature-header">
            <div className="feature-title-row">
              <h3>{isZh ? "用户级开机自启" : "User-Level Autostart"}</h3>
              <span className="tag tag-lime">
                {autostartInfo?.platform === "windows"
                  ? "Windows (HKCU)"
                  : autostartInfo?.platform === "macos"
                    ? "macOS (LaunchAgents)"
                    : "Linux (XDG)"}
              </span>
            </div>
            <label className="switch">
              <input
                type="checkbox"
                checked={autostartInfo?.enabled ?? true}
                onChange={(e) => onToggleAutostart(e.target.checked)}
              />
              <span className="slider round"></span>
            </label>
          </div>

          <p className="feature-desc">
            {isZh
              ? "在登录用户会话启动时以最小化/后台守护模式启动 TokenDance 采集器，无需管理员 UAC 提权。"
              : "Starts TokenDance Collector silently on user login in minimized mode. Requires no administrator UAC elevation."}
          </p>

          <div className="config-box">
            <span className="config-label">{isZh ? "目标注册配置路径:" : "Configuration Target:"}</span>
            <code className="config-val font-mono">{autostartInfo?.targetPath}</code>
          </div>
          <div className="config-box" style={{ marginTop: "8px" }}>
            <span className="config-label">{isZh ? "启动指令明细:" : "Execution Details:"}</span>
            <code className="config-val font-mono">{autostartInfo?.details}</code>
          </div>
        </div>

        <div className="feature-card">
          <div className="feature-header">
            <div className="feature-title-row">
              <h3>{isZh ? "关闭窗口不退出后台采集" : "Window Close Behavior"}</h3>
              <span className="tag tag-purple">{isZh ? "托盘常驻守护" : "System Tray Persistent"}</span>
            </div>
          </div>

          <p className="feature-desc">
            {isZh
              ? "点击窗口右上角的关闭按钮 (×) 只会隐藏主设置窗口，后台采集线程与适配器继续正常监听。如需彻底退出，请通过托盘菜单右键选择「退出程序」。"
              : "Clicking the window close (×) button hides the window into the system tray without terminating background collection. Use Tray -> Quit to terminate."}
          </p>

          <div className="tray-shortcuts">
            <div className="shortcut-item">
              <span className="shortcut-key">左键点击托盘图标</span>
              <span className="shortcut-desc">{isZh ? "快速呼出 / 隐藏设置面板" : "Toggle Settings Window"}</span>
            </div>
            <div className="shortcut-item">
              <span className="shortcut-key">右键托盘菜单</span>
              <span className="shortcut-desc">{isZh ? "一键暂停采集 / 立即同步 / 彻底退出" : "Pause / Sync / Quit App"}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};
