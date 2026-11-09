import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { AudioControls, useAudioOutput } from "../src/audio";
function Fixture() {
  const [enabled, setEnabled] = useState(true);
  const audio = useAudioOutput(enabled);
  return <><AudioControls audio={audio} /><output>{audio.state}</output><button onClick={() => setEnabled(false)}>停止测试环境</button></>;
}
createRoot(document.getElementById("root")).render(<Fixture />);
