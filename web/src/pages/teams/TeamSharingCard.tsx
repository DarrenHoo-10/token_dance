import { useEffect, useRef, useState } from 'react';
import { ShieldCheck } from 'lucide-react';
import { ApiError } from '@/api/client';
import { teamsApi, type MySharingResponse, type SharingFlags } from '@/api/teams';
import { Card } from '@/components/common/Card';
import { Button } from '@/components/common/Button';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import { SharingControls, teamErrorMessage } from './TeamShared';

export function TeamSharingCard() {
  const { scope, applyScope } = useTeam();
  const { t, locale } = useLocale();
  const [data, setData] = useState<MySharingResponse | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const scopeRef = useRef(scope); scopeRef.current = scope;
  const mutation = useRef<AbortController | null>(null);
  const teamId = scope?.team.id;
  const membershipId = scope?.membership.id;
  useEffect(() => {
    const controller = new AbortController();
    setData(null); setError(''); setBusy(false);
    if (teamId) teamsApi.getMySharing(teamId, controller.signal).then(value => { if (!controller.signal.aborted) setData(value); }).catch(err => { if (!controller.signal.aborted) setError(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown')); });
    return () => { controller.abort(); mutation.current?.abort(); };
  }, [teamId, membershipId, retry, t]);
  async function save(sharing: SharingFlags) {
    if (!data || busy || !teamId) return;
    const controller = new AbortController(); mutation.current = controller;
    setBusy(true); setError('');
    try {
      const next = await teamsApi.updateMySharing(teamId, { expectedSharingVersion: data.sharingVersion, sharing }, { signal: controller.signal });
      if (controller.signal.aborted || scopeRef.current?.team.id !== teamId) return;
      setData(next);
      const current = scopeRef.current;
      applyScope({ ...current, team: { ...current.team, authRevision: next.authRevision }, membership: { ...current.membership, sharingVersion: next.sharingVersion } });
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'));
    } finally { if (!controller.signal.aborted) setBusy(false); }
  }
  return <Card className="sky-team-sharing"><div className="panel-header sky-settings-heading"><div><h2><ShieldCheck size={21} />{t('teams.sharing.title')}</h2><p>{locale === 'zh-CN' ? '由你决定，哪些用量参与团队分析。' : 'You decide which usage joins team analytics.'}</p></div></div>{data ? <SharingControls value={data.sharing} onChange={sharing => void save(sharing)} disabled={busy} revealDetailsWithBase={false} /> : !error && <p className="text-muted">{t('common.loading')}</p>}{error && <div className="sky-sharing-error" role="alert"><p>{error}</p><Button variant="outline" onClick={() => { setBusy(false); setRetry(value => value + 1); }}>{t('common.retry')}</Button></div>}<p className="sky-sharing-note"><ShieldCheck size={16} />{t('teams.sharing.closeBaseHint')}</p></Card>;
}
