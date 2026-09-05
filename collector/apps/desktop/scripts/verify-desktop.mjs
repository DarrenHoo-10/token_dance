import fs from "node:fs";
import path from "node:path";
import { execSync } from "node:child_process";

const desktopRoot = path.resolve(".");
const srcTauriRoot = path.join(desktopRoot, "src-tauri");

console.log("==================================================");
console.log("  TokenDance Desktop Settings Shell Verification");
console.log("==================================================");

let hasErrors = false;
function assert(condition, message) {
  if (!condition) {
    console.error(`❌ [FAIL] ${message}`);
    hasErrors = true;
  } else {
    console.log(`✓ [PASS] ${message}`);
  }
}

// 1. Verify Tauri 2 Configuration
console.log("\n[1/5] Verifying Tauri 2 Configuration...");
const tauriConfPath = path.join(srcTauriRoot, "tauri.conf.json");
assert(fs.existsSync(tauriConfPath), "tauri.conf.json exists");

const tauriConf = JSON.parse(fs.readFileSync(tauriConfPath, "utf-8"));
assert(tauriConf.$schema?.includes("schema.tauri.app/config/2"), "Tauri v2 schema URL is used");
assert(tauriConf.productName === "tokendance-desktop", "Product name configured");
assert(tauriConf.identifier === "io.tokendance.desktop", "Bundle identifier configured");
assert(tauriConf.app?.windows?.[0]?.label === "main", "Main window defined");
assert(tauriConf.app?.trayIcon?.id === "main-tray", "Tray icon configured for persistent background daemon");
assert(tauriConf.build?.frontendDist === "../dist", "Frontend dist target points to ../dist");

const capPath = path.join(srcTauriRoot, "capabilities", "default.json");
assert(fs.existsSync(capPath), "Capabilities config exists");
const capConf = JSON.parse(fs.readFileSync(capPath, "utf-8"));
assert(capConf.identifier === "default", "Default capability defined");

// 2. Verify Command Boundary Parity between Rust and TypeScript
console.log("\n[2/5] Verifying Tauri IPC Command Boundary Parity...");
const libRsPath = path.join(srcTauriRoot, "src", "lib.rs");
assert(fs.existsSync(libRsPath), "src/lib.rs exists");
const libRsContent = fs.readFileSync(libRsPath, "utf-8");

const tauriBridgePath = path.join(desktopRoot, "src", "tauri-bridge.ts");
assert(fs.existsSync(tauriBridgePath), "src/tauri-bridge.ts exists");
const bridgeContent = fs.readFileSync(tauriBridgePath, "utf-8");

const requiredCommands = [
  "get_daemon_status",
  "toggle_global_pause",
  "set_global_pause",
  "get_collector_metrics",
  "get_agent_configs",
  "toggle_agent",
  "set_agent_status",
  "preview_upload_batch",
  "trigger_sync_now",
  "get_pending_envelopes",
  "create_config_backup",
  "restore_config_backup",
  "list_config_backups",
  "list_devices",
  "revoke_device",
  "request_data_deletion",
  "purge_local_cache",
  "get_autostart_status",
  "set_autostart",
  "hide_window",
  "show_window",
  "quit_app",
  "open_settings",
  "open_website",
];

for (const cmd of requiredCommands) {
  const registeredInRust = libRsContent.includes(cmd);
  const referencedInTs = bridgeContent.includes(`"${cmd}"`) || bridgeContent.includes(`'${cmd}'`);
  assert(registeredInRust, `Rust registered handler: ${cmd}`);
  assert(referencedInTs, `TS bridge invokes: ${cmd}`);
}

// 3. Verify Lifecycle & Autostart Implementations
console.log("\n[3/5] Verifying Lifecycle & Autostart Logic...");
assert(
  libRsContent.includes("WindowEvent::CloseRequested") &&
    libRsContent.includes("prevent_close") &&
    libRsContent.includes("hide()"),
  "Window close interception (prevent exit, hide to system tray)"
);
assert(libRsContent.includes("TrayIconBuilder"), "System tray builder and click events configured");
assert(libRsContent.includes("CollectorDaemon"), "Continuous background daemon spawned");

const autostartModPath = path.join(srcTauriRoot, "src", "autostart", "mod.rs");
assert(fs.existsSync(autostartModPath), "autostart/mod.rs exists");
const autostartContent = fs.readFileSync(autostartModPath, "utf-8");
assert(
  autostartContent.includes("HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run"),
  "Windows user-level HKCU Run autostart implementation"
);
assert(
  autostartContent.includes("LaunchAgents") && autostartContent.includes("RunAtLoad"),
  "macOS user-level LaunchAgents plist autostart implementation"
);

// 4. Verify Frontend Components & UX Reusability
console.log("\n[4/5] Verifying Frontend Components...");
const components = [
  "DaemonStatusCard.tsx",
  "AgentsMatrixCard.tsx",
  "UploadPreviewCard.tsx",
  "AutostartLifecycleCard.tsx",
  "DevicesRevokeCard.tsx",
  "ConfigBackupRestoreCard.tsx",
  "DataDeletionCard.tsx",
];

for (const comp of components) {
  const compPath = path.join(desktopRoot, "src", "components", comp);
  assert(fs.existsSync(compPath), `Component ${comp} exists`);
}

// 5. Run Rust Unit Tests (Platform-Independent)
console.log("\n[5/5] Executing Rust Unit Tests via Cargo...");
try {
  const cargoBinPath = process.env.USERPROFILE ? `${process.env.USERPROFILE}\\.cargo\\bin` : "";
  const env = { ...process.env, PATH: `${process.env.PATH};${cargoBinPath}` };
  const cargoOutput = execSync("cargo test -- --nocapture", {
    cwd: srcTauriRoot,
    env,
    encoding: "utf-8",
  });
  console.log(cargoOutput);
  assert(/test result: ok\. [1-9]\d* passed; 0 failed/.test(cargoOutput), "Service-backed Rust unit tests passed");
} catch (err) {
  console.error("Cargo test error:", err);
  hasErrors = true;
}

console.log("\n--------------------------------------------------");
if (hasErrors) {
  console.error("❌ Verification failed with errors.");
  process.exit(1);
} else {
  console.log("🎉 All desktop settings shell verifications PASSED!");
  process.exit(0);
}
