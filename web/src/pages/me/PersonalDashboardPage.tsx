import React, { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { usePersonalAnalytics } from '@/context/PersonalAnalyticsContext';

/** Deep-link / bookmark: open the overlay on the current home without replacing a team page. */
export const PersonalDashboardPage: React.FC = () => {
  const { authenticated, loading } = useAuth();
  const { show } = usePersonalAnalytics();
  const navigate = useNavigate();

  useEffect(() => {
    if (loading) return;
    if (!authenticated) {
      navigate('/login?return_to=%2Fleaderboard', { replace: true });
      return;
    }
    show();
    navigate('/leaderboard', { replace: true });
  }, [authenticated, loading, navigate, show]);

  return null;
};

export { PersonalAnalytics } from './PersonalAnalytics';
