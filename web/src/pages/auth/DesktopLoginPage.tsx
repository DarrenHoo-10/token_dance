import { useEffect, useMemo, useRef, useState } from 'react';
import { Navigate, useSearchParams } from 'react-router-dom';
import { api } from '@/api/client';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { LoadingState } from '@/components/states/LoadingState';
import { EmptyState } from '@/components/states/EmptyState';

export function desktopLoginRequest(params: URLSearchParams) {
  const redirectUri = params.get('redirect_uri') || '';
  const codeChallenge = params.get('code_challenge') || '';
  const state = params.get('state') || '';
  try {
    const redirect = new URL(redirectUri);
    if (!/^http:\/\/127\.0\.0\.1:[1-9][0-9]{0,4}\/callback$/.test(redirectUri) ||
      Number(redirect.port || '80') > 65535 || !/^[a-f0-9]{64}$/.test(codeChallenge) || !/^[a-f0-9]{64}$/.test(state)) return null;
    return { redirectUri, codeChallenge, state };
  } catch { return null; }
}

export function DesktopLoginPage() {
  const { authenticated, loading } = useAuth();
  const { locale } = useLocale();
  const [params] = useSearchParams();
  const request = useMemo(() => desktopLoginRequest(params), [params]);
  const pending = useRef<{ key: string; promise: Promise<{ redirectUrl: string }> } | null>(null);
  const [failed, setFailed] = useState(false);
  const zh = locale === 'zh-CN';

  useEffect(() => {
    if (loading || !authenticated || !request) return;
    let active = true;
    const key = JSON.stringify(request);
    if (pending.current?.key !== key) pending.current = { key, promise: api.authorizeDesktop(request) };
    pending.current.promise.then(({ redirectUrl }) => {
      if (!active) return;
      const callback = new URL(redirectUrl);
      const expected = new URL(request.redirectUri);
      if (callback.origin !== expected.origin || callback.pathname !== expected.pathname || callback.searchParams.get('state') !== request.state) throw new Error('Invalid callback');
      window.location.replace(callback.href);
    }).catch(() => { if (active) setFailed(true); });
    return () => { active = false; };
  }, [authenticated, loading, request]);

  if (!request || failed) return <EmptyState
    title={zh ? '未能完成桌面登录' : 'Desktop sign-in could not be completed'}
    description={zh ? '请返回 TokenDance 桌面应用重新发起登录。' : 'Return to the TokenDance desktop app and start sign-in again.'}
  />;
  if (!loading && !authenticated) return <Navigate replace to={`/login?return_to=${encodeURIComponent(`/desktop-login?${params}`)}`} />;
  return <LoadingState message={zh ? '正在完成桌面登录…' : 'Completing desktop sign-in…'} />;
}
