import React, { useEffect, useRef, useState } from "react";
import { Folder, ListFilter, RefreshCw, Search, Trash2 } from "lucide-react";
import { api } from "./api";
import { InlineSelect } from "./inline-select";
import "./cli-sessions.css";

export const desktopSessionRequest = (path, options = {}) => api("/cli" + path, options.body);
export const sessionsChanged = () => window.dispatchEvent(new Event("cage-cli-sessions-changed"));
const archiveDate = new Intl.DateTimeFormat("zh-CN", {year:"numeric",month:"long",day:"numeric",hour:"2-digit",minute:"2-digit",hour12:false});
const projectLabel = path => path.replaceAll("\\", "/").split("/").filter(Boolean).at(-1) || path;
const archiveFilters = {all:"全部会话",pinned:"已置顶",deleting:"删除未完成"};

function ArchiveSelection({ items, selected, onToggle, disabled, label, children }) {
  const input = useRef(null);
  const count = items.filter(item => selected.has(item.id)).length;
  useEffect(() => { input.current.indeterminate = count > 0 && count < items.length; }, [count, items.length]);
  return <label className="archive-selection check-line">
    <input ref={input} type="checkbox" aria-label={label} checked={items.length > 0 && count === items.length}
      disabled={disabled} onChange={() => onToggle(items)} />
    {children && <span>{children}</span>}
  </label>;
}

