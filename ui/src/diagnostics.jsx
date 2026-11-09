import React, { useEffect, useState } from "react";
import { ArrowDownToLine, LoaderCircle } from "lucide-react";
import { api, native, nativeAvailable } from "./api";
import { reportDiagnostic } from "./diagnostics.mjs";
import { isRemoteClient, runtimeURL } from "./client-context.mjs";

export function DiagnosticSettings() {
  const remote = isRemoteClient();
  const [info, setInfo] = useState(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    api("/diagnostics").then(value => { if (live) setInfo(value); })
      .catch(error => { if (live) setError(error.message); });
    return () => { live = false; };
  }, []);
  const save = async () => {
    setBusy(true); setError(""); setNotice("");
    try {
      if (nativeAvailable()) {
        const result = await native("exportDiagnostics");
        if (!result?.canceled) setNotice("诊断包已保存。分享前请检查其中内容。");
      } else {
        // Fetch before downloading so authentication or disk failures are shown.
        const response = await fetch(runtimeURL("/diagnostics/export"));
        if (!response.ok) throw new Error((await response.text()).trim() || "诊断包导出失败");
        if (!response.headers.get("content-type")?.startsWith("application/zip")) throw new Error("当前后台不支持诊断导出，请重新打开 Cage");
        const url = URL.createObjectURL(await response.blob());
        const link = document.createElement("a"); link.href = url; link.download = "cage-diagnostics.zip"; link.click();
        setTimeout(() => URL.revokeObjectURL(url), 30_000);
        setNotice("诊断包已下载。分享前请检查其中内容。");
      }
    } catch (error) { setError(error.message); }
    finally { setBusy(false); }
  };
  return <section className="settings-group" aria-label="日志与诊断">
    <h3>日志与诊断</h3>
    <p className="footnote">自动记录应用故障并脱敏，保留 14 天，容量预算 50 MiB（{remote ? "服务器" : "本机"}上限 40 MiB、环境服务预算 10 MiB）。不收集终端内容、项目文件或完整配置。</p>
    {info && <p className="footnote">{remote ? "服务器" : "本机"}日志 {(info.bytes / 1024 / 1024).toFixed(1)} MiB · {info.files} 个文件</p>}
    <button className="secondary-button" disabled={busy} onClick={save}>
      {busy ? <LoaderCircle size={14} className="spin" /> : <ArrowDownToLine size={14} />}
      {busy ? "正在导出…" : remote ? "导出服务器诊断包" : "导出诊断包"}
    </button>
    <p className="footnote">{remote ? "诊断包包含当前云端主机的脱敏日志、版本和收集状态，需要保持远程连接。导出后保存在你选择的本机位置。" : "诊断包包含脱敏日志、版本和收集状态；环境离线时仍可导出本机记录。只保存在你选择的位置。"}</p>
    {notice && <p role="status">{notice}</p>}
    {error && <p role="alert" className="error-message">{error}</p>}
  </section>;
}

export class DiagnosticBoundary extends React.Component {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidCatch(error, info) { reportDiagnostic("react", new Error(`${error.message}\n${error.stack || ""}\n${info.componentStack || ""}`)); }
  render() {
    if (!this.state.failed) return this.props.children;
    return <main style={{ padding: 32 }}>
      <h2>工作台显示遇到异常</h2>
      <p>后台任务仍可继续运行。可以导出诊断包，再重新加载界面。</p>
      <DiagnosticSettings />
      <button className="primary-button" onClick={() => location.reload()}>重新加载界面</button>
    </main>;
  }
}
