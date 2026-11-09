export function networkState(status, now = Date.now()) {
  if (!status?.vm_running) return { mode: "stopped", label: "环境已停止" };
  const gate = status.safety?.gate;
  if (!gate) return { mode: "unknown", label: "外网状态待确认" };
  const until = Date.parse(gate.until);
  if (gate.mode === "open" && Number.isFinite(until) && until > now)
    return { mode: "open", label: "外网已放行" };
  if (gate.mode === "checking" && Number.isFinite(until) && until > now)
    return { mode: "checking", label: "正在检查" };
  if (gate.mode === "closed" || (Number.isFinite(until) && until <= now))
    return { mode: "closed", label: "外网已关闭" };
  return { mode: "unknown", label: "外网状态待确认" };
}

export function canonicalExitPolicy(value = {}) {
  return {
    mode: value.mode || "dynamic",
    scope: value.scope || "country",
    ip: (value.ip || "").trim(),
    country_code: (value.country_code || "").trim().toUpperCase(),
    region: (value.region || "").trim().replace(/\s+/g, " "),
    city: (value.city || "").trim().replace(/\s+/g, " "),
  };
}

export function reportForSharing(report) {
  return {
    policy: report.policy,
    created_at: report.created_at,
    checks: report.checks,
    scope: "采样与运行时策略检查；不代表平台账号资格或完整虚拟化审计",
  };
}

export function isGuestLocalURL(value) {
  if (value === "about:blank") return true;
  try {
    const url = new URL(value);
    return (
      ["http:", "https:"].includes(url.protocol) &&
      ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)
    );
  } catch {
    return false;
  }
}
