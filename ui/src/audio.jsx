import { useEffect, useRef, useState } from "react";
import { Volume2, VolumeX } from "lucide-react";
import { AudioOutput } from "./audio-output";

function savedAudio() {
  try {
    const value = JSON.parse(localStorage.getItem("cage.audio") || "{}");
    return { muted: value.muted === true, volume: Number.isFinite(value.volume) ? Math.max(0, Math.min(1, value.volume)) : 1 };
  } catch { return { muted: false, volume: 1 }; }
}

export function useAudioOutput(enabled) {
  const [preferences, setPreferences] = useState(savedAudio);
  const [state, setState] = useState("idle");
  const player = useRef();
  useEffect(() => {
    const output = new AudioOutput(setState);
    player.current = output;
    const activate = () => output.activate();
    window.addEventListener("pointerdown", activate, true);
    window.addEventListener("keydown", activate, true);
    return () => {
      window.removeEventListener("pointerdown", activate, true);
      window.removeEventListener("keydown", activate, true);
      output.close();
      player.current = null;
    };
  }, []);
  useEffect(() => {
    player.current?.setVolume(preferences.volume);
    player.current?.setActive(enabled && !preferences.muted && preferences.volume > 0);
    try { localStorage.setItem("cage.audio", JSON.stringify(preferences)); } catch { /* Playback also works without storage. */ }
  }, [enabled, preferences]);
  const toggle = () => {
    if (state === "blocked" && !preferences.muted && preferences.volume > 0) {
      player.current?.activate();
      return;
    }
    const muted = !preferences.muted && preferences.volume > 0;
    setPreferences({ ...preferences, muted, volume: preferences.volume || 1 });
    player.current?.setActive(enabled && !muted);
  };
  return { enabled, state, ...preferences, toggle, volumeChange: volume => {
    setPreferences({ muted: false, volume });
    player.current?.setActive(enabled && volume > 0);
  } };
}

export function AudioControls({ audio }) {
  const silent = audio.muted || audio.volume === 0;
  const label = !audio.enabled ? "环境声音：未启动" : silent ? "取消静音" : audio.state === "blocked" ? "启用声音" : audio.state === "unsupported" ? "当前窗口不支持声音播放" : audio.state === "disconnected" ? "声音连接中断，正在重连" : "静音";
  return <div className="audio-controls">
    <button onClick={audio.toggle} disabled={!audio.enabled} aria-label={label} title={label} aria-pressed={silent}>
      {silent ? <VolumeX size={15} /> : <Volume2 size={15} />}
      {audio.state === "blocked" && !silent && audio.enabled && <span>启用声音</span>}
    </button>
    <input type="range" aria-label="环境音量" title={`环境音量 ${Math.round(audio.volume * 100)}%`} min="0" max="100" step="1" value={Math.round(audio.volume * 100)} disabled={!audio.enabled} onChange={e => audio.volumeChange(Number(e.target.value) / 100)} />
  </div>;
}
