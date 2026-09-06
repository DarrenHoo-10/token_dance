import test from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_WEBSITE_ORIGIN,
  parseWebsiteOrigin,
  resolveWebsiteOrigin,
  websiteHomeUrl,
  websiteLoginUrl,
  websitePageUrl,
  websiteAvatarUrl,
} from "../src/website.ts";

test("empty stored URL falls back to the built-in origin", () => {
  assert.equal(resolveWebsiteOrigin(""), DEFAULT_WEBSITE_ORIGIN);
  assert.equal(resolveWebsiteOrigin("   "), DEFAULT_WEBSITE_ORIGIN);
  assert.equal(resolveWebsiteOrigin(null), DEFAULT_WEBSITE_ORIGIN);
});

test("the tray button opens the site home; login is only used with an explicit return path", () => {
  assert.equal(parseWebsiteOrigin("https://tokendance.example/app"), "https://tokendance.example/app");
  assert.equal(websiteHomeUrl("https://tokendance.example"), "https://tokendance.example/");
  assert.equal(websiteHomeUrl(""), "https://www.nexorai.com.cn/token-dance/");
  assert.equal(
    websiteLoginUrl("http://127.0.0.1:5173", "/settings/profile"),
    "http://127.0.0.1:5173/login?return_to=%2Fsettings%2Fprofile",
  );
});

test("production links preserve the deployment prefix and remove query/fragment from the base", () => {
  assert.equal(parseWebsiteOrigin("https://www.nexorai.com.cn/token-dance/?old=1#test"), DEFAULT_WEBSITE_ORIGIN);
  assert.equal(websiteLoginUrl("", "/settings/profile"), "https://www.nexorai.com.cn/token-dance/login?return_to=%2Fsettings%2Fprofile");
  for (const path of ["/register", "/forgot-password", "/onboarding"]) {
    assert.equal(websitePageUrl("", path), `https://www.nexorai.com.cn/token-dance${path}`);
  }
});

test("previously saved default URLs migrate to the production application", () => {
  for (const old of ["http://127.0.0.1:3000/", "http://localhost:3000", "https://www.nexorai.com.cn/"]) {
    assert.equal(resolveWebsiteOrigin(old), DEFAULT_WEBSITE_ORIGIN);
  }
  assert.equal(resolveWebsiteOrigin("https://custom.example/nested/app/"), "https://custom.example/nested/app");
});

test("credentialed and non-http URLs are rejected", () => {
  for (const value of ["file:///C:/Windows", "javascript:alert(1)", "https://user:secret@example.com"]) {
    assert.throws(() => parseWebsiteOrigin(value));
  }
});

test("account avatars preserve the application prefix and allow external image URLs", () => {
  assert.equal(websiteAvatarUrl("", "/api/v1/public/avatars/current"), "https://www.nexorai.com.cn/token-dance/api/v1/public/avatars/current");
  assert.equal(websiteAvatarUrl("", "/images/avatars/fox.png"), "https://www.nexorai.com.cn/token-dance/images/avatars/fox.png");
  assert.equal(websiteAvatarUrl("", "https://images.example/avatar.png"), "https://images.example/avatar.png");
  assert.equal(websiteAvatarUrl("", null), null);
  assert.equal(websiteAvatarUrl("", "javascript:alert(1)"), null);
});
