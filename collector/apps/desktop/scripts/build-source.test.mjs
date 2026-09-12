import assert from "node:assert/strict";
import test from "node:test";
import { parseMacBuildArgs, verifyReleaseSource } from "./build-source.mjs";

function fakeGit({ branch = "main", head = "a".repeat(40), remote = head, status = "" } = {}) {
  const calls = [];
  const run = args => {
    calls.push(args);
    if (args[0] === "branch") return branch;
    if (args[0] === "fetch") return "";
    if (args[0] === "status") return status;
    if (args[1] === "HEAD") return head;
    if (args[1] === "refs/remotes/origin/main") return remote;
    throw new Error(`unexpected command ${args}`);
  };
  return { run, calls };
}

test("release refreshes origin/main and records the full verified commit", () => {
  const git = fakeGit();
  assert.deepEqual(verifyReleaseSource("unused", git.run), { branch: "main", commitSha: "a".repeat(40), dirty: false });
  assert.ok(git.calls.find(args => args[0] === "fetch"));
});

for (const [name, state] of Object.entries({ feature: { branch: "feature/test" }, detached: { branch: "" }, stale: { remote: "b".repeat(40) }, dirty: { status: " M source.rs" }, untracked: { status: "?? secret.txt" } })) {
  test(`release rejects ${name} source`, () => assert.throws(() => verifyReleaseSource("unused", fakeGit(state).run), /BLOCKED/));
}

test("release stops if fetching latest origin/main fails", () => {
  assert.throws(() => verifyReleaseSource("unused", args => { if (args[0] === "branch") return "main"; throw new Error("fetch unavailable"); }), /fetch unavailable/);
});

test("macOS builds select exact architectures and never install by default", () => {
  assert.deepEqual(parseMacBuildArgs(["x86_64", "--no-install"], "arm64"), { debug: false, install: false, notarized: false, target: "x86_64-apple-darwin", architecture: "x86_64", profile: "release" });
  assert.equal(parseMacBuildArgs([], "arm64").install, false);
  assert.equal(parseMacBuildArgs(["arm64", "--debug"], "x64").profile, "debug");
});

test("invalid or ambiguous build requests fail before executing a build", () => {
  for (const args of [["riscv"], ["--target", "x64"], ["arm64", "x64"], ["--install", "--no-install"], ["--debug", "--install"], ["--unnotarized", "--notarized"]]) {
    assert.throws(() => parseMacBuildArgs(args, "arm64"));
  }
});

test("free macOS distribution is the default; notarization is explicit", () => {
  assert.equal(parseMacBuildArgs([], "arm64").notarized, false);
  assert.equal(parseMacBuildArgs(["--unnotarized"], "arm64").notarized, false);
  assert.equal(parseMacBuildArgs(["--notarized"], "arm64").notarized, true);
});
