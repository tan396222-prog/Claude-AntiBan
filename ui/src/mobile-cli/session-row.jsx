import React, { useRef, useState } from "react";
import { Archive, LoaderCircle, MoreHorizontal, Pencil, Pin, PinOff, Terminal } from "lucide-react";

export const sortSessions = (a, b) => Number(Boolean(b.active)) - Number(Boolean(a.active)) || Number(Boolean(b.pinned)) - Number(Boolean(a.pinned)) ||
  String(b.updated_at).localeCompare(String(a.updated_at));

export function SessionRow({ session, current, disabled, onSelect, onAction, detail }) {
  const [revealed, setRevealed] = useState(false);
  const [offset, setOffset] = useState(null);
  const gesture = useRef(null);
  const suppressClick = useRef(false);
  const selection = useRef(null);
  const width = 120;
  const reveal = -(offset ?? (revealed ? -width : 0));
  const finish = (event, cancelled = false) => {
    const start = gesture.current;
    if (!start || start.id !== event.pointerId) return;
    if (start.horizontal) {
      if (!cancelled) setRevealed(start.offset <= -width / 2);
      suppressClick.current = true;
    }
    gesture.current = null;
    setOffset(null);
  };
  const toggle = () => { setRevealed(value => !value); setOffset(null); requestAnimationFrame(() => selection.current?.focus()); };
  return <li className="swipe-session" style={{ "--session-reveal": width + "px" }}>
    <div className="session-actions" role="group" aria-label={(session.title || "新会话") + "的操作"} hidden={!revealed && offset === null}
      onKeyDown={event => { if (event.key === "Escape" || event.key === "ArrowRight") { event.preventDefault(); event.stopPropagation(); setRevealed(false); selection.current?.focus(); } }}>
      <button type="button" aria-label={session.pinned ? "取消置顶" : "置顶"} title={session.pinned ? "取消置顶" : "置顶"}
        disabled={disabled} onClick={() => onAction("pin", session)}>
        {session.pinned ? <PinOff size={16} /> : <Pin size={16} />}
      </button>
      <button type="button" aria-label="重命名" title="重命名" disabled={disabled} onClick={() => onAction("rename", session)}><Pencil size={16} /></button>
      <button type="button" aria-label="归档" title="归档" disabled={disabled} onClick={() => onAction("archive", session)}><Archive size={16} /></button>
    </div>
    <div className="session-slide" style={{ width:`calc(100% - ${reveal}px)`, transition: offset === null ? undefined : "none" }}
      onPointerDown={event => {
        if (disabled || !event.isPrimary || event.button !== 0) return;
        suppressClick.current = false;
        gesture.current = { id:event.pointerId, x:event.clientX, y:event.clientY, initial:revealed ? -width : 0, offset:revealed ? -width : 0 };
      }}
      onPointerMove={event => {
        const start = gesture.current;
        if (!start || start.id !== event.pointerId) return;
        const dx = event.clientX - start.x, dy = event.clientY - start.y;
        if (!start.horizontal) {
          if (Math.abs(dy) > 10 && Math.abs(dy) >= Math.abs(dx)) { gesture.current = null; return; }
          if (Math.abs(dx) < 12 || Math.abs(dx) <= Math.abs(dy)) return;
          start.horizontal = true;
          event.currentTarget.setPointerCapture(event.pointerId);
        }
        event.preventDefault();
        start.offset = Math.max(-width, Math.min(0, start.initial + dx));
        setOffset(start.offset);
      }}
      onPointerUp={event => finish(event)} onPointerCancel={event => finish(event, true)}
      onClickCapture={event => {
        if (!suppressClick.current) return;
        suppressClick.current = false;
        event.preventDefault(); event.stopPropagation();
      }}
      onKeyDown={event => {
        if (event.key === "ArrowLeft") { event.preventDefault(); setRevealed(true); }
        if (event.key === "ArrowRight" || event.key === "Escape" && revealed) { event.preventDefault(); event.stopPropagation(); setRevealed(false); }
      }}>
      <button ref={selection} type="button" className="session-select" aria-label={session.title || "新会话"} aria-current={current ? "page" : undefined} disabled={disabled}
        onClick={() => { if (revealed) setRevealed(false); else onSelect(); }}>
        <Terminal size={15}/><span className="row-detail"><strong>{session.title || "新会话"}</strong>{detail && <small>{detail}</small>}</span>
        {session.pinned && <Pin size={12} aria-label="已置顶"/>}
        {session.active && <LoaderCircle size={13} className="spin" role="img" aria-label="会话正在运行"/>}
      </button>
      <button type="button" className="session-more" aria-label={(session.title || "新会话") + "的更多操作"}
        aria-expanded={revealed} style={{display:reveal ? "none" : undefined}} disabled={disabled} onClick={toggle}><MoreHorizontal size={17}/></button>
    </div>
  </li>;
}

export function SessionActionDialog({ action, busy, error, onCancel, onSubmit }) {
  const panel = useRef(null);
  const [title, setTitle] = useState(action.session.title || "");
  const rename = action.kind === "rename";
  React.useEffect(() => { panel.current.showModal(); }, []);
  return <dialog className="session-dialog" ref={panel} aria-label={rename ? "重命名会话" : "归档会话"}
    onCancel={event => { event.preventDefault(); if (!busy) onCancel(); }}>
    <form onSubmit={event => { event.preventDefault(); onSubmit(rename ? {title:title.trim()} : {}); }}>
      <h2>{rename ? "重命名会话" : "归档会话？"}</h2>
      {rename ? <label>会话名称<input autoFocus value={title} maxLength={100} disabled={busy} onChange={event => setTitle(event.target.value)}/></label>
        : <p>归档“{action.session.title || "新会话"}”会关闭关联终端并保留历史。可以在设置中的“已归档会话”撤销归档。</p>}
      {error && <p role="alert" className="session-action-error">{error}</p>}
      <footer><button type="button" disabled={busy} onClick={onCancel}>取消</button>
        <button type="submit" disabled={busy || rename && !title.trim()} className="action">{busy ? "正在保存…" : rename ? "保存名称" : "归档会话"}</button></footer>
    </form>
  </dialog>;
}
