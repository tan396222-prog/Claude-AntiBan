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
  const [error, setError] = useState("");
  const player = useRef();
  useEffect(() => {
    const output = new AudioOutput(setState, setError);
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
    const muted = !preferences.muted && preferences.volume > 0;
    setPreferences({ ...preferences, muted, volume: preferences.volume || 1 });
    player.current?.setActive(enabled && !muted);
  };
  return { enabled, state, error, ...preferences, toggle, volumeChange: volume => {
    setPreferences({ muted: false, volume });
    player.current?.setActive(enabled && volume > 0);
  } };
}

export function AudioControls({ audio }) {
  const [open, setOpen] = useState(false);
  const controls = useRef(null);
  const keyboardOpened = useRef(false);
  const pressingSpeaker = useRef(false);
  useEffect(() => {
    if (!open) return;
    const dismiss = event => {
      if (!controls.current?.contains(event.target)) setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [open]);
  useEffect(() => {
    if (!audio.enabled) setOpen(false);
  }, [audio.enabled]);
  const silent = audio.muted || audio.volume === 0;
  const label = !audio.enabled ? "环境声音：未启动" : !open ? "打开音量控制" : silent ? "取消静音" : "静音";
  const status = audio.error || (audio.state === "unsupported" ? "当前窗口不支持声音播放" : audio.state === "disconnected" ? "声音连接中断，正在重连" : "");
  return <div className="audio-controls" ref={controls}
    onPointerLeave={event => { pressingSpeaker.current = false; if (event.pointerType !== "touch") setOpen(false); }}
    onBlur={event => { if (!pressingSpeaker.current && !event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}
    onKeyDown={event => { if (event.key === "Escape" && open) { event.stopPropagation(); setOpen(false); controls.current?.querySelector("button")?.focus(); } }}>
    <button onPointerDown={() => { pressingSpeaker.current = true; }} onPointerCancel={() => { pressingSpeaker.current = false; }} onClick={event => { pressingSpeaker.current = false; if (open) audio.toggle(); else { keyboardOpened.current = event.detail === 0; setOpen(true); } }} disabled={!audio.enabled} aria-label={label} title={status ? `${label}（${status}）` : label} aria-expanded={open} aria-pressed={open ? silent : undefined}>
      {silent ? <VolumeX size={15} /> : <Volume2 size={15} />}
    </button>
    {open && <div className="audio-volume-panel">
      <input type="range" aria-label="环境音量" title={`环境音量 ${Math.round(audio.volume * 100)}%`} min="0" max="100" step="1" value={Math.round(audio.volume * 100)} style={{ "--volume-level": `${Math.round(audio.volume * 100)}%` }} disabled={!audio.enabled} autoFocus={keyboardOpened.current} onChange={e => audio.volumeChange(Number(e.target.value) / 100)} onKeyDown={event => {
        const value = Math.round(audio.volume * 100);
        const next = { ArrowUp: value + 1, ArrowRight: value + 1, ArrowDown: value - 1, ArrowLeft: value - 1, Home: 0, End: 100, PageUp: value + 10, PageDown: value - 10 }[event.key];
        if (next !== undefined) { event.preventDefault(); audio.volumeChange(Math.max(0, Math.min(100, next)) / 100); }
      }} />
    </div>}
  </div>;
}
