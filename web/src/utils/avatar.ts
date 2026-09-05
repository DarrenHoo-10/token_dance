export function avatarUrl(url: string): string {
  return url.startsWith('/api/') ? `${import.meta.env.BASE_URL.replace(/\/$/, '')}${url}` : url;
}
