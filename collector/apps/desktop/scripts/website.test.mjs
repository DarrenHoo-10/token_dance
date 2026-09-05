import test from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_WEBSITE_ORIGIN,
  parseWebsiteOrigin,
  resolveWebsiteOrigin,
  websiteHomeUrl,
  websiteLoginUrl,
} from "../src/website.ts";

test("empty stored URL falls back to the built-in origin", () => {
  assert.equal(resolveWebsiteOrigin(""), DEFAULT_WEBSITE_ORIGIN);
  assert.equal(resolveWebsiteOrigin("   "), DEFAULT_WEBSITE_ORIGIN);
  assert.equal(resolveWebsiteOrigin(null), DEFAULT_WEBSITE_ORIGIN);
});

test("the tray button opens the site home; login is only used with an explicit return path", () => {
  assert.equal(parseWebsiteOrigin("https://tokendance.example/app"), "https://tokendance.example");
  assert.equal(websiteHomeUrl("https://tokendance.example"), "https://tokendance.example/");
  assert.equal(websiteHomeUrl(""), "http://127.0.0.1:3000/");
  assert.equal(
    websiteLoginUrl("http://127.0.0.1:3000", "/settings/profile"),
    "http://127.0.0.1:3000/login?return_to=%2Fsettings%2Fprofile",
  );
});

test("credentialed and non-http URLs are rejected", () => {
  for (const value of ["file:///C:/Windows", "javascript:alert(1)", "https://user:secret@example.com"]) {
    assert.throws(() => parseWebsiteOrigin(value));
  }
});
