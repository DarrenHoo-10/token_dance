import { invoke } from "@tauri-apps/api/core";
import { getWebsiteUrl, isTauriEnvironment } from "./tauri-bridge";
import { resolveWebsiteOrigin, websitePageUrl } from "./website";

export interface AccountUser {
  userId: string;
  displayName: string;
  handle: string | null;
  avatarUrl?: string | null;
  onboardingRequired: boolean;
}
export const accountWebsite = () => resolveWebsiteOrigin(getWebsiteUrl());
export async function getAccountSession(website: string): Promise<{ user: AccountUser | null }> {
  return isTauriEnvironment() ? invoke("get_account_session", { website }) : { user: null };
}
export async function loginAccount(website: string, mode: "login" | "register" = "login"): Promise<{ user: AccountUser | null }> {
  if (!isTauriEnvironment()) throw new Error("PREVIEW_ONLY");
  return invoke("login_account", { website, mode });
}
export async function logoutAccount(website: string): Promise<void> {
  if (isTauriEnvironment()) await invoke("logout_account", { website });
}
export async function openAccountWebsite(path: "/register" | "/forgot-password" | "/onboarding"): Promise<void> {
  const url = websitePageUrl(accountWebsite(), path);
  if (isTauriEnvironment()) await invoke("open_website", { url });
  else window.open(url, "_blank", "noopener,noreferrer");
}
