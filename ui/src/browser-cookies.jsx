import React, { useEffect, useRef, useState } from "react";
import { ArrowLeft, RefreshCw, Trash2 } from "lucide-react";
import { api } from "./api";
import { InlineSelect } from "./inline-select";

export function BrowserCookies({ tab, onClose }) {
  const [scope, setScope] = useState(/^https?:/.test(tab.url) ? "site" : "all");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const [revision, setRevision] = useState(0);
  const [data, setData] = useState(null);
  const [value, setValue] = useState(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const generation = useRef(0), search = useRef(null), operation = useRef(false);
  useEffect(() => { search.current?.focus(); }, []);
  useEffect(() => {
    const version = ++generation.current;
    setLoading(true); setData(null); setValue(null); setError(""); setConfirmClear(false);
    api("/browser/cookies", { id: tab.id, url: tab.url, mode: "list", scope, query, page })
      .then(result => { if (version === generation.current) setData(result); })
      .catch(error => { if (version === generation.current) setError(error.message); })
      .finally(() => { if (version === generation.current) setLoading(false); });
    return () => { generation.current++; };
  }, [tab.id, scope, query, page, revision]);
  const run = async (mode, cookieKey) => {
    if (operation.current || loading) return;
    operation.current = true; setBusy(true); setError("");
    const version = generation.current;
    try {
      const result = await api("/browser/cookies", { id: tab.id, url: tab.url, mode, scope, query, page, cookieKey });
      if (version !== generation.current) return;
      if (mode === "details") setValue({ key: cookieKey, value: result.value });
      else { setData(result); setValue(null); setConfirmClear(false); }
    } catch (error) {
      if (version === generation.current) setError(error.message);
    } finally { operation.current = false; setBusy(false); }
  };
  return <section className="browser-cookies" aria-label="Cookie 管理" onKeyDown={event => {
    if (event.key === "Escape") { event.stopPropagation(); onClose(); }
  }}>
    <div className="cookie-heading">
      <button className="icon-button" aria-label="关闭 Cookie 管理" onClick={onClose}><ArrowLeft size={15} /></button>
      <strong>Cookie 管理</strong><span>{tab.incognito ? "无痕会话" : "普通会话"}</span>
      <button className="icon-button" aria-label="刷新 Cookie" disabled={busy || loading}
        onClick={() => setRevision(revision + 1)}><RefreshCw size={15} /></button>
    </div>
    <div className="cookie-toolbar">
      <InlineSelect aria-label="Cookie 网站范围" value={scope} disabled={busy} onValueChange={next => { setScope(next); setPage(0); }}>
        <option value="site" disabled={!/^https?:/.test(tab.url)}>当前网站</option><option value="all">所有网站</option>
      </InlineSelect>
      <input ref={search} aria-label="搜索 Cookie" placeholder="搜索网站或 Cookie 名称" maxLength={200}
        value={query} disabled={busy} onChange={event => { setQuery(event.target.value); setPage(0); }} />
      <button disabled={busy || loading || !data} onClick={() => setConfirmClear(!confirmClear)}>
        <Trash2 size={14} />清空{scope === "site" ? "当前网站" : "所有网站"}
      </button>
    </div>
    {confirmClear && <div className="cookie-confirm" role="alert">
      <span>清空{scope === "site" ? data?.hostname : "此" + (tab.incognito ? "无痕" : "普通") + "会话所有网站"}的 Cookie？搜索条件不限制清空范围，网站可能需要重新登录。</span>
      <button disabled={busy} onClick={() => run("clear")}>确认清空</button>
      <button disabled={busy} onClick={() => setConfirmClear(false)}>取消</button>
    </div>}
    {error && <p className="cookie-error" role="alert">{error}</p>}
    <div className="cookie-list" aria-busy={busy || loading}>
      {loading ? <p>正在读取 Cookie…</p> : data?.cookies.length === 0 ? <p>没有匹配的 Cookie。</p> : data?.cookies.map(cookie =>
        <article className="cookie-record" key={cookie.key}>
          <div className="cookie-record-heading"><strong>{cookie.name || "（空名称）"}</strong>
            <button disabled={busy} onClick={() => value?.key === cookie.key ? setValue(null) : run("details", cookie.key)}>
              {value?.key === cookie.key ? "隐藏值" : "查看值"}</button>
            <button disabled={busy || cookie.partitionKeyOpaque} aria-label={`删除 Cookie ${cookie.name}`}
              title={cookie.partitionKeyOpaque ? "此分区 Cookie 需要清空所有网站" : "删除此 Cookie"}
              onClick={() => run("delete", cookie.key)}><Trash2 size={14} /></button></div>
          <div className="cookie-metadata">{cookie.domain} · 路径 {cookie.path} · {cookie.size} B</div>
          <div className="cookie-metadata">{cookie.session ? "会话结束时过期" : `过期：${new Date(cookie.expires * 1000).toLocaleString()}`}
            {cookie.secure && " · Secure"}{cookie.httpOnly && " · HttpOnly"}{cookie.sameSite && ` · SameSite=${cookie.sameSite}`}
            {(cookie.partitionKey || cookie.partitionKeyOpaque) && ` · 分区：${cookie.partitionKey?.topLevelSite || "不透明"}`}</div>
          {value?.key === cookie.key && <pre className="cookie-value">{value.value || "（空值）"}</pre>}
        </article>)}
    </div>
    {data && <div className="cookie-pagination"><span>共 {data.total} 条 · 第 {data.page + 1} 页</span>
      <button disabled={busy || loading || data.page === 0} onClick={() => setPage(data.page - 1)}>上一页</button>
      <button disabled={busy || loading || (data.page + 1) * 50 >= data.total} onClick={() => setPage(data.page + 1)}>下一页</button>
    </div>}
  </section>;
}
