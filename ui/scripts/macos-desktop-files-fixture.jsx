import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { MacOSClaudeWindow } from "../src/macos-claude-window";
import { useDesktopFiles } from "../src/browser-downloads";
import "../src/style.css";

function Fixture() {
  const [notice, setNotice] = useState("");
  const files = useDesktopFiles(true, setNotice);
  return <div className="app">
    <output>{notice}</output>
    <MacOSClaudeWindow visible uploads={files.uploads} onStatus={setNotice} onConfirm={async () => true} />
  </div>;
}
createRoot(document.getElementById("root")).render(<Fixture />);
