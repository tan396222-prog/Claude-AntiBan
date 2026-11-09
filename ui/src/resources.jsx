import React, { useEffect, useState } from "react";
import { api } from "./api";

const gib = (bytes) => `${(bytes / 1073741824).toFixed(1)} GiB`;

export function ResourceSettings({ status, onConfirm }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const [starting, setStarting] = useState(false);
  useEffect(() => {
    let live = true;
    let timer;
    const refresh = async () => {
      try {
        const next = await api("/environment/resources");
        if (live) { setData(next); setError(""); }
      } catch (e) { if (live) setError(e.message); }
      if (live) timer = setTimeout(refresh, 4000);
    };
    refresh();
    return () => { live = false; clearTimeout(timer); };
  }, [status.vm_running, status.job?.running]);
  const apply = async () => {
    if (!(await onConfirm({
      title: "重启并应用自动资源配置？",
      body: "正在运行的命令会结束，浏览器和编辑器会重启。请先保存内容；项目文件和登录数据保留。手动暂停的网络仍保持暂停。",
      confirm: "保存后重启",
    }))) return;
    setStarting(true);
    try { await api("/environment/resources", {}); }
    catch (e) { setError(e.message); }
    finally { setStarting(false); }
  };
  const observation = data?.observation;
  const guest = observation?.guest;
  const fresh = observation && Date.now() - Date.parse(observation.observed_at) < 10000;
  const resourceError = error || data?.resource_error;
  const resourcesVerified = data?.resources_verified === true && !resourceError;
  if (data?.runtime === "container") return <section className="environment-versions" aria-label="计算资源">
    <h4>服务器容器资源上限</h4>
    <p className="field-help">CPU、内存与进程数量由服务器上的容器资源组限制。</p>
    <div className="setting-summary-row"><span>资源限制核验</span><strong>{data.running ? resourcesVerified ? "已核验" : "未通过核验" : "环境未运行"}</strong></div>
    <div className="setting-summary-row"><span>容器内存上限</span><strong>{data.running ? resourcesVerified ? gib(data.configured_memory_bytes) : "未核验" : gib(data.recommended_memory_bytes)}</strong></div>
    <div className="setting-summary-row"><span>容器 CPU 上限</span><strong>{data.running ? resourcesVerified ? `${data.configured_cpus} 个逻辑核心` : "未核验" : `${data.recommended_cpus} 个逻辑核心`}</strong></div>
    <div className="setting-summary-row"><span>进程与线程总数上限</span><strong>{data.running ? resourcesVerified ? data.pids_limit : "未核验" : "运行后核验"}</strong></div>
    <p className="field-help">{data.running ? resourcesVerified ? "已从服务器核验容器所属资源组的实际限制。实时内存与 CPU 用量尚未提供。" : "无法确认运行中容器的实际资源限制，请查看下方错误或等待重新核验。" : "环境尚未运行，当前显示部署配置中的资源上限。"}</p>
    {resourceError && <p role="alert" className="error-message">{resourceError}</p>}
  </section>;
  return <section className="environment-versions" aria-label="计算资源">
    <h4>计算资源 · 自动管理</h4>
    <p className="field-help">内存随程序需求增长，持续空闲后逐步归还主机。默认上限为主机资源的一半；CPU 按实际任务使用，上限表示可同时使用的逻辑核心数。</p>
    <div className="setting-summary-row"><span>自动内存上限</span><strong>{data ? gib(data.recommended_memory_bytes) : "正在读取…"}</strong></div>
    <div className="setting-summary-row"><span>自动 CPU 上限</span><strong>{data ? `${data.recommended_cpus} 个逻辑核心` : "—"}</strong></div>
    {data?.running && <>
      <div className="setting-summary-row"><span>本次启动的内存上限</span><strong>{gib(data.configured_memory_bytes)}</strong></div>
      <div className="setting-summary-row"><span>环境内存用量</span><strong>{fresh && guest ? gib(guest.total_bytes - guest.available_bytes) : "等待采样"}</strong></div>
      <div className="setting-summary-row"><span>环境当前可用内存总量</span><strong>{fresh && guest ? gib(guest.total_bytes) : "—"}</strong></div>
      <div className="setting-summary-row"><span>CPU 使用率</span><strong>{fresh ? `${observation.cpu_percent.toFixed(0)}% / ${observation.cpus} 个核心` : "—"}</strong></div>
      {fresh && guest?.oom_kills > 0 && <p className="field-help" role="status">本次环境启动后，系统因内存不足终止过 {guest.oom_kills} 个进程。请降低重任务的并行数量，或使用内存更多的主机。</p>}
      {(observation?.pressure || observation?.host_pressure) && <p className="field-help" role="status">{observation.host_pressure ? "主机内存紧张，暂缓扩大分配。" : "环境内存紧张，正在上限内增加可用内存。达到上限后请减少同时运行的任务。"}</p>}
      {data.restart_required && <button disabled={starting || status.job?.running} onClick={apply}>重启并应用自动资源配置</button>}
    </>}
    {!data?.running && data && <p className="field-help">下次启动环境时应用自动配置。</p>}
    {(error || observation?.error) && <p role="alert" className="error-message">{error || observation.error}</p>}
    <p className="field-help">内存用量不含可回收缓存；这些数值反映环境内部状态，主机活动监视器还会统计虚拟化开销。短时故障导致内部断网后，会自动重新检查，通过后恢复；手动暂停需手动恢复。</p>
  </section>;
}
