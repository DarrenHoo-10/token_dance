import React, { useEffect } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { usePersonalAnalytics } from '@/context/PersonalAnalyticsContext';
import { LoadingState } from '@/components/states/LoadingState';
import { PersonalAnalytics } from '@/pages/me/PersonalAnalytics';

export const PublicProfilePage: React.FC = () => {
  const { handle } = useParams<{ handle: string }>();
  const { user, loading } = useAuth();
  const { show } = usePersonalAnalytics();
  if (loading) return <LoadingState />;
  if (user?.handle && user.handle.toLowerCase() === handle?.toLowerCase()) {
    return <OwnAnalyticsOpener show={show} />;
  }
  return <section className="product-page-shell"><PersonalAnalytics publicHandle={handle} /></section>;
};

const OwnAnalyticsOpener: React.FC<{ show: () => void }> = ({ show }) => {
  const navigate = useNavigate();
  useEffect(() => {
    show();
    navigate('/leaderboard', { replace: true });
  }, [navigate, show]);
  return null;
};
