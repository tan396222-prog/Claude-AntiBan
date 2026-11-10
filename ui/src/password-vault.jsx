import React, { useEffect, useRef, useState } from "react";
import { KeyRound, Plus, Search, ShieldCheck } from "lucide-react";
import { api, writeClipboard } from "./api";
import "./password-vault.css";

const emptyBrowser = { available: false, entries: [], candidates: [] };
export function usePasswordVault(ready, tab) {
  const [state, setState] = useState(emptyBrowser);
  const refresh = useRef(null), mutation = useRef(0);
  useEffect(() => {
    let cancelled = false, timer, running = false;
    setState(emptyBrowser);
    const poll = async () => {
      if (running || cancelled || !ready) return;
      running = true;
      const revision = mutation.current;
      clearTimeout(timer);
      try {
        const value = await api("/vault/browser", { action: "status", tab });
        if (!cancelled && revision === mutation.current) setState({ ...emptyBrowser, ...value, tab });
      } catch (error) {
        if (!cancelled) setState({ ...emptyBrowser, message: error.message });
      } finally {
        running = false;
        if (!cancelled && ready) timer = setTimeout(poll, 2000);
      }
    };
    refresh.current = poll;
    void poll();
    return () => { cancelled = true; clearTimeout(timer); refresh.current = null; };
  }, [ready, tab]);
  const act = async input => {
    await api("/vault/browser", input);
    mutation.current++;
    if (input.candidate) setState(previous => ({ ...previous, candidates: previous.candidates.filter(value => value.id !== input.candidate) }));
    await refresh.current?.();
  };
  return { state, act };
}

export function BrowserVaultBar({ tab, vault, onManage, ready, menu = false }) {
  const [open, setOpen] = useState(false), [busy, setBusy] = useState(false), [message, setMessage] = useState("");
  const { state, act } = vault;
  const entries = state.tab === tab.id ? state.entries : [];
  const candidate = state.candidates.find(value => value.tab === tab.id);
  useEffect(() => { setOpen(false); setMessage(""); }, [tab.id, tab.url]);
  const run = async (input, success) => {
    setBusy(true); setMessage("");
    try { await act(input); setMessage(success); }
    catch (error) { setMessage(error.message); }
    finally { setBusy(false); }
  };
  return <div className="vault-browser" onKeyDown={event => {
    if (event.key === "Escape" && open) { event.stopPropagation(); setOpen(false); event.currentTarget.querySelector("button").focus(); }
  }}>
    <div className="vault-browser-heading">
      <button className={menu ? "browser-menu-item" : "text-button"} role={menu ? "menuitem" : undefined}
        aria-expanded={open} onClick={() => setOpen(!open)}>
        <KeyRound size={16} /><span>密码{entries.length ? ` · ${entries.length} 个账号` : ""}</span>
      </button>
      {message && <span role="status">{message}</span>}
    </div>
    {candidate && <div className="vault-save" role="region" aria-label="保存网站密码">
      <div><strong>{candidate.update ? "更新此账号的密码？" : "保存此账号密码？"}</strong>
        <span>{candidate.origin} · {candidate.username || "未填写用户名"}</span>
        <small>请确认网站登录成功后保存。</small></div>
      <div className="vault-actions">
        <button className="primary" disabled={busy} onClick={() => run({ action: "save", candidate: candidate.id }, "密码已保存")}>{candidate.update ? "更新密码" : "保存密码"}</button>
        <button disabled={busy} onClick={() => run({ action: "dismiss", candidate: candidate.id }, "")}>暂不保存</button>
        <button className="text-button" disabled={busy} onClick={() => run({ action: "never", candidate: candidate.id }, "此网站不再提示保存")}>此网站不再提示</button>
      </div>
    </div>}
    {open && <div className="vault-picker" role="region" aria-label="当前网站的密码">
      <p>{state.origin && state.tab === tab.id ? state.origin : "在 HTTPS 网站的登录页选择账号填充"}</p>
      {!ready ? <p>启动环境后可填写网站密码。</p> : !state.available ? <p>{state.message || "正在连接浏览器密码功能…"}</p> :
        entries.length ? entries.map(entry => <button className="vault-account-choice" key={entry.id} disabled={busy}
          onClick={() => run({ action: "fill", tab: tab.id, id: entry.id, document: state.document }, "已填写，请在网页中提交登录")}>
          <span>{entry.username || "未填写用户名"}</span><span>填入账号密码</span>
        </button>) : <p>此网站尚未保存账号密码。</p>}
      <button className="text-button" onClick={onManage}>管理密码保险箱</button>
    </div>}
  </div>;
}

