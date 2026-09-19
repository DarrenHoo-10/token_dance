import React from 'react';
import { Outlet, useLocation } from 'react-router-dom';
import { Navbar } from './Navbar';
import { Footer } from './Footer';

export const AppLayout: React.FC = () => {
  const { pathname } = useLocation();
  return (
    <div className={`app-container product-shell product-sky ${pathname === '/leaderboard' ? 'sky-home-shell' : ''}`}>
      <Navbar />
      <main className="main-content">
        <Outlet />
      </main>
      <Footer />
    </div>
  );
};
