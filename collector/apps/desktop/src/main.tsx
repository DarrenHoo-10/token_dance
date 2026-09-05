import React from "react";
import ReactDOM from "react-dom/client";
import { App } from "./App.tsx";
import { UsagePanel } from "./UsagePanel.tsx";
import "./styles/base.css";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    {new URLSearchParams(window.location.search).get("view") === "settings" ? <App /> : <UsagePanel />}
  </React.StrictMode>
);
