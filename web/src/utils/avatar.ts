export function avatarUrl(url: string): string {
  url = url.replace(/^\/images\/avatars\/(bunny|cat|fox|panda)\.png$/, '/images/avatars/$1-256.jpg');
  return url.startsWith('/api/') || url.startsWith('/images/avatars/') ? `${import.meta.env.BASE_URL.replace(/\/$/, '')}${url}` : url;
}

export function teamAvatarUrl(team: { id: string; avatarUrl?: string | null; profileVersion: string }): string {
  if (!team.avatarUrl) return '';
  const path = team.avatarUrl.startsWith('/api/') ? team.avatarUrl : `/api/v1/teams/${encodeURIComponent(team.id)}/avatar/content`;
  return `${avatarUrl(path)}?v=${encodeURIComponent(team.profileVersion)}`;
}
