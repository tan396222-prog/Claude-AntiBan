import React, { useEffect, useRef, useState } from 'react';
import { Download, ExternalLink, FolderInput, Puzzle, RefreshCw, Trash2, Upload, X } from 'lucide-react';
import { api, uploadFile } from './api';
import { assertConnection, getClientContext } from './client-context.mjs';
import { InlineSelect } from './inline-select';

function Permissions({ item, expanded = false }) {
  const permissions = [...(item.permissions || []), ...(item.hostPermissions || [])];
  return <details className="extension-permissions" open={expanded || undefined}><summary>权限与网站访问{permissions.length ? `（${permissions.length}）` : ''}</summary>
    {permissions.length ? <ul>{permissions.map((value, index) => <li key={index}>{value}</li>)}</ul> : <p>未声明额外权限。</p>}</details>;
}

function LocalExtensionImport({ existing, onUpdated, onClose }) {
  const [profiles, setProfiles] = useState(null), [profileID, setProfileID] = useState('');
  const [selected, setSelected] = useState(new Set()), [results, setResults] = useState({});
  const [busy, setBusy] = useState(false), [error, setError] = useState('');
  const alive = useRef(true);
  useEffect(() => {
    api('/client/browser-extensions').then(data => { if (alive.current) { setProfiles(data.profiles); setProfileID(data.profiles[0]?.id || ''); } })
      .catch(error => { if (alive.current) setError(error.message); });
    return () => { alive.current = false; };
  }, []);
  const profile = profiles?.find(item => item.id === profileID);
  const installed = new Set(existing.map(item => item.source));
  const completed = Object.values(results).some(value => value.startsWith('已导入'));
  const eligible = item => item.compatible && !installed.has(`${profileID}:${item.id}`);
  const importCount = profile?.extensions.filter(item => selected.has(item.id) && eligible(item)).length || 0;
  const choose = value => { setProfileID(value); setSelected(new Set()); setResults({}); setError(''); };
  const importSelected = async () => {
    if (busy || !profile) return;
    const connection = getClientContext().connection_id;
    setBusy(true); setError('');
    for (const item of profile.extensions.filter(item => selected.has(item.id) && eligible(item))) {
      let pending;
      try {
        assertConnection(connection);
        if (!alive.current) break;
        setResults(previous => ({ ...previous, [item.id]: '正在导入…' }));
        const response = await fetch('/api/client/browser-extensions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ profile: profileID, id: item.id, version: item.version }) });
        if (!response.ok) throw new Error((await response.text()).trim());
        const file = new File([await response.blob()], item.id + '.zip', { type: 'application/zip' });
        assertConnection(connection);
        if (!alive.current) break;
        pending = await uploadFile('/browser/extensions/upload', file);
        assertConnection(connection);
        if (!alive.current) throw new Error('导入已停止');
        const next = await api('/browser/extensions', { mode: 'install', session: pending.session, target: `${profileID}:${item.id}`, enabled: item.state_known && item.enabled });
        pending = null;
        if (alive.current) { onUpdated(next.items); setResults(previous => ({ ...previous, [item.id]: item.state_known && item.enabled ? '已导入并启用' : '已导入，保持停用' })); }
      } catch (error) {
        if (pending && getClientContext().connection_id === connection) await api('/browser/extensions', { mode: 'cancel', session: pending.session }).catch(() => {});
        if (alive.current) setResults(previous => ({ ...previous, [item.id]: '导入失败：' + error.message }));
        if (getClientContext().connection_id !== connection) break;
      }
    }
    if (alive.current) setBusy(false);
  };
  return <section className="extension-import" aria-label="从本机导入扩展">
    <div className="browser-setting-heading"><h3>从本机浏览器导入</h3><button className="icon-button" aria-label="关闭本机导入" disabled={busy} onClick={onClose}><X size={16} /></button></div>
    <p>一次复制所选扩展及启停状态。浏览器账户、Cookie 和扩展登录数据保留在本机浏览器中。</p>
    {error && <p className="browser-settings-error" role="alert">{error}</p>}
    {!profiles && !error && <p role="status">正在查找本机浏览器配置…</p>}
    {profiles?.length === 0 && <p>未找到 Edge、Chrome、Brave 或 Chromium 的扩展配置。</p>}
    {Boolean(profiles?.length) && <>
      <label className="browser-setting-field">本机浏览器配置<InlineSelect aria-label="本机浏览器配置" value={profileID} disabled={busy} onValueChange={choose}>
        {profiles.map(item => <option value={item.id} key={item.id}>{item.browser} · {item.name}</option>)}
      </InlineSelect></label>
      <div className="extension-import-actions"><button disabled={busy} onClick={() => setSelected(new Set(profile?.extensions.filter(eligible).map(item => item.id)))}>选择全部兼容扩展</button>
        {completed && !busy && !importCount ? <button className="primary" onClick={onClose}>完成并关闭</button> : <button className="primary" disabled={busy || !importCount} onClick={importSelected}><Download size={15} />{busy ? '正在导入…' : `导入所选扩展（${importCount}）`}</button>}</div>
      {!profile?.extensions.length && <p>此配置没有可读取的扩展。</p>}
      {profile?.extensions.map(item => <article className="extension-import-row" key={item.id}>
        <label><input type="checkbox" disabled={busy || !eligible(item)} checked={selected.has(item.id)} onChange={event => setSelected(previous => { const next = new Set(previous); if (event.target.checked) next.add(item.id); else next.delete(item.id); return next; })} />
          <strong>{item.name}</strong><span>{item.version}</span></label>
        <p className="extension-import-state" role={results[item.id] ? 'status' : undefined}>{results[item.id] || (!item.compatible ? '不支持此扩展版本，仅支持 Manifest V3' : installed.has(`${profileID}:${item.id}`) ? '已导入' : !item.state_known ? '未能读取启停状态，导入后停用' : item.enabled ? '本机已启用' : '本机已停用')}</p>
        {item.compatible && !item.identity_preserved && <p>此扩展的标识可能变化，依赖浏览器身份或授权的功能可能需要重新配置。</p>}
        <Permissions item={item} />
      </article>)}
    </>}
  </section>;
}

export function BrowserExtensions({ onOpenPage, onInternal, onConfirm }) {
  const [items, setItems] = useState(null), [error, setError] = useState(''), [busy, setBusy] = useState('');
  const [preview, setPreview] = useState(null), [importing, setImporting] = useState(false);
  const file = useRef(), alive = useRef(true), pending = useRef(null);
  const connection = useRef(getClientContext().connection_id);
  const load = async () => {
    setError('');
    try { const result = await api('/browser/extensions', { mode: 'list' }); if (alive.current) setItems(result.items); }
    catch (error) { if (alive.current) setError(error.message); }
  };
  useEffect(() => {
    void load();
    return () => { alive.current = false; if (pending.current && getClientContext().connection_id === connection.current) void api('/browser/extensions', { mode: 'cancel', session: pending.current }).catch(() => {}); };
  }, []);
  const select = async event => {
    const selected = event.target.files?.[0]; event.target.value = '';
    if (!selected) return;
    if (selected.size > 32 << 20) { setError('扩展 ZIP 不能超过 32 MB'); return; }
    setBusy('upload'); setError('');
    try {
      const result = await uploadFile('/browser/extensions/upload', selected);
      if (!alive.current) { if (getClientContext().connection_id === connection.current) await api('/browser/extensions', { mode: 'cancel', session: result.session }); return; }
      pending.current = result.session; setPreview(result);
    } catch (error) { if (alive.current) setError(error.message); }
    finally { if (alive.current) setBusy(''); }
  };
  const act = async (mode, id) => {
    if (busy) return;
    if (mode === 'remove' && !(await onConfirm({ title: '移除此扩展？', body: '扩展将从 Cage 浏览器中移除。本机 Edge 或 Chrome 中的扩展保持原状。', confirm: '移除扩展' }))) return;
    setBusy(id || mode); setError('');
    try {
      const result = await api('/browser/extensions', { mode, id, ...(mode === 'install' ? { session: preview.session } : {}) });
      if (mode === 'install' || mode === 'cancel') { pending.current = null; setPreview(null); }
      if (result.native) await onInternal('extensions', result.id);
      else if (result.id && result.url) await onOpenPage(result);
      else if (result.items) setItems(result.items);
    } catch (error) { setError(error.message); }
    finally { setBusy(''); }
  };
  const cancel = async () => {
    setBusy('cancel'); setError('');
    try { await api('/browser/extensions', { mode: 'cancel', session: preview.session }); pending.current = null; setPreview(null); }
    catch (error) { setError(error.message); }
    finally { setBusy(''); }
  };
  return <div className="browser-extensions">
    <div className="browser-setting-actions">
      <button className="primary" disabled={Boolean(busy || preview)} onClick={() => file.current?.click()}><Upload size={15} />添加 ZIP 扩展</button>
      <button disabled={Boolean(busy || preview)} onClick={() => setImporting(true)}><FolderInput size={15} />从本机导入</button>
      <button aria-label="刷新扩展" disabled={Boolean(busy)} onClick={load}><RefreshCw size={15} /></button>
    </div>
    <input ref={file} type="file" accept=".zip,application/zip" hidden onChange={select} aria-label="选择扩展 ZIP" />
    <p>支持 Chromium Manifest V3 扩展。扩展运行在 Cage 环境中，并按声明的权限访问网站。</p>
    {error && <p className="browser-settings-error" role="alert">{error}</p>}
    {busy === 'upload' && <p role="status">正在读取扩展包…</p>}
    {preview && <section className="extension-install-preview" aria-label="确认扩展安装">
      <h3>确认安装 {preview.name}</h3><p>版本 {preview.version}</p><p>{preview.description}</p>
      <Permissions item={preview} expanded />
      <div className="browser-setting-actions"><button className="primary" disabled={Boolean(busy)} onClick={() => act('install')}>安装扩展</button><button disabled={Boolean(busy)} onClick={cancel}>取消</button></div>
    </section>}
    {importing && <LocalExtensionImport existing={items || []} onUpdated={setItems} onClose={() => setImporting(false)} />}
    <div className="browser-setting-heading"><h3>已安装的扩展{items ? `（${items.length}）` : ''}</h3></div>
    {items === null && !error ? <p role="status">正在读取扩展…</p> : items?.length === 0 ? <div className="extension-empty"><Puzzle size={28} /><strong>让浏览器更合用</strong><p>添加扩展，或从本机浏览器导入已有扩展。</p></div> : items?.map(item => <article className="extension-item" key={item.id}>
      <div className="browser-setting-heading"><div><strong>{item.name}</strong><span className="extension-version">{item.version}</span></div>
        <label className="extension-enable"><input type="checkbox" aria-label={`启用 ${item.name}`} checked={Boolean(item.enabled)} disabled={Boolean(busy) || item.mayDisable === false} onChange={() => act(item.enabled ? 'disable' : 'enable', item.id)} />{item.enabled ? '已启用' : '已停用'}</label></div>
      {item.description && <p>{item.description}</p>}{item.error && <p role="alert" className="browser-settings-error">{item.error}</p>}
      <Permissions item={item} />
      <div className="extension-item-footer"><span>{item.id}</span><div className="browser-setting-actions">
        {item.optionsUrl && <button disabled={Boolean(busy) || !item.enabled} onClick={() => act('options', item.id)}>扩展选项</button>}
        <button disabled={Boolean(busy) || item.mayDisable === false} onClick={() => act('remove', item.id)}><Trash2 size={14} />{item.managed ? '移除' : '管理'}</button>
      </div></div>
    </article>)}
    <div className="browser-settings-links"><button onClick={() => onInternal('extensions')}><ExternalLink size={15} />Chromium 扩展管理</button>
      <button onClick={() => onOpenPage({ url: 'https://chromewebstore.google.com/', kind: 'web' })}><ExternalLink size={15} />打开扩展商店</button></div>
  </div>;
}
