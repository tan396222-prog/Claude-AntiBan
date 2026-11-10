import React, { useEffect, useRef, useState } from "react";
import { api, native, nativeAvailable } from "./api";
import { useAccount } from "./account";
import { MacOSLogin } from "./macos-login";
import { DiagnosticSettings } from "./diagnostics.jsx";

const steps = [["screen", "窗口录制"], ["input", "辅助功能"], ["projects", "文件系统扩展"]];

export function MacOSFirstSetup() {
  const { account, refresh } = useAccount();
  const [permissionPage, setPermissionPage] = useState("screen");
  const [state, setState] = useState(null);
  const [permissions, setPermissions] = useState({});
  const [error, setError] = useState("");
  const [completionFailed, setCompletionFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [diagnostics, setDiagnostics] = useState(false);
  const started = useRef(false);
  const finished = useRef(false);
  const requested = useRef("");
  useEffect(() => {
    let active = true, timer;
    async function poll() {
      try {
        const next = await api("/status");
        if (!active) return;
        setState(next);
        if ((!next.vm_running || account.macos_password_pending) && !next.job?.running && !next.job?.error && !started.current) {
          started.current = true;
          await api("/start", {guest_system:"macos"});
        }
        let measured = {};
        if (next.ready) {
          try { measured = await api("/macos/first-setup", {action:"permissions"}); }
          catch { /* OS login and Setup Assistant run before the permission helper. */ }
        }
        if (active) setPermissions(measured);
        const pending = steps.find(([id]) => !measured[id]);
        if (active && measured.managed_credential && pending && requested.current !== pending[0]) {
          requested.current = pending[0];
          setPermissionPage(pending[0]);
          try { await api("/macos/first-setup",{action:pending[0]}); }
          catch (e) { if (active) setError(e.message); }
        }
        if (active && steps.every(([id]) => measured[id]) && !finished.current) {
          finished.current = true;
          try {
            const result = await api("/macos/first-setup", {action:"finish"});
            if (result.error) throw new Error(result.error);
            if (active) await refresh();
          } catch (e) {
            if (active) { setCompletionFailed(true); setError(e.message); }
          }
        }
      } catch (e) { if (active) setError(e.message); }
      if (active) timer = setTimeout(poll, 2000);
    }
    poll();
    window.cageWorkbenchAction = async command => {
      if (command === "closeTab") document.dispatchEvent(new Event("cage-close-modal"));
      if (command === "quit") await native("quitNow");
    };
    window.cageEditAction = () => false;
    if (nativeAvailable()) native("workbenchReady").catch(() => {});
    return () => { active = false; clearTimeout(timer); delete window.cageWorkbenchAction; delete window.cageEditAction; };
  }, []);
  async function act(action) {
    if (busy) return;
    if (steps.some(([id]) => id === action)) setPermissionPage(action);
    setBusy(true); setError("");
    try {
      if (action === "start") await api("/start", {guest_system:"macos"});
      else {
        const result = await api("/macos/first-setup", {action});
        if (result.error) throw new Error(result.error);
        if (action === "finish") { await refresh(); setCompletionFailed(false); }
      }
    } catch (e) {
      if (action === "finish") setCompletionFailed(true);
      setError(e.message);
    }
    finally { setBusy(false); }
  }
  const controls = <>{steps.map(([id, label]) => <button key={id} disabled={busy || !state?.ready}
    onClick={() => act(id)}>{label}{permissions[id] && " ✓"}</button>)}
    </>;
  const permissionHint = {
    screen: "打开 Cage Guest Window 的窗口录制开关。如果提示「退出并重新打开」，按提示操作。",
    input: "打开 Cage Guest Window 的辅助功能开关，允许 Cage 操作客机窗口。",
    projects: "打开 macFUSE 和 macFUSE (local) 两个开关，用于在客机中访问你选择的项目。",
  }[permissionPage];
  const progress = !state?.ready ? "正在等待虚拟 Mac 就绪；启动后再打开客机系统设置。" :
    steps.every(([id]) => permissions[id]) ? completionFailed ? "授权已通过，请点击重试完成设置。" : "授权已通过，正在进入工作台…" :
      `请确认：${steps.filter(([id]) => !permissions[id]).map(([, label]) => label).join("、")}。Cage 会自动检查并继续。`;
  return <main className="welcome mac-first-setup"><section className="welcome-content">
    <h1>正在准备虚拟 Mac</h1>
    <p>Cage 会自动登录、配置并检查环境。首次使用只需确认下面的系统权限；这些操作都在虚拟 Mac 内完成。</p>
    <ol>
      <li>在 Cage 打开的「窗口录制」「辅助功能」设置里允许 <strong>Cage Guest Window</strong>。如果提示「退出并重新打开」，按提示操作。需要重新定位时，点击同名按钮。</li>
      <li>Cage 会直接打开「文件系统扩展」面板，你只需打开 <strong>macFUSE</strong> 和 <strong>macFUSE (local)</strong> 两个开关。需要重新打开页面时，点击「文件系统扩展」。完成后 Cage 会自动检查并进入工作台。</li>
    </ol>
    <p>授权时若要求管理员，用户名填 <strong>cagesetup</strong>，{account.macos_password_pending ? "密码正在等待同步，完成前可能仍需原客机密码。" : account.macos_credentials_ready ? "密码与 Cage 账户相同。" : "可点选系统密码框后点击「填写授权密码」。"}确认后继续授权。此管理员在完成后自动停用。</p>
    {(!state?.vm_running || account.macos_password_pending) && <button className="primary" disabled={busy || !state || state.job?.running} onClick={() => act("start")}>{account.macos_password_pending ? "重试同步客机密码" : "启动虚拟 Mac"}</button>}
    {state?.vm_running && <MacOSLogin initialSetup autoOpen={Boolean(permissions.managed_credential)} managedCredential={permissions.managed_credential} disabled={!state.ready} setupActions={controls}
      setupHint={`${permissionHint} 需要管理员授权时，用户名用 cagesetup；选中密码框后可点击「填写授权密码」。`}
      setupStatus={error || progress} setupError={Boolean(error)} />}
    <div className="welcome-shortcuts">{controls}</div>
    <p role="status">{progress}</p>
    {(completionFailed || error) && <button className="primary" disabled={busy || !steps.every(([id]) => permissions[id])} onClick={() => act("finish")}>重试完成设置</button>}
    {(error || (!state?.ready && state?.job?.error)) && <p role="alert">{error || state.job.error}</p>}
    <button className="text-button" onClick={() => setDiagnostics(!diagnostics)}>日志与诊断</button>
    {diagnostics && <DiagnosticSettings />}
  </section></main>;
}
