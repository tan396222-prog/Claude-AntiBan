import React, { useEffect, useState } from "react";
import { Activity } from "lucide-react";
import { api } from "./api";

export function NetworkLatency({ enabled, revision, selected }) {
  const [sample, setSample] = useState(null),
    [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false,
      timer;
    const measure = async () => {
      try {
        const proxy = await api("/proxy");
        if (cancelled) return;
        if (!proxy.active || !proxy.running || !proxy.selected) {
          setSample({ label: "延迟 —", detail: "当前代理未连接" });
        } else {
          const result = await api("/proxy/delay", { node_id: proxy.selected });
          if (cancelled) return;
          const current = await api("/proxy");
          if (cancelled) return;
          if (
            !current.active ||
            !current.running ||
            current.selected !== proxy.selected ||
            current.revision !== proxy.revision
          ) {
            setSample(null);
          } else if (Number.isFinite(result.delay) && result.delay > 0) {
            setSample({
              label: `${Math.round(result.delay)} ms`,
              detail:
                "当前节点到 www.gstatic.com 的 HTTP 往返延迟（统一延迟口径），通常不含建连开销，不代表网页首开耗时。每 30 秒更新，点击重新检测；环境联网状态单独检查。",
            });
          } else {
            setSample({
              label: "延迟不可用",
              detail: "当前节点测速未成功，点击重试",
            });
          }
        }
      } catch {
        if (!cancelled)
          setSample({
            label: "延迟不可用",
            detail: "无法测量当前节点延迟，点击重试",
          });
      } finally {
        if (!cancelled) timer = setTimeout(measure, 30000);
      }
    };
    measure();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [enabled, revision, selected, retry]);
  return (
    <button
      className="status-latency"
      aria-label={`网络延迟：${enabled ? sample?.label || "检测中" : "未连接"}`}
      title={
        enabled
          ? sample?.detail || "正在检测当前节点的网络延迟"
          : "环境启动并连接代理后检测网络延迟"
      }
      disabled={!enabled}
      onClick={() => {
        setSample(null);
        setRetry((value) => value + 1);
      }}
    >
      <Activity size={12} />
      <span>{enabled ? sample?.label || "测延迟…" : "延迟 —"}</span>
    </button>
  );
}
