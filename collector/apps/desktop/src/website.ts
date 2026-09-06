export const DEFAULT_WEBSITE_ORIGIN =
  (import.meta as ImportMeta & { env?: Record<string, string> }).env?.VITE_TOKENDANCE_WEBSITE_ORIGIN
  || "https://www.nexorai.com.cn/token-dance";
export const WEBSITE_URL_STORAGE_KEY = "tokendance.websiteUrl";

export function parseWebsiteOrigin(value: string): string {
  const url = new URL(value);
  if (!["http:", "https:"].includes(url.protocol) || !url.host || url.username || url.password) {
    throw new Error("请输入有效的 HTTP / HTTPS 网站地址");
  }
  return `${url.origin}${url.pathname.replace(/\/+$/, "")}`;
}

export function resolveWebsiteOrigin(stored: string | null | undefined): string {
  const raw = (stored ?? "").trim();
  if (!raw) return DEFAULT_WEBSITE_ORIGIN;
  const base = parseWebsiteOrigin(raw);
  if (["http://127.0.0.1:3000", "http://localhost:3000", "https://www.nexorai.com.cn"].includes(base)) {
    return DEFAULT_WEBSITE_ORIGIN;
  }
  return base;
}

export function websiteHomeUrl(origin: string): string {
  return `${resolveWebsiteOrigin(origin)}/`;
}

export function websitePageUrl(origin: string, path: string): string {
  return new URL(path.replace(/^\/+/, ""), websiteHomeUrl(origin)).href;
}

export function websiteLoginUrl(origin: string, returnTo: string): string {
  const url = new URL(websitePageUrl(origin, "/login"));
  url.searchParams.set("return_to", returnTo);
  return url.href;
}
