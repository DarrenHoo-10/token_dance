export function avatarUrl(url: string): string {
  url = url.replace(/^\/images\/avatars\/(bunny|cat|fox|panda)\.png$/, '/images/avatars/$1-256.jpg');
  return url.startsWith('/api/') || url.startsWith('/images/avatars/') ? `${import.meta.env.BASE_URL.replace(/\/$/, '')}${url}` : url;
}
