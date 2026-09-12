import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { sourceSnapshot } from "./build-source.mjs";

if (process.platform !== "darwin") throw new Error("Local macOS testing requires a Mac");
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = sourceSnapshot(root);
const env = { ...process.env, MACOSX_DEPLOYMENT_TARGET: "13.0", TOKENDANCE_LOCAL_TEST_BUILD: "1", VITE_TOKENDANCE_LOCAL_TEST: "1" };
const build = spawnSync(path.join(root, "node_modules/.bin/tauri"), [
  "build", "--debug", "--features", "custom-protocol", "--bundles", "app",
  "--config", "src-tauri/tauri.local-test.conf.json", "--no-sign", "--ci", "--", "--locked",
], { cwd: root, env, stdio: "inherit" });
if (build.status !== 0) process.exit(build.status ?? 1);
const bundle = path.join(root, "src-tauri/target/debug/bundle/macos/TokenDance Test.app");
const destination = path.join(root, "release/TokenDance Test.app");
if (!fs.existsSync(bundle)) throw new Error(`Test bundle missing: ${bundle}`);
fs.mkdirSync(path.dirname(destination), { recursive: true });
fs.rmSync(destination, { recursive: true, force: true });
fs.cpSync(bundle, destination, { recursive: true });
const executable = path.join(destination, "Contents/MacOS/tokendance-desktop");
fs.writeFileSync(path.join(root, "release/local-test-build-info.json"), JSON.stringify({
  ...source, profile: "debug", localTest: true, builtAt: new Date().toISOString(),
  bundleId: "io.tokendance.desktop.local-test", app: destination,
  sha256: createHash("sha256").update(fs.readFileSync(executable)).digest("hex"),
}, null, 2) + "\n");
console.log(`Local test ready: ${destination}`);
