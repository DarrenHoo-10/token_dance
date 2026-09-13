// Keep both desktop headers on the same logo used by the website.
export const brandLogo = new URL("./assets/logo-tokendance-v2.png", import.meta.url).href;
export const localTestBuild = (import.meta as ImportMeta & { env: Record<string, string> }).env.VITE_TOKENDANCE_LOCAL_TEST === "1";
