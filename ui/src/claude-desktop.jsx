import { InlineSelect } from "./inline-select";
import React, { useEffect, useRef, useState } from "react";
import {
  Download,
  Monitor,
  PanelLeftOpen,
  Power,
  RefreshCw,
} from "lucide-react";
import { BrowserUploads } from "./browser-uploads";
import { ProjectDialog } from "./project-dialog";
import { api } from "./api";
import { RemoteView } from "./views";
import "./claude-desktop.css";

const desktopZooms = [0.75, 0.9, 1, 1.1, 1.25, 1.5];

export function ClaudeDesktop({
  uploads = [],
  status,
  onProjectOpened,
  visible,
  remoteRef,
  onStatus,
  onFallback,
  onConfirm,
  onDetails,
  onExpand,
  sidebarCollapsed,
}) {
  const [closedChooser, setClosedChooser] = useState("");
  const directoryRequest = uploads.find((item) => item.directory && item.id !== closedChooser);
  const [zoom, setZoom] = useState(() => {
    try {
      const value = Number(localStorage.getItem("cage-desktop-zoom"));
      return desktopZooms.includes(value) ? value : 1;
    } catch { return 1; }
  });
  const [desktop, setDesktop] = useState(null);
  const [error, setError] = useState("");
  const [statusError, setStatusError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [jobID, setJobID] = useState("");
  const opened = useRef(false);
  const ownJob = status.job?.kind?.startsWith("claude-desktop-");
  const busy = submitting || Boolean(status.job?.running) || Boolean(jobID);
  const refresh = async () => {
    try {
      const value = await api("/claude-desktop");
      setDesktop(value);
      setStatusError("");
      return value;
    } catch (e) {
      setStatusError(e.message);
      return null;
    }
  };
  const run = async (action) => {
    setSubmitting(true);
    setError("");
    try {
      const job = await api(`/claude-desktop/${action}`, {});
      setJobID(job.id);
    } catch (e) {
      setError(e.message);
    } finally {
      setSubmitting(false);
    }
  };
  useEffect(() => {
    if (!status.vm_running || status.job?.running) return;
    let disposed = false;
    const poll = async () => {
      const value = await refresh();
      if (disposed) return;
      if (value && !opened.current) {
        opened.current = true;
        if (value.installed && !value.running && !value.failed && !jobID)
          await run("start");
      }
    };
    poll();
    const timer = setInterval(poll, 5000);
    return () => {
      disposed = true;
      clearInterval(timer);
    };
  }, [status.vm_running, status.job?.running]);
  useEffect(() => {
    if (!jobID || status.job?.id !== jobID || status.job.running) return;
    setJobID("");
    if (status.job.error) {
      setError(status.job.error);
      return;
    }
    if (status.job.kind === "claude-desktop-install") run("start");
    else refresh();
  }, [status.job]);
  return (
    <section className="claude-desktop-view" aria-label="Claude Desktop">
      <BrowserUploads requests={uploads} onStatus={onStatus} />
      {directoryRequest && <ProjectDialog
        key={directoryRequest.id}
        status={status}
        activate={false}
        onClose={() => {
          setClosedChooser(directoryRequest.id);
          void api("/input/files/cancel", { target: "desktop", id: directoryRequest.id }).catch((e) => onStatus(e.message));
        }}
        onOpened={async (workspace) => {
          await api("/input/files/directory", { target: "desktop", id: directoryRequest.id, workspace });
          setClosedChooser(directoryRequest.id);
          await onProjectOpened(workspace);
        }}
      />}
      <header className="claude-desktop-toolbar">
        {sidebarCollapsed && (
          <button
            className="icon-button"
            aria-label="展开侧栏"
            onClick={onExpand}
          >
            <PanelLeftOpen size={17} />
          </button>
        )}
        <Monitor size={18} />
        <strong>Claude Desktop</strong>
        <span className="desktop-version">
          {desktop?.version ? `v${desktop.version}` : "Linux"}
        </span>
        <span className="desktop-toolbar-spacer" />
        {desktop?.running && (
          <label className="desktop-display-size">
            显示大小
            <InlineSelect aria-label="Desktop 显示大小" value={zoom} onValueChange={(next) => {
              const value = Number(next);
              setZoom(value);
              try { localStorage.setItem("cage-desktop-zoom", String(value)); } catch { /* Session-only preference. */ }
            }}>
              {desktopZooms.map((value) => <option key={value} value={value}>{Math.round(value * 100)}%{value === 1 ? "（默认）" : ""}</option>)}
            </InlineSelect>
          </label>
        )}
        {desktop?.running && (
          <button
            className="quiet-button"
            disabled={busy}
            onClick={async () => {
              if (
                await onConfirm({
                  title: "关闭 Claude Desktop？",
                  body: "请先保存应用内的工作。浏览器和 Cage 终端会继续运行。",
                  confirm: "关闭应用",
                })
              )
                run("stop");
            }}
          >
            <Power size={15} />
            关闭应用
          </button>
        )}
        <button
          className="icon-button"
          aria-label="刷新 Claude Desktop 状态"
          disabled={busy}
          onClick={refresh}
        >
          <RefreshCw size={15} />
        </button>
      </header>
      {(error || statusError || desktop?.failed) && (
        <div className="connection-banner" role="alert">
          {error || statusError || (desktop?.failure_reason === "oom-kill"
            ? "环境内存不足，Claude Desktop 已被系统关闭。请在设置中检查计算资源后重新打开。"
            : "Claude Desktop 未能启动，可重试打开或修复安装。")}
          <button onClick={onDetails}>操作详情</button>
        </div>
      )}
      {desktop?.running && desktop.managed === false && <div className="connection-banner" role="status">当前应用尚未接入 Cage 账户解锁。请先保存并关闭应用，再重新打开。</div>}
      {busy && ownJob && (
        <div className="desktop-progress" role="status">
          <RefreshCw size={16} className="spin" />
          {status.job.message}
          <button className="text-button" onClick={onDetails}>
            查看进度
          </button>
        </div>
      )}
      <div className="desktop-screen">
        {desktop?.running && status.vm_running ? (
          <RemoteView
            ref={remoteRef}
            desktop
            zoom={zoom}
            visible={visible}
            keyboard={status.keyboard}
            onStatus={onStatus}
            onFallback={onFallback}
          />
        ) : (
          <div className="desktop-empty">
            <Monitor size={36} />
            <h2>
              {desktop?.installed
                ? "Claude Desktop"
                : "在 Cage 中使用 Claude Desktop"}
            </h2>
            <p>
              Chat 和 Code 共用一个官方应用。在 Cage
              内安装、登录并使用，项目文件来自已打开的文件夹。
            </p>
            {!desktop?.installed && (
              <p>
                首次使用时从 Anthropic 官方源下载，不增加 Cage 默认安装包体积。
              </p>
            )}
            <div className="desktop-actions">
              {desktop && (
                <button
                  className="primary"
                  disabled={busy || !status.vm_running}
                  onClick={() => run(desktop.installed ? "start" : "install")}
                >
                  {desktop.installed ? (
                    <Monitor size={16} />
                  ) : (
                    <Download size={16} />
                  )}
                  {desktop.installed ? "打开 Claude Desktop" : "下载并安装"}
                </button>
              )}
              {desktop?.installed && (
                <button
                  className="quiet-button"
                  disabled={busy}
                  onClick={() => run("install")}
                >
                  更新 / 修复安装
                </button>
              )}
              {!desktop && (
                <button
                  className="quiet-button"
                  disabled={busy}
                  onClick={refresh}
                >
                  重新读取状态
                </button>
              )}
            </div>
            <p className="desktop-note">
              关闭应用后释放图形会话资源，登录状态保留在 Cage
              环境中。需要额外虚拟机的 Cowork 功能暂未适配。
            </p>
          </div>
        )}
      </div>
    </section>
  );
}
