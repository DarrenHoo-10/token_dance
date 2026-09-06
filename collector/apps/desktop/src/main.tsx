import React from "react";
import ReactDOM from "react-dom/client";
import { App } from "./App.tsx";
import { UsagePanel } from "./UsagePanel.tsx";
import { FloatingOrb } from "./orb/FloatingOrb.tsx";
import { OrbDetails } from "./orb/OrbDetails.tsx";
import { OrbEffects } from "./orb/OrbEffects.tsx";
import "./styles/base.css";

const view = new URLSearchParams(window.location.search).get("view");
const page = view === "settings" ? <App />
  : view === "orb" ? <FloatingOrb />
  : view === "orb-effects" ? <OrbEffects />
  : view === "orb-details" ? <OrbDetails />
  : <UsagePanel />;

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    {page}
  </React.StrictMode>
);