export function useCLISessions(enabled, visible) {
  const [sessions, setSessions] = useState([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [revision, setRevision] = useState(0);
  const refresh = () => { setLoading(true); setRevision(value => value + 1); };
  useEffect(() => {
    window.addEventListener("cage-cli-sessions-changed", refresh);
    return () => window.removeEventListener("cage-cli-sessions-changed", refresh);
  }, []);
  useEffect(() => {
    if (!enabled || !visible) { setLoading(false); return; }
    let live = true, timer;
    const load = async () => {
      setLoading(true);
      try {
        const result = await desktopSessionRequest("/sessions");
        if (!live) return;
        setSessions((result.sessions || []).map(session => result.notice ? { ...session, active: false, running: false } : session));
        setError(result.notice || "");
      } catch (error) {
        if (live) {
          setError(error.message);
          setSessions(previous => previous.map(session => ({ ...session, active: false, running: false })));
        }
      } finally { if (live) setLoading(false); }
      if (live) timer = setTimeout(load, 5000);
    };
    void load();
    return () => { live = false; clearTimeout(timer); };
  }, [enabled, visible, revision]);
  return { sessions, error, loading, refresh };
}

export function ArchivedSessions({ request = desktopSessionRequest, ready, onRestored }) {
  const [sessions, setSessions] = useState(null);
  const [error, setError] = useState("");
  const [readError, setReadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState("");
  const [revision, setRevision] = useState(0);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("all");
  const [project, setProject] = useState("");
  const [selected, setSelected] = useState(new Set());
  const [bulkConfirm, setBulkConfirm] = useState(null);
  const [resultMessage, setResultMessage] = useState("");
  const [progress, setProgress] = useState(null);
  useEffect(() => {
    let live = true, timer;
    const load = async () => {
      try {
        const result = await request("/sessions?archived=true");
        if (live) {
          const list = result.sessions || [];
          setSessions(list); setReadError(result.notice || "");
          setSelected(previous => new Set([...previous].filter(id => list.some(session => session.id === id))));
        }
      } catch (error) { if (live) setReadError(error.message); }
      if (live) timer = setTimeout(load, 5000);
    };
    if (!busy) void load();
    return () => { live = false; clearTimeout(timer); };
  }, [request, revision, busy]);
  const change = async (items, operation) => {
    if (busy) return;
    setBusy(true); setError(""); setResultMessage("");
    setProgress({done:0, total:items.length});
    const completed = new Set(), failures = [];
    try {
      for (const session of items) {
        try {
          await request("/sessions/" + session.id + "/" + operation, {body:operation === "purge" ? {confirm:true} : {}});
          completed.add(session.id);
        } catch (error) { failures.push({session, message:error.message}); }
        setProgress({done:completed.size + failures.length, total:items.length});
      }
      setSessions(list => list.filter(item => !completed.has(item.id)));
      setSelected(previous => new Set([...previous].filter(id => !completed.has(id))));
      if (completed.size) {
        setConfirming("");
        setResultMessage(`已${operation === "restore" ? "取消归档" : "永久删除"} ${completed.size} 个会话。`);
        sessionsChanged();
      }
      setBulkConfirm(null);
      if (failures.length) setError(`${failures.length} 个会话处理失败：${failures.map(({session,message}) => `“${session.title || "新会话"}”：${message}`).join("；")}`);
      if (operation === "restore") for (const id of completed) onRestored?.(id);
    } finally { setBusy(false); setProgress(null); }
  };
  const resetSelection = (setter, value) => {
    setter(value); setSelected(new Set()); setBulkConfirm(null); setConfirming(""); setResultMessage("");
  };
  const toggleSelection = items => {
    const all = items.every(item => selected.has(item.id));
    setSelected(previous => {
      const next = new Set(previous);
      for (const item of items) { if (all) next.delete(item.id); else next.add(item.id); }
      return next;
    });
    setBulkConfirm(null); setConfirming(""); setResultMessage("");
  };
  const projects = [...new Set(sessions?.map(session => session.workspace) || [])];
  const selectedProject = projects.includes(project) ? project : "";
  const search = query.trim().toLocaleLowerCase();
  const groups = new Map();
  for (const session of sessions || []) {
    if (selectedProject && session.workspace !== selectedProject) continue;
    if (filter === "pinned" && !session.pinned || filter === "deleting" && !session.deleting) continue;
    if (search && !(session.title + " " + session.workspace).toLocaleLowerCase().includes(search)) continue;
    if (!groups.has(session.workspace)) groups.set(session.workspace, []);
    groups.get(session.workspace).push(session);
  }
  const visible = [...groups.values()].flat();
  const selectedItems = visible.filter(session => selected.has(session.id));
  const hasDeleting = selectedItems.some(session => session.deleting);
  return <section className="archived-sessions" aria-label="已归档会话">
    <h2 className="archive-page-title">已归档会话</h2>
    <div className="archive-toolbar">
      <div className="archive-search"><Search size={18} aria-hidden="true" />
        <input type="search" aria-label="搜索已归档的会话" placeholder="搜索已归档的会话" value={query} disabled={busy} onChange={event => resetSelection(setQuery, event.target.value)} />
      </div>
      <InlineSelect aria-label="会话筛选" value={filter} disabled={busy} onValueChange={value => resetSelection(setFilter, value)}
        displayLabel={<span className="archive-filter-label"><ListFilter size={17}/><span>{archiveFilters[filter]}</span></span>}>
        {Object.entries(archiveFilters).map(([value,label]) => <option key={value} value={value}>{label}</option>)}
      </InlineSelect>
      <InlineSelect aria-label="项目筛选" value={selectedProject} disabled={busy} onValueChange={value => resetSelection(setProject, value)}
        displayLabel={<span className="archive-filter-label"><Folder size={17}/><span>{selectedProject ? projectLabel(selectedProject) : "所有项目"}</span></span>}>
        <option value="">所有项目</option>
        {projects.map(path => <option key={path} value={path}>{projects.some(other => other !== path && projectLabel(other) === projectLabel(path)) ? path : projectLabel(path)}</option>)}
      </InlineSelect>
    </div>
    {visible.length > 0 && <div className="archive-bulk-toolbar" role="group" aria-label="批量处理会话">
      <ArchiveSelection items={visible} selected={selected} onToggle={toggleSelection} disabled={busy} label="全选当前筛选结果">全选</ArchiveSelection>
      <span className="archive-note">已选择 {selectedItems.length} 个会话</span>
      <div className="archived-session-actions">
        <button className="archive-button" disabled={busy || !selectedItems.length} onClick={() => resetSelection(setSelected, new Set())}>清空选择</button>
        <button className="archive-restore" disabled={busy || !selectedItems.length || hasDeleting} onClick={() => change(selectedItems, "restore")}>批量取消归档</button>
        <button className="archive-button archive-delete" disabled={busy || !ready || !selectedItems.length} onClick={() => { setConfirming(""); setBulkConfirm(selectedItems); }}>批量删除</button>
      </div>
      {hasDeleting && <p className="archive-note archive-bulk-note">所选会话包含删除未完成项，请取消选择这些会话后再取消归档。</p>}
    </div>}
    {progress && <p role="status" className="archive-note">正在处理 {progress.done} / {progress.total} 个会话…</p>}
    {resultMessage && <p role="status" className="archive-note">{resultMessage}</p>}
    {bulkConfirm && <div className="archive-confirm" role="group" aria-label="确认批量永久删除会话">
      <p>永久删除所选的 {bulkConfirm.length} 个会话及其 Claude Code 原始对话和子代理记录？此操作无法撤销。</p>
      <div className="archived-session-actions"><button className="archive-button" disabled={busy} onClick={() => setBulkConfirm(null)}>取消</button>
        <button className="archive-button archive-delete" disabled={busy || !ready} onClick={() => change(bulkConfirm, "purge")}>永久删除所选会话</button></div>
    </div>}
    {(error || readError) && <div role="alert" className="archive-error">{error || readError}
      {readError && <button className="archive-button" onClick={() => setRevision(value => value + 1)} disabled={busy}>重新读取</button>}
    </div>}
    {!sessions && !readError && <p role="status">正在读取…</p>}
    {sessions?.length === 0 && <p className="archive-note">暂无已归档会话。</p>}
    {Boolean(sessions?.length) && !groups.size && <p className="archive-note">没有符合筛选条件的已归档会话。</p>}
    {[...groups].map(([path,items]) => <section className="archive-project" key={path} aria-label={projectLabel(path) + "的已归档会话"}>
      <header className="archive-project-heading">
        <ArchiveSelection items={items} selected={selected} onToggle={toggleSelection} disabled={busy} label={`全选项目 ${path} 的当前筛选结果`} />
        <Folder size={18}/><h3 title={path}>{projectLabel(path)}</h3><span>{items.length} 个会话</span></header>
      <div className="archive-project-list">{items.map(session => {
        const date = new Date(session.updated_at);
        return <article className="archived-session" key={session.id}>
          <div className="archived-session-row">
            <ArchiveSelection items={[session]} selected={selected} onToggle={toggleSelection} disabled={busy} label={`选择会话 ${session.title || "新会话"}`} />
            <div className="archived-session-title"><h4>{session.title || "新会话"}</h4>
              {!Number.isNaN(date.valueOf()) && <time dateTime={session.updated_at}>{archiveDate.format(date)}</time>}
            </div>
            <div className="archived-session-actions">
              <button className="archive-icon-button" aria-label={session.deleting ? "重试删除" : "删除会话"} title={session.deleting ? "重试删除" : "删除会话"}
                disabled={busy || !ready} onClick={() => { setBulkConfirm(null); session.deleting ? change([session], "purge") : setConfirming(session.id); }}>
                {session.deleting ? <RefreshCw size={16}/> : <Trash2 size={16}/>}</button>
              <button className="archive-restore" disabled={busy || session.deleting} onClick={() => change([session], "restore")}>取消归档</button>
            </div>
          </div>
          {session.deleting && <p className="archive-note">永久删除尚未完成，连接恢复后可重试。</p>}
          {confirming === session.id && <div className="archive-confirm" role="group" aria-label="确认永久删除会话">
            <p>永久删除“{session.title || "新会话"}”及其 Claude Code 原始对话和子代理记录？此操作无法撤销。</p>
            <div className="archived-session-actions"><button className="archive-button" disabled={busy} onClick={() => setConfirming("")}>取消</button>
              <button className="archive-button archive-delete" disabled={busy || !ready} onClick={() => change([session], "purge")}>永久删除</button></div>
          </div>}
        </article>;
      })}</div>
    </section>)}
    {!ready && <p className="archive-note">启动主机环境后可以永久删除 Claude Code 原始记录。</p>}
  </section>;
}
