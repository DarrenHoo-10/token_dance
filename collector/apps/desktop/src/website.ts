export const DEFAULT_WEBSITE_ORIGIN = "http://127.0.0.1:3000";
export const WEBSITE_URL_STORAGE_KEY = "tokendance.websiteUrl";

export function parseWebsiteOrigin(value: string): string {
  const url = new URL(value);
  if (!["http:", "https:"].includes(url.protocol) || !url.host || url.username || url.password) {
    throw new Error("请输入有效的 HTTP / HTTPS 网站地址");
  }
  return url.origin;
}

export function resolveWebsiteOrigin(stored: string | null | undefined): string {
  const raw = (stored ?? "").trim();
  if (!raw) return DEFAULT_WEBSITE_ORIGIN;
  return parseWebsiteOrigin(raw);
}

export function websiteHomeUrl(origin: string): string {
  return new URL("/", `${resolveWebsiteOrigin(origin)}/`).href;
}

export function websiteLoginUrl(origin: string, returnTo: string): string {
  const url = new URL("/login", `${resolveWebsiteOrigin(origin)}/`);
  url.searchParams.set("return_to", returnTo);
  return url.href;
}
