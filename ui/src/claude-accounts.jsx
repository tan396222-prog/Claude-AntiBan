import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { Check, Pencil, Plus, RefreshCw, TerminalSquare, Timer } from "lucide-react";
import { api } from "./api";
import crabIcon from "./claude-account-crab.svg";
import "./claude-accounts.css";

// Ordinary reads never start an upstream usage query.
export function useClaudeAccounts() {
  const [registry, setRegistry] = useState(null);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const pendingMutation = useRef(0);
  const polling = useRef(false);
  const load = useCallback(async (change, visiblePanel = false) => {
    if (!change && (pendingMutation.current || (visiblePanel && polling.current))) return;
    const request = ++generation.current;
    if (change) pendingMutation.current++;
    else if (visiblePanel) polling.current = true;
    try {
      const value = await api(visiblePanel ? "/claude-accounts?panel=visible" : "/claude-accounts", change);
      if (!Array.isArray(value?.accounts)) throw new Error("无法读取 Claude 账号列表");
      if (generation.current === request) {
        setRegistry(value);
        setError("");
      }
      return value.accounts;
    } catch (e) {
      if (generation.current === request) setError(e.message);
      if (change) throw e;
    } finally {
      if (change) pendingMutation.current--;
      else if (visiblePanel) polling.current = false;
    }
  }, []);
  useEffect(() => { load(); return () => { generation.current++; }; }, [load]);
  return { accounts: registry?.accounts ?? null, detected: registry?.detected,
    detectionStatus: registry?.detection_status, detectionError: registry?.detection_error,
    usageRunning: registry?.usage_panel_running, usageUnavailable: registry?.usage_unavailable_reason,
    loginNotice: registry?.login_notice, error, load };
}

const statusLabels = {
  pending: "等待登录", ready: "已登录", signed_out: "需要登录",
  duplicate: "账号已在列表中", changed: "登录身份已改变",
};
const localTimestamp = new Intl.DateTimeFormat(undefined, {
  month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit",
});
const timeLabel = (seconds) => localTimestamp.format(seconds * 1000);

const QuotaTime = createContext(0);

// All rows share a clock. Only quota readings subscribe; a tick doesn't render
// the account forms or fetch data. Sleep until the next visible unit changes.
function QuotaClock({ accounts, children }) {
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    let timer;
    const update = () => {
      clearTimeout(timer);
      if (document.hidden) return;
      const millis = Date.now();
      const seconds = Math.floor(millis / 1000);
      setNow(seconds);
      let delay = Infinity;
      for (const account of accounts || []) {
        for (const window of [account.quota?.five_hour, account.quota?.seven_day]) {
          const left = window?.resets_at - seconds;
          if (left > 0) delay = Math.min(delay, left <= 60 ? 1 : (left - 1) % 60 + 1);
          const seen = window?.observed_at;
          if (window && seen && seen + 901 > seconds) delay = Math.min(delay, seen + 901 - seconds);
        }
      }
      if (Number.isFinite(delay)) timer = setTimeout(update, delay * 1000 - millis % 1000);
    };
    update();
    document.addEventListener("visibilitychange", update);
    return () => { clearTimeout(timer); document.removeEventListener("visibilitychange", update); };
  }, [accounts]);
  return <QuotaTime.Provider value={now}>{children}</QuotaTime.Provider>;
}

