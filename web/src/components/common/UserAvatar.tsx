import { useState } from 'react';
import { avatarUrl } from '@/utils/avatar';

export function UserAvatar({ url, name, className, fallbackClassName = className, alt = '', loading = 'eager', fetchPriority = 'auto' }: {
  url?: string | null; name: string; className?: string; fallbackClassName?: string; alt?: string;
  loading?: 'eager' | 'lazy'; fetchPriority?: 'high' | 'low' | 'auto';
}) {
  const [failedUrl, setFailedUrl] = useState<string | null>(null);
  if (url && url !== failedUrl) {
    return <img className={className} src={avatarUrl(url)} alt={alt} loading={loading} fetchPriority={fetchPriority} decoding="async" onError={() => setFailedUrl(url)} />;
  }
  return <span className={fallbackClassName} aria-hidden="true">{Array.from(name.trim())[0]?.toUpperCase() || 'T'}</span>;
}