const newEntry = () => ({ origin: "", username: "", password: "" });
export function PasswordVaultSettings({ onConfirm, onDirty }) {
  const [store, setStore] = useState({ entries: [], blocked: [] });
  const [loading, setLoading] = useState(true), [busy, setBusy] = useState(false);
  const [search, setSearch] = useState(""), [draft, setDraft] = useState(null);
  const [verify, setVerify] = useState(null), [accountPassword, setAccountPassword] = useState("");
  const [revealed, setRevealed] = useState(null), [error, setError] = useState(""), [message, setMessage] = useState("");
  const alive = useRef(true);
  const refresh = async () => {
    const data = await api("/vault");
    if (alive.current) setStore(data);
  };
  useEffect(() => {
    alive.current = true;
    refresh().catch(error => { if (alive.current) setError(error.message); }).finally(() => { if (alive.current) setLoading(false); });
    const clear = () => { setRevealed(null); setAccountPassword(""); };
    window.addEventListener("blur", clear);
    document.addEventListener("visibilitychange", clear);
    return () => { alive.current = false; window.removeEventListener("blur", clear); document.removeEventListener("visibilitychange", clear); };
  }, []);
  useEffect(() => { onDirty(Boolean(draft)); return () => onDirty(false); }, [draft, onDirty]);
  useEffect(() => {
    if (!revealed) return;
    const timer = setTimeout(() => setRevealed(null), 30000);
    return () => clearTimeout(timer);
  }, [revealed]);
  const run = async (input, success) => {
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await api("/vault", input);
      if (!alive.current) return;
      if (input.action === "reveal") {
        if (verify.mode === "copy") { await writeClipboard(result.password); setMessage("密码已复制到本机剪贴板"); }
        else setRevealed({ id: input.id, password: result.password });
        setVerify(null);
      } else { setDraft(null); setRevealed(null); await refresh(); setMessage(success); }
    } catch (error) { if (alive.current) setError(error.message); }
    finally { if (alive.current) { setBusy(false); setAccountPassword(""); } }
  };
  const askReveal = (entry, mode) => { setRevealed(null); setAccountPassword(""); setVerify({ entry, mode }); setError(""); };
  const entries = store.entries.filter(entry => `${entry.origin} ${entry.username}`.toLowerCase().includes(search.toLowerCase()));
  return <section className="vault-settings">
    <div className="section-title"><h3>密码保险箱</h3><p>账号密码加密保存在这台设备，随 Cage 一起解锁和锁定。</p></div>
    <div className="vault-note"><ShieldCheck size={17} /><span>查看或复制密码需要再次验证 Cage 账户密码。填充后由你提交登录。</span></div>
    {error && <p className="account-error" role="alert">{error}</p>}
    {message && <p role="status">{message}</p>}
    <div className="vault-toolbar"><label className="vault-search"><span><Search size={14} />搜索网站或账号</span>
      <input type="search" value={search} onChange={event => setSearch(event.target.value)} /></label>
      <button disabled={busy || Boolean(draft) || loading || Boolean(error && !store.entries.length)} onClick={() => { setDraft(newEntry()); setVerify(null); setRevealed(null); }}><Plus size={15} />添加密码</button></div>
    {draft && <form className="vault-editor" onSubmit={event => { event.preventDefault(); void run({ action: "save", entry: draft }, "密码记录已保存"); }}>
      <h4>{draft.id ? "编辑密码" : "添加密码"}</h4>
      <fieldset disabled={busy}>
        <label>网站地址<input type="url" required autoFocus placeholder="https://mail.example.com" value={draft.origin} onChange={event => setDraft({ ...draft, origin: event.target.value })} /></label>
        <label>账号或邮箱<input autoComplete="off" maxLength={1024} value={draft.username} onChange={event => setDraft({ ...draft, username: event.target.value })} /></label>
        <label>{draft.id ? "新密码（留空保留原密码）" : "网站密码"}<input type="password" autoComplete="new-password" required={!draft.id} maxLength={8192} value={draft.password || ""} onChange={event => setDraft({ ...draft, password: event.target.value })} /></label>
        <div className="vault-actions"><button className="primary" type="submit">保存记录</button><button type="button" onClick={() => setDraft(null)}>取消</button></div>
      </fieldset>
    </form>}
    {verify && <form className="vault-editor" onSubmit={event => { event.preventDefault(); void run({ action: "reveal", id: verify.entry.id, account_password: accountPassword }); }}>
      <h4>{verify.mode === "copy" ? "验证后复制密码" : "验证后查看密码"}</h4><p>{verify.entry.origin} · {verify.entry.username}</p>
      <label>Cage 账户密码<input type="password" autoComplete="off" autoFocus required maxLength={1024} value={accountPassword} onChange={event => setAccountPassword(event.target.value)} /></label>
      <div className="vault-actions"><button className="primary" disabled={busy} type="submit">验证并{verify.mode === "copy" ? "复制" : "查看"}</button>
        <button type="button" disabled={busy} onClick={() => { setVerify(null); setAccountPassword(""); }}>取消验证</button></div>
    </form>}
    {loading ? <p>正在读取密码保险箱…</p> : !entries.length ? <div className="vault-empty"><KeyRound size={24} /><p>{search ? "没有找到匹配的账号" : "还没有保存密码"}</p><small>网页登录后可以确认保存，也可以手动添加。</small></div> :
      <div className="vault-list">{entries.map(entry => <article className="vault-entry" key={entry.id}>
        <div><strong>{entry.origin}</strong><span>{entry.username || "未填写用户名"}</span></div>
        {revealed?.id === entry.id && <div className="vault-revealed"><output aria-label="已保存的网站密码">{revealed.password}</output><button onClick={() => setRevealed(null)}>隐藏密码</button></div>}
        <div className="vault-actions">
          <button disabled={busy || Boolean(draft)} onClick={() => askReveal(entry, "view")}>查看密码</button>
          <button disabled={busy || Boolean(draft)} onClick={() => askReveal(entry, "copy")}>复制密码</button>
          <button disabled={busy || Boolean(draft)} onClick={() => { setDraft({ ...entry, password: "" }); setVerify(null); setRevealed(null); }}>编辑</button>
          <button disabled={busy || Boolean(draft)} onClick={async () => { if (await onConfirm({ title: "删除此密码？", body: `${entry.origin} · ${entry.username || "未填写用户名"}。删除后无法恢复。`, confirm: "删除密码" })) await run({ action: "delete", id: entry.id }, "密码记录已删除"); }}>删除</button>
        </div>
      </article>)}</div>}
    {store.blocked.length > 0 && <section className="vault-blocked"><h4>不再提示保存的网站</h4>{store.blocked.map(origin => <div key={origin}><span>{origin}</span><button disabled={busy} onClick={() => run({ action: "unblock", origin }, "已恢复此网站的保存提示")}>恢复提示</button></div>)}</section>}
    <p className="footnote">目前支持 HTTPS 顶层网页的常见登录表单。密码不会同步到其他设备；备份时需同时保留本地账户文件与密码库。</p>
  </section>;
}
