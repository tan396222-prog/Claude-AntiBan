import "./region-presets.css";
import React, { useEffect, useState } from "react";
import { Check, LoaderCircle, Zap } from "lucide-react";
import { api } from "./api";
import { InlineSelect } from "./inline-select";

const FOLLOW_EXIT = "follow-exit";

// One-click timezone + language: applies a preset (or following the exit)
// through the same settings job as the form below.
export function RegionQuickApply({ status, busy, onConfirm, onJob, onError }) {
  const [data, setData] = useState(null);
  const [choice, setChoice] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const locationKey = `${status.location?.ip || ""}|${status.location?.timezone || ""}`;
  useEffect(() => {
    let alive = true;
    api("/region/presets")
      .then((result) => {
        if (!alive) return;
        setData(result);
        setChoice((previous) => previous || result.recommended || result.current || result.presets[0]?.id || FOLLOW_EXIT);
      })
      .catch((e) => alive && onError(e.message));
    return () => {
      alive = false;
    };
  }, [locationKey]);
  if (!data) return null;
  const selected = data.presets.find((preset) => preset.id === choice);
  const following = data.settings.auto_timezone && data.settings.auto_language;
  const applied = choice === FOLLOW_EXIT ? following : choice === data.current;
  const apply = async () => {
    if (busy || submitting) return;
    if (
      status.vm_running &&
      !(await onConfirm({
        title: "一键修改时区和语言并重启环境？",
        body: "请先保存编辑器中的文件。当前终端会话会结束，浏览器的页面和登录状态将保留。",
        confirm: "应用并重启",
      }))
    )
      return;
    setSubmitting(true);
    onError("");
    try {
      const result = await api("/region/apply", choice === FOLLOW_EXIT ? { follow_exit: true } : { preset: choice });
      onJob(result.id);
    } catch (e) {
      onError(e.message);
    } finally {
      setSubmitting(false);
    }
  };
  return (
    <section className="region-quick" aria-label="一键修改时区和语言">
      <h4>
        <Zap size={15} /> 一键修改时区和语言
      </h4>
      <p className="field-help">
        同时设置客机系统时区、系统语言（locale）、CLI 环境变量和浏览器首选语言。建议与代理出口所在地保持一致。
      </p>
      <div className="region-quick-row">
        <InlineSelect aria-label="时区与语言预设" value={choice} onValueChange={setChoice}>
          <option value={FOLLOW_EXIT}>跟随出口 IP（自动） / Follow exit IP</option>
          {data.presets.map((preset) => (
            <option key={preset.id} value={preset.id}>
              {preset.label} / {preset.label_en}
              {preset.id === data.recommended ? " · 匹配当前出口" : ""}
            </option>
          ))}
        </InlineSelect>
        <button className="primary" disabled={busy || submitting || applied} onClick={apply}>
          {submitting ? <LoaderCircle size={15} className="spin" /> : applied ? <Check size={15} /> : <Zap size={15} />}{" "}
          {applied ? "已应用" : status.vm_running ? "一键应用并重启" : "一键应用"}
        </button>
      </div>
      <div className="inline-info">
        <span>
          {selected
            ? `${selected.timezone} · ${selected.locale} · 网页 ${selected.browser_language}`
            : "启动或应用时经代理查询出口所在地，自动设置时区和语言。"}
          {data.recommended && choice !== data.recommended && (
            <>
              {" "}
              <button className="text-button" onClick={() => setChoice(data.recommended)}>
                选择与当前出口匹配的预设
              </button>
            </>
          )}
        </span>
      </div>
    </section>
  );
}
