export function avatarUrl(url: string): string {
  return url.startsWith('/api/') || url.startsWith('/images/avatars/') ? `${import.meta.env.BASE_URL.replace(/\/$/, '')}${url}` : url;
}
