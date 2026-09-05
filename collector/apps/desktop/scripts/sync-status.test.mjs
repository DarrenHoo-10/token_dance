import assert from "node:assert/strict";
import test from "node:test";
import { syncStatusText } from "../src/sync-status.ts";

test("signed out and older backends never claim queued data was synced", () => {
  assert.equal(syncStatusText("LOGIN_REQUIRED", 1612, true), "登录后自动同步");
  assert.equal(syncStatusText(undefined, 0, true), "登录后自动同步");
});
test("new queued events override a previously synced state", () => {
  assert.equal(syncStatusText("SYNCED", 1, true), "等待自动同步");
  assert.equal(syncStatusText("SYNCED", 0, true), "已同步");
});
test("background failures, pause and expired access have distinct states", () => {
  assert.match(syncStatusText("RETRYING", 2, true), /自动重试/);
  assert.match(syncStatusText("NEEDS_ATTENTION", 2, true), /同步受阻/);
  assert.equal(syncStatusText("PAUSED", 2, false), "Sync paused");
  assert.equal(syncStatusText("SYNCING", 2, false), "Syncing");
});

test("rejected records are retained and are not reported as a connection failure", () => {
  assert.match(syncStatusText("DATA_REJECTED", 78, true), /校验未通过.*保留在本机/);
  assert.match(syncStatusText("DATA_REJECTED", 78, false), /Kept locally/);
  assert.doesNotMatch(syncStatusText("RETRYING", 78, true), /连接异常/);
});
