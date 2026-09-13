import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

function runGit(args, cwd) {
  const result = spawnSync("git", args, { cwd, encoding: "utf8" });
  if (result.status !== 0) throw new Error(`BLOCKED: git ${args[0]} failed: ${result.stderr || result.error || "unknown error"}`);
  return result.stdout.trim();
}

export function sourceSnapshot(cwd, git = args => runGit(args, cwd)) {
  return {
    branch: git(["branch", "--show-current"]),
    commitSha: git(["rev-parse", "HEAD"]),
    dirty: git(["status", "--porcelain", "--untracked-files=all"]) !== "",
  };
}

export function verifyReleaseSource(cwd, git = args => runGit(args, cwd)) {
  if (git(["branch", "--show-current"]) !== "main") {
    throw new Error("BLOCKED: desktop releases must be built from the main branch");
  }
  git(["fetch", "origin", "+refs/heads/main:refs/remotes/origin/main"]);
  const source = sourceSnapshot(cwd, git);
  const remote = git(["rev-parse", "refs/remotes/origin/main"]);
  if (source.branch !== "main" || source.commitSha !== remote || source.dirty) {
    throw new Error("BLOCKED: release requires a clean main checkout with HEAD equal to the latest origin/main");
  }
  return source;
}

export function parseMacBuildArgs(args, hostArch) {
  const accepted = new Set(["--install", "--no-install", "--debug", "--unnotarized", "--notarized"]);
  const architectures = args.filter(arg => !arg.startsWith("-"));
  if (architectures.length > 1 || args.some(arg => arg.startsWith("-") && !accepted.has(arg))) {
    throw new Error("Usage: build-macos.mjs [arm64|x86_64] [--unnotarized|--notarized] [--debug] [--install|--no-install]");
  }
  if (args.includes("--install") && args.includes("--no-install")) throw new Error("Conflicting install options");
  if (args.includes("--unnotarized") && args.includes("--notarized")) throw new Error("Conflicting signing options");
  const notarized = args.includes("--notarized");
  const debug = args.includes("--debug");
  const install = args.includes("--install");
  if (debug && install) throw new Error("BLOCKED: debug builds cannot be installed as a release");
  const requested = architectures[0] || hostArch;
  const target = ({ arm64: "aarch64-apple-darwin", aarch64: "aarch64-apple-darwin", x64: "x86_64-apple-darwin", x86_64: "x86_64-apple-darwin" })[requested];
  if (!target) throw new Error(`Unsupported macOS architecture: ${requested}`);
  return { debug, install, notarized, target, architecture: target.startsWith("x86_64") ? "x86_64" : "arm64", profile: debug ? "debug" : "release" };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const source = verifyReleaseSource(process.cwd());
    if (process.argv[2]) fs.writeFileSync(process.argv[2], `${JSON.stringify(source, null, 2)}\n`);
    console.log(JSON.stringify(source));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
