import React from "react";
import { TokenShowProvider, useTokenShow } from "./context/TokenShowContext.tsx";
import { Navbar } from "./components/Navbar.tsx";
import { DashboardView } from "./components/DashboardView.tsx";
import { AgentsMatrixView } from "./components/AgentsMatrixView.tsx";
import { OfflineQueueView } from "./components/OfflineQueueView.tsx";
import { PrivacySettingsView } from "./components/PrivacySettingsView.tsx";
import { DevicesConfigView } from "./components/DevicesConfigView.tsx";
import { LeaderboardExploreView } from "./components/LeaderboardExploreView.tsx";
import { AuthModal } from "./components/AuthModal.tsx";
import { OnboardingWizard } from "./components/OnboardingWizard.tsx";
import { UploadPreviewModal } from "./components/UploadPreviewModal.tsx";
import type { ControlPlaneClient } from "./api/controlPlane.ts";
import "./styles/app.css";

const MainContent: React.FC = () => {
  const { activeTab, error } = useTokenShow();

  return (
    <main className="main-content">
      {error && <div className="feedback-error" role="alert">{error}</div>}
      {activeTab === "dashboard" && <DashboardView />}
      {activeTab === "agents" && <AgentsMatrixView />}
      {activeTab === "queue" && <OfflineQueueView />}
      {activeTab === "privacy" && <PrivacySettingsView />}
      {activeTab === "devices" && <DevicesConfigView />}
      {activeTab === "leaderboard" && <LeaderboardExploreView />}

      {/* Global Modals */}
      <AuthModal />
      <OnboardingWizard />
      <UploadPreviewModal />
    </main>
  );
};

export const App: React.FC<{ client?: ControlPlaneClient }> = ({ client }) => {
  return (
    <TokenShowProvider client={client}>
      <div className="app-container">
        <Navbar />
        <MainContent />
      </div>
    </TokenShowProvider>
  );
};

export default App;
