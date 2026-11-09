import React from "react";
import { createRoot } from "react-dom/client";
import { AppearanceProvider } from "../src/appearance";
import { TerminalView } from "../src/views";
import "../src/style.css";

const root = createRoot(document.getElementById("terminal"));
window.showTestTerminal = (visible = true) =>
  root.render(
    <AppearanceProvider>
      <TerminalView
        session={{ id: "image-test" }}
        visible={visible}
        onStatus={(text) => {
          document.getElementById("status").textContent = text;
        }}
      />
    </AppearanceProvider>,
  );
window.showTestTerminal();
