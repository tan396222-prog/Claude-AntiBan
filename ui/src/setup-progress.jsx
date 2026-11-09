import React from "react";
import { ArrowDownToLine, Box, Layers3, PackageOpen, SlidersHorizontal } from "lucide-react";
import "./setup-progress.css";

const stages = [
  ["image", "系统镜像", ArrowDownToLine],
  ["packages", "系统软件", Layers3],
  ["tools", "开发工具", PackageOpen],
  ["services", "环境服务", SlidersHorizontal],
];

function bytes(value) {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
  return `${Number(value.toFixed(unit && value < 100 ? 1 : 0))} ${units[unit]}`;
}

export function SetupProgress({ job, onDetails, onStop, onRetry, disconnected }) {
  const { phase, label, received, total } = job.setup_progress;
  const stage = stages.find(([key]) => key === phase);
  const Icon = stage?.[2] || Box;
  const hasBytes = Number.isFinite(received) && received >= 0;
  const knownSize = hasBytes && Number.isFinite(total) && total > 0 && received <= total;
  const percent = knownSize ? Math.floor(received / total * 100) : null;
  const failed = Boolean(job.error);
  const meter = knownSize ? `${bytes(received)} / ${bytes(total)}` : hasBytes ? `已接收 ${bytes(received)}` : "";
  return <section className={`setup-progress${failed ? " setup-progress-failed" : ""}`} aria-label="环境安装进度">
    <ol className="setup-stages" aria-label="安装阶段">
      {stages.map(([key, name]) => <li key={key} aria-current={key === phase ? "step" : undefined}>{name}</li>)}
    </ol>
    <div className="setup-transfer">
      <div className="setup-transfer-heading">
        <span className="setup-transfer-icon"><Icon size={19} aria-hidden="true" /></span>
        <div className="setup-transfer-description">
          <h2>{label || stage?.[1] || "正在准备环境"}</h2>
          {meter && <span className="setup-transfer-bytes">{meter}</span>}
        </div>
        {knownSize && <strong className="setup-transfer-number">{percent}<small>%</small></strong>}
      </div>
      {!failed && <div role="progressbar" aria-label={label || stage?.[1] || "当前操作"}
        aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent ?? undefined}
        aria-valuetext={knownSize ? `${percent}%，${meter}` : meter || undefined}
        className={knownSize ? "setup-meter" : "setup-meter setup-meter-active"}>
        <span style={knownSize ? { width: `${received / total * 100}%` } : undefined} />
      </div>}
      {failed && <>
        <p className="setup-transfer-error" role="alert">{job.error}</p>
        <p className="setup-transfer-hint">可手动继续安装，已保存的下载会尝试续传。</p>
      </>}
    </div>
    <div className="setup-transfer-actions">
      <button className="text-button" type="button" onClick={onDetails}>操作详情</button>
      {failed
        ? <button type="button" className="primary" onClick={onRetry} disabled={disconnected}>继续安装</button>
        : <button type="button" onClick={onStop} disabled={disconnected}>停止安装</button>}
    </div>
  </section>;
}
