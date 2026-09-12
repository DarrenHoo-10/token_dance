import { useState } from 'react';
import { avatarUrl } from '@/utils/avatar';

export function UserAvatar({ url, name, className, fallbackClassName = className, alt = '' }: {
  url?: string | null; name: string; className?: string; fallbackClassName?: string; alt?: string;
}) {
  const [failedUrl, setFailedUrl] = useState<string | null>(null);
  if (url && url !== failedUrl) {
    return <img className={className} src={avatarUrl(url)} alt={alt} onError={() => setFailedUrl(url)} />;
  }
  return <span className={fallbackClassName} aria-hidden="true">{Array.from(name.trim())[0]?.toUpperCase() || 'T'}</span>;
}
