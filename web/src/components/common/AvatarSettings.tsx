import { useEffect, useRef, useState } from 'react';
import { api } from '@/api/client';
import { useLocale } from '@/context/LocaleContext';
import { useAuth } from '@/context/AuthContext';
import { avatarUrl } from '@/utils/avatar';
import type { UserProfile } from '@/types/api';
import { Button } from './Button';

export function AvatarSettings({ profile, onUpdated, onBusy, disabled = false }: { profile: UserProfile; onUpdated: (profile: UserProfile) => void; onBusy: (busy: boolean) => void; disabled?: boolean }) {
  const { locale } = useLocale(); const zh = locale === 'zh-CN';
  const { setUser } = useAuth();
  const input = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState('');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  useEffect(() => {
    if (!file) { setPreview(''); return; }
    const url = URL.createObjectURL(file); setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [file]);
  function selectFile(value?: File) {
    setError(''); setMessage('');
    if (!value) return;
    if (!['image/png','image/jpeg','image/webp'].includes(value.type) || value.size === 0 || value.size > 5 * 1024 * 1024) {
      setError(zh ? '请选择不超过 5 MB 的 PNG、JPG 或 WebP 图片。' : 'Choose a PNG, JPG or WebP image up to 5 MB.'); return;
    }
    setFile(value);
  }
  async function save(remove = false) {
    if (busy || disabled || (!remove && !file)) return;
    setBusy(true); onBusy(true); setError(''); setMessage('');
    try {
      if (remove) await api.deleteAvatar();
      else {
        const bytes = await file!.arrayBuffer();
        const hash = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(v => v.toString(16).padStart(2,'0')).join('');
        const intent = await api.createAvatarUploadIntent(file!.type, file!.size, hash);
        await api.uploadAvatarContent(intent.objectId, file!);
        await api.completeAvatarUpload(intent.objectId);
      }
      const updated = await api.getProfile();
      onUpdated(updated);
      setUser(current => current ? { ...current, avatarUrl: updated.avatarUrl } : current);
      setFile(null); setMessage(zh ? '头像已更新' : 'Avatar updated');
    } catch {
      setError(zh ? '头像更新失败，请检查图片并重试。图片边长不能超过 4096 像素。' : 'Could not update avatar. Retry with an image no larger than 4096 pixels per side.');
    } finally { setBusy(false); onBusy(false); }
  }
  const source = preview || (profile.avatarUrl ? avatarUrl(profile.avatarUrl) : '');
  return <section className="avatar-settings" aria-label={zh ? '头像设置' : 'Avatar settings'}>
    <div className="profile-avatar-preview">{source ? <img src={source} alt={zh ? '头像预览' : 'Avatar preview'} /> : <span>{profile.displayName.slice(0, 1).toUpperCase()}</span>}</div>
    <div>
      <h3>{zh ? '头像' : 'Avatar'}</h3>
      <p className="text-muted">{zh ? 'PNG、JPG 或 WebP，最大 5 MB。' : 'PNG, JPG or WebP, up to 5 MB.'}</p>
      <input ref={input} type="file" accept="image/png,image/jpeg,image/webp" aria-label={zh ? '选择头像文件' : 'Choose avatar file'} hidden disabled={busy || disabled} onChange={e => { selectFile(e.target.files?.[0]); e.target.value = ''; }} />
      <div className="avatar-actions">
        <Button variant="outline" disabled={busy || disabled} onClick={() => input.current?.click()}>{zh ? '选择图片' : 'Choose image'}</Button>
        {file && <Button variant="dark" loading={busy} disabled={disabled} onClick={() => save()}>{zh ? '保存头像' : 'Save avatar'}</Button>}
        {file && <Button variant="ghost" disabled={busy || disabled} onClick={() => { setFile(null); setError(''); }}>{zh ? '取消' : 'Cancel'}</Button>}
        {!file && profile.avatarUrl && <Button variant="ghost" disabled={busy || disabled} onClick={() => save(true)}>{zh ? '移除头像' : 'Remove avatar'}</Button>}
      </div>
      {error && <p role="alert" className="avatar-error">{error}</p>}
      {message && <p role="status">{message}</p>}
    </div>
  </section>;
}
