import { isWindows, isMac } from "./platform.mjs";
import React, { useEffect, useState } from "react";
import { FolderOpen, LoaderCircle, Plus } from "lucide-react";
import { api, native, nativeAvailable } from "./api";
import { SafetyMessage } from "./safety-message";
import { Modal } from "./views";
import { MacOSProjectApproval } from "./macos-project-approval";
import { ProjectRecovery } from "./project-recovery";
import { projectName, rememberClientProjectName } from "./workspace-state.mjs";
import { getClientContext, isRemoteClient } from "./client-context.mjs";

export function ProjectDialog({
  status,
  initialPath = "",
  activate = true,
  onClose,
  onOpened,
  onRemoved,
  onSettings,
}) {
  const projects = status.projects || [];
  const [adding, setAdding] = useState(Boolean(initialPath) || projects.length === 0);
  const [path, setPath] = useState(initialPath || status.workspace || projects[0] || "");
  const [error, setError] = useState("");
  const [recovery, setRecovery] = useState("");
  const [pending, setPending] = useState(null);
  const [submitting, setSubmitting] = useState(false);
  const [removeTarget, setRemoveTarget] = useState("");
  const [removed, setRemoved] = useState([]);
  const [directories, setDirectories] = useState(null);
  const [browsing, setBrowsing] = useState(false);
  const remote = isRemoteClient();
  const context = getClientContext();
  const canShare = remote && nativeAvailable() && context.connection?.manifest?.capabilities?.client_project_share === true;
  const [shares, setShares] = useState([]);
  const [writeAccess, setWriteAccess] = useState(false);
  const [sharing, setSharing] = useState(false);
  useEffect(() => {
    if (!canShare) return;
    let current = true;
    api("/client/project-shares").then(({ shares }) => {
      if (current) {
        (shares || []).forEach(rememberClientProjectName);
        setShares(shares || []);
      }
    }).catch((e) => { if (current) setError(e.message); });
    return () => { current = false; };
  }, [canShare, context.connection_id]);
  const share = async () => {
    setSubmitting(true); setError("");
    try {
      const item = await native("shareLocalProject", { mode: writeAccess ? "write" : "read" });
      if (item.canceled) return;
      rememberClientProjectName(item);
      setShares((old) => [...old.filter((s) => s.id !== item.id), item]);
      setPath(item.guest_path); setAdding(false); setSharing(false);
      const result = await api("/projects/open", { workspace: item.guest_path, add: false, activate });
      await onOpened(result.workspace);
    } catch (e) { setError(e.message); }
    finally { setSubmitting(false); }
  };
  const revoke = async (item) => {
    setSubmitting(true); setError("");
    try {
      await api("/client/project-shares/" + item.id, undefined, "DELETE");
      setShares((old) => old.filter((s) => s.id !== item.id));
      if (path === item.guest_path) setPath(status.environment_directory || "");
    } catch (e) { setError(e.message); }
    finally { setSubmitting(false); }
  };
  const projectList = [...new Set([...projects, ...shares.map((s) => s.guest_path)])].filter(project => !removed.includes(project));
  const selectedShare = shares.find((item) => item.guest_path === path);
  const busy = submitting || Boolean(status.job?.running);
  const remove = async () => {
    if (busy || !removeTarget) return;
    setSubmitting(true); setError("");
    try {
      const result = await api("/projects/remove", { workspace: removeTarget });
      setRemoved(previous => [...previous, removeTarget]);
      setPath(result.workspace || status.environment_directory || "");
      setRemoveTarget("");
      await onRemoved?.(removeTarget, result.workspace);
    } catch (e) { setError(e.message); }
    finally { setSubmitting(false); }
  };
  const browse = async (directory = path.trim()) => {
    setBrowsing(true); setError("");
    try { setDirectories(await api(`/projects/directories?${new URLSearchParams({ path: directory })}`)); }
    catch (e) { setError(e.message); }
    finally { setBrowsing(false); }
  };
  const choose = async () => {
    if (remote) { await browse(); return; }
    try {
      const selected = await native("chooseFolder");
      if (selected) {
        setPath(selected);
        setError("");
      }
    } catch {
      setError("未能选择文件夹，请直接填写路径。");
    }
  };
  const add = () => {
    setAdding(true);
    setPath("");
    setError("");
    if (remote) void browse("");
    else if (nativeAvailable()) void choose();
  };
  const open = async (event) => {
    event?.preventDefault();
    if (busy || sharing || removeTarget || !status.ready || !path.trim()) return;
    setSubmitting(true);
    setError("");
    setRecovery("");
    try {
      const result = await api("/projects/open", {
        workspace: path.trim(),
        add: adding,
        activate,
      });
      if (result.approval) setPending({ workspace: result.workspace, grant: result.approval });
      else await onOpened(result.workspace);
    } catch (e) {
      setError(e.message);
      setRecovery(e.recovery || "");
    } finally {
      setSubmitting(false);
    }
  };
  if (pending) return <MacOSProjectApproval pending={pending} onClose={onClose} onSettings={onSettings} onCheck={async () => {
    const result = await api("/projects/open", { workspace: pending.workspace, add: adding, activate });
    if (result.approval) {
      setPending({workspace: result.workspace, grant: result.approval});
      return;
    }
    await onOpened(result.workspace);
  }} />;
  return (
    <Modal
      title={activate ? "打开项目" : "选择工作项目"}
      onClose={busy ? undefined : onClose}
      footer={
        <>
          <span className="save-note">
            {busy
              ? submitting ? removeTarget ? "正在移除项目…" : "正在连接项目…" : status.job?.message
              : adding ? "添加到 Cage 项目列表" : "使用已授权的 Cage 项目"}
          </span>
          <button disabled={busy} onClick={onClose}>
            取消
          </button>
          <button
            className="primary"
            form="open-project"
            type="submit"
            disabled={!path.trim() || busy || sharing || Boolean(removeTarget) || !status.ready}
          >
            {busy ? (
              <LoaderCircle className="spin" size={15} />
            ) : (
              <FolderOpen size={15} />
            )}
            {activate ? "打开项目" : "使用此项目"}
          </button>
        </>
      }
    >
      <form
        id="open-project"
        className="modal-body project-form"
        onSubmit={open}
      >
        <p>{activate ? remote ? "选择已有项目，或添加一个云端项目文件夹。" : "选择已有项目，或添加一个本机项目文件夹。" : "从 Cage 项目列表选择工作目录。新项目由 Cage 授权并共享。"}</p>
        {canShare && <>
          <button type="button" className="quiet-button project-picker-add" onClick={() => { setSharing(!sharing); setWriteAccess(false); }} disabled={busy} aria-expanded={sharing}>
            <FolderOpen size={16} />共享本机项目…
          </button>
          {sharing && <fieldset className="project-picker-list project-permissions" disabled={busy}>
            <legend>共享给{context.name || "当前云端 Cage"}</legend>
            <p className="field-help">云端按需读取项目。凭据、Git 元数据和链接受限。断开连接后共享结束。</p>
            <label><input type="checkbox" checked={writeAccess} onChange={(e) => setWriteAccess(e.target.checked)} />允许云端修改和删除本机项目文件</label>
            <button type="button" onClick={share}>选择本机文件夹…</button>
          </fieldset>}
        </>}
        {!sharing && !adding && <>
          <fieldset className="project-picker-list" disabled={busy || Boolean(removeTarget)}>
            <legend>Cage 项目</legend>
            {projectList.map((project) => <label className={`project-picker-item ${path === project ? "selected" : ""}`} key={project}>
              <input type="radio" name="project" value={project} checked={path === project} onChange={() => { setPath(project); setError(""); }} />
              <FolderOpen size={18} aria-hidden="true" />
              <span>
                <strong>{project === status.environment_directory ? "默认工作区" : projectName(project)}</strong>
                <small>{shares.find((s) => s.guest_path === project)?.path || project}</small>
                {status.project_errors?.some((item) => item.path === project) && <small>需要重新连接，选择后重试</small>}
              </span>
            </label>)}
          </fieldset>
          {selectedShare && <button type="button" disabled={submitting} onClick={() => revoke(selectedShare)}>停止共享本机项目（{selectedShare.readonly ? "只读" : "读写"}）</button>}
          {activate && !selectedShare && path && path !== status.environment_directory && !removeTarget && <button type="button" className="quiet-button" disabled={busy} onClick={() => { setRemoveTarget(path); setError(""); setRecovery(""); }}>从 Cage 移除此项目</button>}
          {removeTarget && <section className="project-picker-list" aria-label="确认移除项目">
            <p>移除“{projectName(removeTarget)}”将解除目录共享。原文件和会话历史保留，重新添加相同目录后可继续使用历史。</p>
            <p className="field-help">请先保存文件，并关闭此项目的终端、运行中会话和编辑器。移除当前项目后会选中默认工作区。</p>
            <button type="button" disabled={busy} onClick={() => { setRemoveTarget(""); setError(""); }}>取消移除</button>
            <button type="button" disabled={busy} onClick={remove}>确认移除项目</button>
          </section>}
          <button type="button" className="quiet-button project-picker-add" onClick={add} disabled={busy || Boolean(removeTarget) || !status.ready}><Plus size={16} />{remote ? "添加云端项目…" : "添加本机项目…"}</button>
        </>}
        {!sharing && adding && <>
          {projects.length > 0 && <button type="button" className="quiet-button project-picker-add" disabled={busy} onClick={() => { setAdding(false); setPath(status.workspace || projects[0]); setError(""); }}>返回已有项目</button>}
          <label htmlFor="project-path">新项目文件夹</label>
          <div className="folder-input">
          <input
            id="project-path"
            autoFocus
            value={path}
            onChange={(event) => {
              setPath(event.target.value);
              setError("");
            }}
            placeholder={remote ? "/home/cage/Projects/my-project" : isWindows ? "C:/Users/你的名字/Projects/my-project" : isMac ? "/Users/你的名字/Projects/my-project" : "/home/你的名字/Projects/my-project"}
            disabled={busy}
            spellCheck="false"
            autoComplete="off"
          />
          {(remote || nativeAvailable()) && (
            <button type="button" onClick={choose} disabled={busy || browsing}>
              {browsing ? "正在读取…" : remote ? "浏览云端文件夹…" : "选择文件夹…"}
            </button>
          )}
          </div>
          {remote && directories && <section className="remote-directory-picker" aria-label="云端文件夹">
            <div className="remote-directory-heading"><strong>{directories.path}</strong><button type="button" disabled={browsing} onClick={() => setDirectories(null)}>收起</button></div>
            <div className="remote-directory-list">
              {directories.parent && <button type="button" disabled={browsing} onClick={() => browse(directories.parent)}>返回上级文件夹</button>}
              {directories.directories?.map((directory) => <button type="button" key={directory.path} disabled={browsing} onClick={() => browse(directory.path)}><FolderOpen size={16} />{directory.name}</button>)}
              {directories.directories?.length === 0 && <p className="field-help">这个目录下没有可浏览的文件夹。</p>}
            </div>
            <button type="button" disabled={browsing || !directories.selectable} onClick={() => { setPath(directories.path); setDirectories(null); }}>选择此云端文件夹</button>
          </section>}
        </>}
        <p className="field-help">
          {remote ? "云端项目保存在服务器上。" : "这个文件夹中的改动会同步到本机。"}所有项目共用 Cage 环境，已有终端和网页会保持运行。
        </p>
        {!status.ready && <p className="field-help">环境就绪后可添加或打开项目。已有项目仍可从列表移除。</p>}
        {error && (
          <div className="error-message" role="alert">
            <SafetyMessage message={error} />
            {recovery && <ProjectRecovery recovery={recovery} disabled={busy} onRetry={() => void open()}
              onSettings={onSettings} onSessionClosed={() => void open()} />}
          </div>
        )}
      </form>
    </Modal>
  );
}
