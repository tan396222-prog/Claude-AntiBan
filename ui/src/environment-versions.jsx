import React, { useEffect, useState } from "react";
import { api } from "./api";

export function EnvironmentVersions({ status, onConfirm }) {
  const [data, setData] = useState(null), [error, setError] = useState(""), [loading, setLoading] = useState(false), [starting, setStarting] = useState(false);
  const refresh = async () => {
    setLoading(true);
    try { setData(await api("/environment/versions")); setError(""); }
    catch (e) { setError(e.message); }
    finally { setLoading(false); }
  };
  useEffect(() => {
    let live = true;
    setLoading(true);
    api("/environment/versions")
      .then((next) => { if (live) { setData(next); setError(""); } })
      .catch((e) => { if (live) setError(e.message); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [status.vm_running, status.job?.running]);
  const busy = Boolean(status.job?.running) || starting;
  const update = async (tools) => {
    if (!(await onConfirm({
      title: tools ? "更新环境工具？" : "更新环境组件？",
      body: "请先保存浏览器和编辑器中的内容。浏览器、编辑器会短暂重启，编辑器内的终端可能结束；工作台终端、项目文件和登录状态会保留。",
      confirm: tools ? "更新工具" : "更新组件",
    }))) return;
    setStarting(true); setError("");
    try { await api(tools ? "/environment/tools" : "/environment/update", {}); }
    catch (e) { setError(e.message); }
    finally { setStarting(false); }
  };
  const componentLabels = { current: "已同步", update_required: "需要更新", stopped: "环境已停止", unknown: "暂时无法确认" };
  const toolNames = { chromium: "浏览器", code_server: "文件编辑器", claude: "Claude Code", mise: "工具版本管理器", proxy: "代理核心" };
  return (
    <section className="environment-versions" aria-label="环境版本">
      <h4>版本与组件</h4>
      <div className="setting-summary-row"><span>控制服务</span><strong>{data?.controller?.version || "—"}</strong></div>
      <div className="setting-summary-row"><span>环境组件</span><strong>{componentLabels[data?.components?.status] || (loading ? "正在读取…" : "暂时无法确认")}</strong></div>
      {data?.tools && <table className="environment-tool-table">
        <thead><tr><th>工具</th><th>当前版本</th><th>受管版本</th></tr></thead>
        <tbody>{Object.entries(toolNames).map(([key, name]) => <tr key={key}><td>{name}</td><td>{data.tools[key]?.current || "—"}</td><td>{data.tools[key]?.required || "—"}</td></tr>)}</tbody>
      </table>}
      {(error || data?.error) && <p role="alert" className="error-message">{error || data.error}</p>}
      <div className="environment-version-actions">
        <button disabled={loading || busy} onClick={refresh}>{loading ? "正在读取…" : "刷新版本"}</button>
        {data?.components?.update_required && <button className="primary" disabled={busy || !status.vm_running} onClick={() => update(false)}>更新环境组件</button>}
        <button disabled={busy || !data?.tools_update_allowed} onClick={() => update(true)}>更新环境工具</button>
      </div>
      <p className="field-help">组件更新保留虚拟磁盘与终端。工具更新需先启动环境并同步组件，依赖通过已选代理下载；无需放行业务网络，也不会自动恢复已暂停的外网。</p>
    </section>
  );
}
