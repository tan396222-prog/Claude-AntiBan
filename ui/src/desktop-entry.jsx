import React, { lazy, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { installDiagnostics } from "./diagnostics.mjs";
import { DiagnosticBoundary } from "./diagnostics.jsx";
import { ClientRoot } from "./remote-hosts";
import "./style.css";

const Workbench = lazy(() => import("./main.jsx"));

installDiagnostics();
createRoot(document.getElementById("root")).render(
  <DiagnosticBoundary>
    <ClientRoot>
      <Suspense
        fallback={
          <div className="client-loading" role="status">
            正在打开工作台…
          </div>
        }
      >
        <Workbench />
      </Suspense>
    </ClientRoot>
  </DiagnosticBoundary>,
);

window.addEventListener("hashchange", () => {
  if (location.hash) location.reload();
});
