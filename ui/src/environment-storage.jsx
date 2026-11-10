import React, { useEffect, useState } from "react";
import { FolderOpen } from "lucide-react";
import { api, native, nativeAvailable } from "./api";
import { SafetyMessage } from "./safety-message";

export function EnvironmentStorage({ status, onConfirm }) {
  const [location, setLocation] = useState(null);
  const [pending, setPending] = useState(null);
  const [directory, setDirectory] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const job = status.job;
  const recovery = status.storage_recovery?.required ? status.storage_recovery : location?.recovery;
  const storageJob = job?.kind === "storage" || job?.kind === "storage-recovery";
  const busy = submitting || Boolean(pending) || Boolean(job?.running);
  useEffect(() => {
    let live = true;
    if (pending && job?.id === pending.previousJob) return;
    if (storageJob && job.running) return;
    api("/environment/storage").then((next) => {
      if (!live) return;
      setLocation(next);
      if (pending) {
        setPending(null);
        if (job?.id === pending.id && job.error) setError(job.error);
        else if (job?.id === pending.id || next.directory && next.directory === pending.path) { setDirectory(""); setError(""); }
        else setError("操作已结束，请核对当前环境目录后重试。");
      }
    }).catch((e) => {
      if (live) { setError(e.message); setPending(null); }
    });
    return () => { live = false; };
  }, [pending, job?.id, job?.running, storageJob, status.storage_recovery?.required]);

  const choose = async () => {
    setError("");
    try {
      const selected = await native("chooseFolder", { purpose: "environmentStorage" });
      if (selected) setDirectory(selected);
    } catch { setError("未能选择文件夹，请直接填写绝对路径。"); }
  };
  const apply = async () => {
    if (!location?.directory || busy || recovery?.required) return;
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
  const recover = async (repair) => {
    if (busy) return;
    setSubmitting(true);
    setError("");
    try {
      const accepted = await api("/environment/storage/recover", { repair });
      setPending({ id: accepted.id, previousJob: job?.id });
    } catch (e) { setError(e.message); }
    finally { setSubmitting(false); }
  };
  return <section className="environment-versions environment-storage" aria-label="本机环境存储">
    <h4>本机环境存储</h4>
    {recovery?.required ? <>
      <p className="field-help">环境存储需要恢复。请连接迁移使用的磁盘后重试。恢复完成后可以启动环境。</p>
      <p role="alert" className="error-message"><SafetyMessage message={recovery.error} /></p>
      <div className="storage-actions">
        <button type="button" disabled={busy} onClick={() => recover(false)}>重试恢复</button>
        {recovery.repair_allowed && <button type="button" disabled={busy} onClick={() => recover(true)}>修复系统助手并恢复</button>}
      </div>
      {recovery.repair_allowed && <p className="field-help storage-repair-help">修复系统助手需要 Windows 系统授权。完成后会继续恢复原环境。</p>}
    </> : <>
    <p className="field-help">系统镜像、下载缓存和虚拟磁盘可以放在另一块本地磁盘。项目文件保存在各自的项目文件夹。</p>
    <div className="setting-summary-row"><span>当前环境目录</span><strong>{location?.directory || "正在读取…"}</strong></div>
    {status.environment_identity_directory && status.environment_identity_directory !== status.environment_directory &&
      <p className="field-help">升级前的默认项目文件位于 <code>{status.environment_identity_directory}</code>，当前默认项目位于 <code>{status.environment_directory}</code>。如需使用旧目录中的项目，请在主机文件管理器中将项目移到 Cage 配置目录之外，再从工作台打开。</p>}
    {location?.backup && <p className="field-help">迁移前的备份保存在 <code>{location.backup}</code>。确认新环境能正常启动、文件和登录状态齐全后，可自行清理备份。</p>}
    <label>新存储位置
      <input value={directory} onChange={(e) => { setDirectory(e.target.value); setError(""); }} spellCheck="false"
        disabled={busy || !location?.directory} placeholder="本地磁盘上专用空文件夹的完整路径" />
    </label>
    <div className="storage-actions">
      {nativeAvailable() && <button type="button" disabled={busy || !location?.directory} onClick={choose}><FolderOpen size={15} />选择文件夹</button>}
      <button type="button" disabled={busy || !location?.directory || !directory.trim() || directory.trim() === location.directory} onClick={apply}>
        {location?.has_environment ? "迁移到此位置" : "使用此位置"}
      </button>
    </div>
    </>}
    {storageJob && job.running && <p role="status" className="field-help">{job.message}</p>}
    {error && error !== recovery?.error && <p role="alert" className="error-message"><SafetyMessage message={error} /></p>}
  </section>;
}
