import React, { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { usePersonalAnalytics } from '@/context/PersonalAnalyticsContext';
import { LoadingState } from '@/components/states/LoadingState';
import { UnauthorizedState } from '@/components/states/UnauthorizedState';

/** Deep-link / bookmark: open the overlay on the current home without replacing a team page. */
export const PersonalDashboardPage: React.FC = () => {
  const { authenticated, loading } = useAuth();
  const { show } = usePersonalAnalytics();
  const navigate = useNavigate();

  useEffect(() => {
    if (loading || !authenticated) return;
    show();
    navigate('/leaderboard', { replace: true });
  }, [authenticated, loading, navigate, show]);

  if (loading) return <LoadingState />;
  if (!authenticated) return <UnauthorizedState />;
  return null;
};

export { PersonalAnalytics } from './PersonalAnalytics';
