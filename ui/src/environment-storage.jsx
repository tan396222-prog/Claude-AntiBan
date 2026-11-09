import React, { useEffect, useState } from "react";
import { FolderOpen } from "lucide-react";
import { api, native, nativeAvailable } from "./api";

export function EnvironmentStorage({ status, onConfirm }) {
  const [location, setLocation] = useState(null);
  const [pending, setPending] = useState(null);
  const [directory, setDirectory] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const job = status.job;
  const busy = submitting || Boolean(pending) || Boolean(job?.running);
  useEffect(() => {
    let live = true;
    if (pending && job?.id === pending.previousJob) return;
    if (job?.kind === "storage" && job.running) return;
    api("/environment/storage").then((next) => {
      if (!live) return;
      setLocation(next);
      if (pending) {
        setPending(null);
        if (job?.id === pending.id && job.error) setError(job.error);
        else if (job?.id === pending.id || next.directory === pending.path) { setDirectory(""); setError(""); }
        else setError("操作已结束，请核对当前环境目录后重试。");
      }
    }).catch((e) => {
      if (live) { setError(e.message); setPending(null); }
    });
    return () => { live = false; };
  }, [pending, job?.id, job?.running]);

  const choose = async () => {
    setError("");
    try {
      const selected = await native("chooseFolder", { purpose: "environmentStorage" });
      if (selected) setDirectory(selected);
    } catch { setError("未能选择文件夹，请直接填写绝对路径。"); }
  };
  const apply = async () => {
    if (!location || busy) return;
    const path = directory.trim();
    if (location.has_environment && !(await onConfirm({
      title: "迁移本机环境？",
      body: `请先保存文件。Cage 会停止环境和终端，将虚拟磁盘复制到 ${path}，校验后再切换位置。原数据保留为备份，迁移完成后可手动启动环境。`,
      confirm: "迁移环境",
    }))) return;
    setSubmitting(true);
    setError("");
    try {
      const accepted = await api("/environment/storage", {
        directory: path, previous: location.directory, migrate: location.has_environment,
      });
      setPending({ id: accepted.id, previousJob: job?.id, path });
    } catch (e) { setError(e.message); }
    finally { setSubmitting(false); }
  };
  return <section className="environment-versions environment-storage" aria-label="本机环境存储">
    <h4>本机环境存储</h4>
    <p className="field-help">Linux 镜像、下载缓存和虚拟磁盘可以放在另一块本地磁盘。项目文件保存在各自的项目文件夹。</p>
    <div className="setting-summary-row"><span>当前环境目录</span><strong>{location?.directory || "正在读取…"}</strong></div>
    {location?.backup && <p className="field-help">迁移前的备份保存在 <code>{location.backup}</code>。确认新环境能正常启动、文件和登录状态齐全后，可自行清理备份。</p>}
    <label>新存储位置
      <input value={directory} onChange={(e) => { setDirectory(e.target.value); setError(""); }} spellCheck="false"
        disabled={busy || !location} placeholder="本地磁盘上专用空文件夹的完整路径" />
    </label>
    <div className="storage-actions">
      {nativeAvailable() && <button type="button" disabled={busy || !location} onClick={choose}><FolderOpen size={15} />选择文件夹</button>}
      <button type="button" disabled={busy || !location || !directory.trim() || directory.trim() === location.directory} onClick={apply}>
        {location?.has_environment ? "迁移到此位置" : "使用此位置"}
      </button>
    </div>
    {job?.kind === "storage" && job.running && <p role="status" className="field-help">{job.message}</p>}
    {error && <p role="alert" className="error-message">{error}</p>}
  </section>;
}
