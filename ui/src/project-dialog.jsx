import { isWindows, isMac } from "./platform.mjs";
import React, { useState } from "react";
import { FolderOpen, LoaderCircle, Plus } from "lucide-react";
import { api, native, nativeAvailable } from "./api";
import { Modal } from "./views";
import { projectName } from "./workspace-state.mjs";
import { isRemoteClient } from "./client-context.mjs";

export function ProjectDialog({
  status,
  initialPath = "",
  activate = true,
  onClose,
  onOpened,
}) {
  const projects = status.projects || [];
  const [adding, setAdding] = useState(Boolean(initialPath) || projects.length === 0);
  const [path, setPath] = useState(initialPath || status.workspace || projects[0] || "");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [directories, setDirectories] = useState(null);
  const [browsing, setBrowsing] = useState(false);
  const remote = isRemoteClient();
  const busy = submitting || Boolean(status.job?.running);
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
    event.preventDefault();
    if (busy || !path.trim()) return;
    setSubmitting(true);
    setError("");
    try {
      const result = await api("/projects/open", {
        workspace: path.trim(),
        add: adding,
        activate,
      });
      await onOpened(result.workspace);
    } catch (e) {
      setError(e.message);
    } finally {
      setSubmitting(false);
    }
  };
  return (
    <Modal
      title={activate ? "打开项目" : "选择工作项目"}
      onClose={busy ? undefined : onClose}
      footer={
        <>
          <span className="save-note">
            {busy
              ? submitting ? "正在连接项目…" : status.job?.message
              : adding ? "添加到 Cage 项目列表" : "使用已授权的 Cage 项目"}
          </span>
          <button disabled={busy} onClick={onClose}>
            取消
          </button>
          <button
            className="primary"
            form="open-project"
            type="submit"
            disabled={!path.trim() || busy}
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
        {!adding && <>
          <fieldset className="project-picker-list" disabled={busy}>
            <legend>Cage 项目</legend>
            {projects.map((project) => <label className={`project-picker-item ${path === project ? "selected" : ""}`} key={project}>
              <input type="radio" name="project" value={project} checked={path === project} onChange={() => { setPath(project); setError(""); }} />
              <FolderOpen size={18} aria-hidden="true" />
              <span>
                <strong>{project === status.environment_directory ? "默认工作区" : projectName(project)}</strong>
                <small>{project}</small>
                {status.project_errors?.some((item) => item.path === project) && <small>需要重新连接，选择后重试</small>}
              </span>
            </label>)}
          </fieldset>
          <button type="button" className="quiet-button project-picker-add" onClick={add} disabled={busy}><Plus size={16} />{remote ? "添加云端项目…" : "添加本机项目…"}</button>
        </>}
        {adding && <>
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
          {remote ? "项目文件保存在云端，修改会直接影响云端文件。本机文件需要通过上传传递。" : "这个文件夹中的改动会同步到本机。"}所有项目共用 Cage 环境，已有终端和网页会保持运行。
        </p>
        {error && (
          <div className="error-message" role="alert">
            {error}
          </div>
        )}
      </form>
    </Modal>
  );
}
