import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseMacBuildArgs, sourceSnapshot, verifyReleaseSource } from "./build-source.mjs";

const desktopRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.chdir(desktopRoot);

let options;
let source;
try {
  options = parseMacBuildArgs(process.argv.slice(2), process.arch);
  if (process.platform !== "darwin") throw new Error("macOS builds require a macOS host");
  source = options.debug ? sourceSnapshot(desktopRoot) : verifyReleaseSource(desktopRoot);
} catch (error) {
  console.error(error.message);
  process.exit(1);
}
const { target: rustTarget, architecture: outArch, install: installToApplications, profile } = options;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    stdio: "inherit",
    env: { ...process.env, MACOSX_DEPLOYMENT_TARGET: "13.0" },
    ...options,
  });
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
  return result;
}

function capture(command, args) {
  const result = spawnSync(command, args, { encoding: "utf8" });
  return {
    status: result.status ?? 1,
    stdout: result.stdout || "",
    stderr: result.stderr || "",
  };
}

function sha256(file) {
  const hash = createHash("sha256");
  hash.update(fs.readFileSync(file));
  return hash.digest("hex");
}

function listedSigningIdentities() {
  const listed = capture("security", ["find-identity", "-v", "-p", "codesigning"]);
  return [...listed.stdout.matchAll(/^\s*\d+\)\s+[A-F0-9]+\s+"([^"]+)"/gm)].map(
    (match) => match[1],
  );
}

function resolveSigningIdentity() {
  if (options.debug || process.env.CI === "true" || process.env.SKIP_MACOS_SIGN === "1") {
    return null;
  }
  const names = listedSigningIdentities();
  const requestedIdentity =
    process.env.APPLE_SIGNING_IDENTITY || process.env.DEVELOPER_ID_APPLICATION;
  if (requestedIdentity) {
    if (!names.includes(requestedIdentity)) {
      console.error(
        `BLOCKED: signing identity not in keychain: ${requestedIdentity}`,
      );
      process.exit(78);
    }
    return requestedIdentity;
  }
  return (
    names.find((name) => name.startsWith("Developer ID Application:")) ||
    names.find((name) => name.startsWith("Apple Development:")) ||
    null
  );
}

function installSignedApp(staged) {
  verifyReleaseSource(desktopRoot);
  const installed = "/Applications/TokenDance.app";
  const temporary = `/Applications/.TokenDance-${process.pid}.app`;
  const backup = `/Applications/.TokenDance-${process.pid}.previous.app`;
  run("ditto", [staged, temporary]);
  run("codesign", ["--verify", "--deep", "--strict", temporary]);
  spawnSync("osascript", ["-e", 'if application "TokenDance" is running then tell application "TokenDance" to quit'], {
    stdio: "ignore",
  });
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 800);
  const previousExists = fs.existsSync(installed);
  if (previousExists) fs.renameSync(installed, backup);
  try {
    fs.renameSync(temporary, installed);
  } catch (error) {
    if (previousExists) fs.renameSync(backup, installed);
    throw error;
  }
  if (previousExists) fs.rmSync(backup, { recursive: true });
  console.log(`installed: ${installed}`);
  spawnSync("open", [installed], { stdio: "ignore" });
}

run("npx", [
  "tauri",
  "build",
  "--target",
  rustTarget,
  "--features",
  options.notarized ? "custom-protocol" : "custom-protocol,unnotarized-distribution",
  "--bundles",
  "app",
  "--config",
  "src-tauri/tauri.macos.conf.json",
  ...(!options.notarized ? ["--config", JSON.stringify({bundle:{macOS:{infoPlist:"macos-unnotarized-info.plist"}}})] : []),
  "--ci",
  "--no-sign",
  ...(options.debug ? ["--debug"] : []),
  "--",
  "--locked",
]);

const appPath = path.join(
  desktopRoot,
  "src-tauri/target",
  rustTarget,
  `${profile}/bundle/macos/TokenDance.app`,
);
if (!fs.existsSync(appPath)) {
  console.error(`BLOCKED: Tauri did not produce ${appPath}`);
  process.exit(1);
}

const config = JSON.parse(fs.readFileSync(path.join(desktopRoot, "src-tauri/tauri.conf.json"), "utf8"));
const version = config.version;
const releaseDir = path.join(desktopRoot, "release");
fs.mkdirSync(releaseDir, { recursive: true });
const staged = path.join(releaseDir, `TokenDance-${version}-macos-${outArch}${options.debug ? "-debug" : ""}.app`);
fs.rmSync(staged, { recursive: true, force: true });
fs.cpSync(appPath, staged, { recursive: true });

const executable = capture("plutil", ["-extract", "CFBundleExecutable", "raw", "-o", "-", path.join(staged, "Contents/Info.plist")]);
const executableName = executable.stdout.trim();
const binary = path.join(staged, "Contents/MacOS", executableName);
if (executable.status !== 0 || path.basename(executableName) !== executableName || !executableName || !fs.existsSync(binary)) {
  console.error("BLOCKED: bundle does not contain its declared executable");
  process.exit(1);
}
run("lipo", [binary, "-verify_arch", outArch]);

const identity = options.notarized ? resolveSigningIdentity() : null;
let notarizedInfo = null;
if (!options.debug && !options.notarized) {
  const dmg = path.join(releaseDir, `TokenDance-${version}-macos-${outArch}-unnotarized.dmg`);
  run("python3", [path.resolve(desktopRoot, "../../packaging/macos/package-unnotarized.py"), staged, dmg]);
  notarizedInfo = JSON.parse(fs.readFileSync(dmg.replace(/\.dmg$/, ".build-info.json"), "utf8"));
  if (installToApplications) installSignedApp(staged);
} else if (!options.debug && process.env.CI !== "true") {
  if (!identity?.startsWith("Developer ID Application:")) {
    throw new Error("BLOCKED: a Developer ID Application identity is required for a public DMG");
  }
  if (!process.env.APPLE_NOTARY_PROFILE) throw new Error("BLOCKED: APPLE_NOTARY_PROFILE is required to notarize the DMG");
  const dmg = path.join(releaseDir, `TokenDance-${version}-macos-${outArch}.dmg`);
  if (fs.existsSync(dmg)) throw new Error(`Output already exists: ${dmg}`);
  run("bash", [path.resolve(desktopRoot, "../../packaging/macos/sign-notarize.sh"), staged, dmg], {
    env: { ...process.env, DEVELOPER_ID_APPLICATION: identity },
  });
  notarizedInfo = JSON.parse(fs.readFileSync(dmg.replace(/\.dmg$/, ".build-info.json"), "utf8"));
  if (installToApplications) installSignedApp(staged);
} else if (installToApplications) {
  throw new Error("BLOCKED: only a notarized local release can be installed");
}

const info = {
  ...source,
  profile,
  builtAt: new Date().toISOString(),
  version,
  architecture: outArch,
  rustTarget,
  app: path.basename(staged),
  executable: path.basename(binary),
  sha256: sha256(binary),
  bundleId: "io.tokendance.desktop",
  minimumSystemVersion: "13.0",
  ...(notarizedInfo ?? { notarized: false }),
};
const serialized = `${JSON.stringify(info, null, 2)}\n`;
fs.writeFileSync(path.join(releaseDir, `build-info-${outArch}.json`), serialized);
fs.writeFileSync(path.join(releaseDir, "build-info.json"), serialized);
console.log(`macOS app ready: ${staged}`);