function resetCountdown(seconds) {
  if (seconds < 60) return `${seconds}秒后`;
  const minutes = Math.ceil(seconds / 60);
  if (minutes < 60) return `${minutes}分后`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}时 ${minutes % 60}分后`;
  return `${Math.floor(hours / 24)}天 ${hours % 24}时后`;
}

const usageLabels = {
  checking: "正在查询额度…", reported: "已获取额度", partial: "本次只返回了部分额度",
  cached: "本次只有缓存读数", unavailable: "本次未返回额度", error: "额度查询失败",
  not_applicable: "当前登录不提供订阅额度",
  identity_changed: "登录身份已改变，本次读数已丢弃", offline: "环境离线，无法查询",
  update_required: "需同步环境组件后才能查询", blocked: "暂时无法查询额度",
  signed_out: "登录后可查询额度", local: "已有会话读数", unknown: "尚未查询额度",
};

function useUsageCheck(profile, environment, perform, change) {
  const [manual, setManual] = useState(null);
  const check = () => perform(async () => {
    setManual({ state: "checking" });
    try {
      await change(profile.id ? { action: "refresh", id: profile.id } :
        { action: "refresh_default", identity: profile.identity });
      setManual(null);
    } catch (e) {
      setManual({ state: "error", previousCheck: profile.usage_checked_at, previousStatus: profile.usage_status });
      await change(); // A failed identity check can still change which default account the panel should show.
      throw e;
    }
  });
  let state = profile.usage_status || (profile.quota ? "local" : "unknown");
  if (profile.status === "changed" || profile.status === "duplicate") state = "identity_changed";
  if (profile.status === "signed_out" || profile.status === "pending") state = "signed_out";
  if (manual?.state === "error" && manual.previousCheck === profile.usage_checked_at && manual.previousStatus === profile.usage_status) state = "error";
  if (profile.usage_checking || manual?.state === "checking") state = "checking";
  if (environment) state = environment;
  return { state, check };
}

function QuotaWindow({ label, snapshot, state }) {
  const now = useContext(QuotaTime);
  const reset = snapshot?.resets_at;
  const remaining = reset ? reset - now : null;
  const expired = remaining !== null && remaining <= 0;
  const hasReading = Number.isFinite(snapshot?.used_percentage);
  const known = !expired && hasReading;
  const lastSeen = snapshot?.observed_at;
  const stale = known && (!lastSeen || now - lastSeen > 15 * 60 ||
    !["reported", "partial"].includes(state));
  const countdown = expired ? "等待更新" : remaining !== null ? resetCountdown(remaining) :
    known ? "重置时间未知" : state === "checking" ? "查询中" : "暂无读数";
  return <div className="claude-quota-row" role="group" aria-label={label + " 额度"}>
    <span className="claude-quota-period">{label}</span>
    {known
      ? <progress aria-label={label + " 已用额度"} value={snapshot.used_percentage} max={100} />
      : <span className="claude-quota-unknown" aria-hidden="true" />}
    <div className="claude-quota-reading">
      {stale && <span className="claude-quota-stale" title={lastSeen ? `历史读数 · ${timeLabel(lastSeen)}` : "历史读数"}>历史读数</span>}
      {known && <strong title={lastSeen ? `已用额度 · 读取于 ${timeLabel(lastSeen)}` : "已用额度"}>{snapshot.used_percentage}%</strong>}
      <span className="claude-quota-reset" title={reset ? `${timeLabel(reset)} 重置` : "尚无重置时间"}>
        {remaining > 0 && <Timer size={12} aria-hidden="true" />}{countdown}
      </span>
    </div>
    {hasReading && (expired || stale || state === "partial") && <small className="claude-quota-history">
      {expired && `上次 ${snapshot.used_percentage}% · `}
      {lastSeen > 0 ? <>{!expired && "读取于 "}<time dateTime={new Date(lastSeen * 1000).toISOString()}>{timeLabel(lastSeen)}</time></> : "读数时间未知"}
    </small>}
  </div>;
}

function ProfileUsage({ quota, state }) {
  return <div className="claude-quota-grid">
    <QuotaWindow label="5h" snapshot={quota?.five_hour} state={state} />
    <QuotaWindow label="7d" snapshot={quota?.seven_day} state={state} />
  </div>;
}

function UsageNote({ profile, state }) {
  const latestReading = Math.max(profile.quota?.five_hour?.observed_at || 0, profile.quota?.seven_day?.observed_at || 0);
  return <small className="claude-quota-age">
    <span>{usageLabels[state] || "暂无额度信息"}</span>
    {profile.usage_checked_at > 0 && <span>上次查询 {timeLabel(profile.usage_checked_at)}</span>}
    {latestReading > 0 && <span>最近读数 {timeLabel(latestReading)}</span>}
  </small>;
}

function DetectedProfile({ profile, usageEnvironment, busy, join, perform, change }) {
  const usage = useUsageCheck(profile, usageEnvironment, perform, change);
  return <article className="claude-profile claude-detected" aria-label={profile.email}>
    <div className="claude-profile-heading">
      <span className="claude-profile-avatar" aria-hidden="true"><img src={crabIcon} alt="" /></span>
      <div className="claude-profile-identity">
        <div className="claude-profile-name">
          <h4 title={profile.email}>{profile.email}</h4>
          {profile.plan && <span className="claude-profile-plan">{profile.plan}</span>}
        </div>
        <div className="claude-profile-meta">
          <span className="claude-profile-status">普通终端 · 已发现，未加入</span>
          {profile.organization && <span className="claude-profile-email" title={profile.organization}>{profile.organization}</span>}
        </div>
      </div>
    </div>
    <ProfileUsage quota={profile.quota} state={usage.state} />
    <div className="claude-profile-actions">
      <button className="quiet-button" disabled={busy} onClick={join}><Plus size={14} />加入调度</button>
      <button className="quiet-button" disabled={busy || !!usageEnvironment || usage.state === "checking"} onClick={usage.check}>
        <RefreshCw size={14} />检查状态</button>
      <UsageNote profile={profile} state={usage.state} />
    </div>
  </article>;
}

function ProfileCard({ profile, environment, usageEnvironment, busy, active, defaultActive, perform, change, login, cancelDraft }) {
  const [renaming, setRenaming] = useState(false);
  const [label, setLabel] = useState("");
  const displayName = profile.label || profile.email;
  const fromTerminal = profile.source === "default";
  const savedLabel = label.trim() || profile.email;
  const usage = useUsageCheck(profile, usageEnvironment, perform, change);
  return <article className="claude-profile" aria-label={displayName} data-enabled={profile.enabled}>
    <div className="claude-profile-heading">
      <span className="claude-profile-avatar" aria-hidden="true"><img src={crabIcon} alt="" /></span>
      <div className="claude-profile-identity">
        {renaming ? <form className="claude-profile-rename" onSubmit={(e) => {
          e.preventDefault();
          perform(async () => { await change({ action: "update", id: profile.id, label: savedLabel }); setRenaming(false); });
        }}>
          <input autoFocus aria-label="账号备注" maxLength={64} placeholder={profile.email || "账号名称"} value={label} onChange={(e) => setLabel(e.target.value)} />
          <button disabled={busy || !savedLabel} aria-label="保存备注"><Check size={15} /></button>
          <button type="button" disabled={busy} onClick={() => setRenaming(false)}>取消</button>
        </form> : <div className="claude-profile-name">
          <h4 title={displayName}>{displayName}</h4>
          {profile.plan && <span className="claude-profile-plan">{profile.plan}</span>}
          <button type="button" className="icon-button" disabled={busy} aria-label={`修改 ${displayName} 的备注`}
            onClick={() => { setLabel(profile.label === profile.email ? "" : profile.label || ""); setRenaming(true); }}><Pencil size={14} /></button>
        </div>}
        <div className="claude-profile-meta">
          <span className="claude-profile-status" data-ready={profile.status === "ready"}>{statusLabels[profile.status] || "状态未知"}</span>
          {profile.email && <span className="claude-profile-email" title={[profile.email, profile.organization].filter(Boolean).join(" · ")}>
            {[profile.email !== displayName && profile.email, profile.organization].filter(Boolean).join(" · ")}
          </span>}
        </div>
      </div>
      <label className="check-line claude-participation">
        <input type="checkbox" aria-label="参与自动分配" checked={profile.enabled} disabled={busy}
          onChange={(e) => { const enabled = e.target.checked; perform(() => change({ action: "update", id: profile.id, enabled })); }} />
        <span className="claude-switch-track" aria-hidden="true" />
        <span>{profile.enabled ? "参与分配" : "已暂停"}</span>
      </label>
    </div>
    <ProfileUsage quota={profile.quota} state={usage.state} />
    {active > 0 && <p className="claude-profile-note">{active} 个终端仍在运行{!fromTerminal && "，关闭后可重新登录"}</p>}
    {!fromTerminal && defaultActive > 0 && <p className="field-help">
      其中 {defaultActive} 个工作终端仍使用普通终端的登录。若普通终端已换号，请重新打开这些会话确认身份。
    </p>}
    {fromTerminal && ["changed", "signed_out"].includes(profile.status) && <p className="field-help">
      {profile.status === "changed" ? "普通终端已换号，此账号暂不参与分配。新身份需单独加入。" : "普通终端已退出。要继续使用此账号，请先重新登录原身份。"}
      {active > 0 && " 原工作终端仍保留首次分配的标签。请重新打开工作会话确认身份。"}
    </p>}
    {!fromTerminal && profile.status === "changed" && <p className="field-help">请用原账号重新登录。若要使用新的身份，请点击「登录新账号」。</p>}
    {profile.status === "duplicate" && <p className="field-help">同一订阅只参与一次分配。请在此位置登录另一个账号。</p>}
    <div className="claude-profile-actions">
      {fromTerminal ? <span className="claude-profile-source">普通终端账号</span> :
        <button className="quiet-button" disabled={busy || !!environment || active > 0} onClick={() => perform(() => login(profile.id))}>
          <TerminalSquare size={14} />{profile.status === "ready" ? "重新登录" : "打开登录终端"}</button>}
      <button className="quiet-button" disabled={busy || !!usageEnvironment || usage.state === "checking"} onClick={usage.check}>
        <RefreshCw size={14} />检查状态</button>
      {!fromTerminal && !profile.email && !active && ["pending", "signed_out", "duplicate"].includes(profile.status) &&
        <button className="text-button" disabled={busy} onClick={() => perform(() => cancelDraft(profile))}>撤销添加</button>}
      <UsageNote profile={profile} state={usage.state} />
    </div>
  </article>;
}

export function ClaudeAccountsPanel({ store, running, environmentBusy, tabs, login, onConfirm }) {
  const { accounts, detected, detectionStatus, detectionError, loginNotice, usageRunning, usageUnavailable, load, error } = store;
  const [busy, setBusy] = useState(false);
  const [requestingUsage, setRequestingUsage] = useState(false);
  const [actionError, setActionError] = useState("");
  const visibleProfiles = useMemo(() => detected ? [detected, ...(accounts || [])] : accounts, [accounts, detected]);
  const environment = !running || detectionStatus === "offline" ? "offline" :
    detectionStatus === "update_required" ? "update_required" : "";
  const usageEnvironment = environment || (usageUnavailable ? "blocked" : "");
  const checking = !usageEnvironment && (usageRunning || visibleProfiles?.some((profile) => profile.usage_checking));
  const activeCounts = useMemo(() => {
    const counts = new Map();
    for (const tab of tabs) {
      if (!tab.claude_account_id || tab.closed) continue;
      const count = counts.get(tab.claude_account_id) || { all: 0, fromDefault: 0 };
      count.all++;
      if (tab.claude_account_source === "default") count.fromDefault++;
      counts.set(tab.claude_account_id, count);
    }
    return counts;
  }, [tabs]);
  useEffect(() => {
    let requested = false, mounted = true;
    const openPanel = () => {
      if (requested || document.hidden) return;
      requested = true;
      setRequestingUsage(running);
      if (running) {
        load({ action: "usage_panel" })
          .catch(() => {})
          .finally(() => { if (mounted) setRequestingUsage(false); });
      } else load();
    };
    openPanel();
    document.addEventListener("visibilitychange", openPanel);
    return () => { mounted = false; document.removeEventListener("visibilitychange", openPanel); };
  }, [load, running]);
  useEffect(() => {
    const timer = setInterval(() => { if (!document.hidden) load(undefined, true); }, checking ? 2000 : 12000);
    return () => clearInterval(timer);
  }, [load, checking]);
  const perform = async (operation) => {
    if (busy) return;
    setBusy(true);
    setActionError("");
    try { await operation(); }
    catch (e) { setActionError(e.message); }
    finally { setBusy(false); }
  };
  const cancelDraft = async (profile) => {
    const last = accounts.length === 1;
    if (await onConfirm({ title: `撤销添加「${profile.label}」？`,
      body: last
        ? "这是列表中的最后一个账号。撤销后，新建 Claude 终端会沿用环境原有的登录。虚拟机中的资料会保留。"
        : "此账号会从列表中移除，虚拟机中的资料会保留。",
      confirm: "撤销添加" })) {
      await load({ action: "cancel", id: profile.id });
    }
  };
  const joinDetected = async () => {
    try { await load({ action: "join", identity: detected.identity }); }
    catch (e) {
      await load(); // The identity shown when the user clicked may no longer be signed in.
      throw e;
    }
  };
  return <section className="claude-profiles" aria-label="Claude 账号管理">
    <div className="section-title">
      <h3>Claude 账号</h3>
      <p>将普通终端已登录的订阅加入调度，或单独登录一个新账号。新建 Claude 终端时自动分配。</p>
    </div>
    <div className="claude-add-profile">
      <button className="primary" disabled={busy || environmentBusy || !!environment}
        onClick={() => perform(() => login())}><Plus size={16} />登录新账号</button>
    </div>
    {(actionError || error) && <p className="account-error" role="alert">{actionError || error}</p>}
    {loginNotice && <p className="claude-profile-note" role="status">{loginNotice}</p>}
    {detectionError && <p className="account-error" role="alert">{detectionError}</p>}
    {usageUnavailable && !environment && <p className="claude-profile-note" role="status">{usageUnavailable}</p>}
    {!running ? <p className="claude-profile-note">启动 Cage 环境后可检测普通终端的登录，也可登录新账号。</p> :
      detectionStatus === "offline" ? <p className="claude-profile-note">工作环境就绪后可检测和登录账号。</p> :
      detectionStatus === "signed_out" && <p className="claude-profile-note">普通终端尚未登录 Claude 订阅。可在其中运行 <code>claude auth login</code>，登录后会在这里出现。</p>}
    <div className="claude-list-heading">
      <h4>账号列表 <span>{visibleProfiles?.length ?? "—"}</span></h4>
      {requestingUsage || checking ? <small role="status">{requestingUsage ? "正在请求账号用量…" : "正在查询账号用量…"}</small> :
        accounts?.length > 0 && <small>{accounts.filter((a) => a.enabled).length} 个启用调度</small>}
    </div>
    <div className="claude-profile-list" role="region" aria-label="账号列表" tabIndex={0}>
      {accounts === null && !error && <p role="status">正在读取账号…</p>}
      {accounts?.length === 0 && !detected && <div className="claude-profile-empty">
        <img src={crabIcon} alt="" /><p>尚无已加入调度的账号。</p><small>加入第一个账号前，新终端沿用环境原有的登录。</small>
      </div>}
      <QuotaClock accounts={visibleProfiles}>
        {detected && <DetectedProfile key={detected.identity} profile={detected} usageEnvironment={usageEnvironment} busy={busy || environmentBusy || !!environment}
          perform={perform} change={load}
          join={() => perform(joinDetected)} />}
        {accounts?.map((profile) => <ProfileCard key={profile.id} profile={profile} environment={environment} usageEnvironment={usageEnvironment}
          busy={busy || environmentBusy} perform={perform} change={load} login={login} cancelDraft={cancelDraft}
          active={activeCounts.get(profile.id)?.all || 0} defaultActive={activeCounts.get(profile.id)?.fromDefault || 0} />)}
      </QuotaClock>
    </div>
  </section>;
}
