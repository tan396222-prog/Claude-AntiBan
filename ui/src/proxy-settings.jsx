import { InlineSelect } from "./inline-select";
import React, { useEffect, useRef, useState } from "react";
import {
  Download,
  Plus,
  Search,
  RefreshCw,
  Check,
  Activity,
  Server,
  Trash2,
  Pencil,
  LoaderCircle,
  Square,
  CircleHelp,
  ArrowRight,
  X,
  Route,
} from "lucide-react";
import { api } from "./api";
import { SafetyMessage } from "./safety-message";
import { ProxyYAMLInput } from "./proxy-yaml-input";
import "./proxy.css";

const bytes = (v) => {
  const n = Math.max(0, Number(v) || 0);
  if (n < 1024) return `${n.toFixed(0)} B`;
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(1)} KiB`;
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MiB`;
  return `${(n / 1024 ** 3).toFixed(2)} GiB`;
};
function usageLabel(raw) {
  const values = Object.fromEntries(
    (raw || "").split(";").map((v) => v.trim().split("=")),
  );
  const used = Number(values.upload || 0) + Number(values.download || 0);
  return [
    values.total ? `${bytes(used)} / ${bytes(values.total)}` : "",
    Number(values.expire) > 0
      ? `到期 ${new Date(Number(values.expire) * 1000).toLocaleDateString()}`
      : "",
  ]
    .filter(Boolean)
    .join(" · ");
}
const emptyForm = {
  name: "",
  url: "",
  content: "",
  interval_hours: 24,
  host_download: false,
};
export function ProxySettings({ status, onApplied, onConfirm, onDirty, initializing = false }) {
  const [proxy, setProxy] = useState(null),
    [tab, setTab] = useState("nodes"),
    [search, setSearch] = useState(""),
    [source, setSource] = useState(""),
    [form, setForm] = useState(null),
    [mode, setMode] = useState("url"),
    [error, setError] = useState(""),
    [pending, setPending] = useState(""),
    [notice, setNotice] = useState(""),
    [delays, setDelays] = useState({}),
    [connections, setConnections] = useState({ connections: [] }),
    [logs, setLogs] = useState([]),
    [port, setPort] = useState("17896"),
    [rates, setRates] = useState({ up: 0, down: 0 });
  const [manual, setManual] = useState({
    name: "",
    type: "socks5",
    server: "",
    port: "1080",
    username: "",
    password: "",
  });
  const [routeDraft, setRouteDraft] = useState(null);
  const [hostRefresh, setHostRefresh] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [readingFile, setReadingFile] = useState(false);
  const alive = useRef(true),
    loading = useRef(false),
    latest = useRef(null),
    measuring = useRef(false),
    portDirty = useRef(false);
  const reload = async () => {
    const value = await api("/proxy");
    if (alive.current) {
      setProxy(value);
      if (!portDirty.current) setPort(String(value.port));
    }
    return value;
  };
  useEffect(() => {
    alive.current = true;
    const tick = async () => {
      if (loading.current) return;
      loading.current = true;
      try {
        await reload();
      } catch (e) {
        if (alive.current) setError(e.message);
      } finally {
        loading.current = false;
      }
    };
    tick();
    const timer = setInterval(tick, 4000);
    return () => {
      alive.current = false;
      clearInterval(timer);
    };
  }, []);
  useEffect(() => {
    if (tab !== "connections" && tab !== "diagnostics") return;
    let cancelled = false;
    const tick = async () => {
      try {
        if (tab === "diagnostics") {
          const result = await api("/proxy/logs");
          if (!cancelled) setLogs(result.lines);
        } else {
          const result = await api("/proxy/connections"),
            now = Date.now();
          if (cancelled) return;
          if (latest.current) {
            const seconds = (now - latest.current.time) / 1000;
            if (seconds > 0)
              setRates({
                up: Math.max(
                  0,
                  (result.uploadTotal - latest.current.uploadTotal) / seconds,
                ),
                down: Math.max(
                  0,
                  (result.downloadTotal - latest.current.downloadTotal) /
                    seconds,
                ),
              });
          }
          latest.current = { ...result, time: now };
          setConnections(result);
        }
      } catch (e) {
        if (!cancelled) setError(e.message);
      }
    };
    tick();
    const timer = setInterval(tick, 3000);
    return () => {
      cancelled = true;
      clearInterval(timer);
      latest.current = null;
    };
  }, [tab]);
  const busy = Boolean(pending) || Boolean(status.job?.running);
  const mutate = async (action, input = {}) => {
    setError("");
    setNotice("");
    setPending(action);
    try {
      const job = await api(`/proxy/${action}`, input);
      let current;
      do {
        await new Promise((resolve) => setTimeout(resolve, 500));
        current = (await api("/status")).job;
        if (current.id !== job.id)
          throw new Error("操作已被其他任务替代，请查看当前状态");
      } while (current.running);
      portDirty.current = false;
      const updated = await reload();
      await onApplied();
      if (current.error) throw new Error(current.error);
      if (alive.current) {
        setForm(null);
        if (action === "route" || action === "select") setRouteDraft(null);
        const importedSource =
          action === "import"
            ? updated.sources.at(-1)
            : action === "refresh"
              ? updated.sources.find((source) => source.id === input.id)
              : null;
        setNotice(
          importedSource
            ? `已导入 ${importedSource.nodes.length} 个节点，跳过 ${importedSource.skipped?.length || 0} 个${importedSource.skipped?.length ? "；可在来源下查看跳过原因" : ""}`
            : current.message || "已完成",
        );
        setDelays({});
      }
    } catch (e) {
      if (alive.current) setError(e.message);
    } finally {
      if (alive.current) setPending("");
    }
  };
  const applyRoute = async (nodeIDs) => {
    if (
      !proxy.active &&
      status.vm_running &&
      !(await onConfirm({
        title: "启用内置代理并重启环境？",
        body: "首次启用或修改代理端口需要重启，当前终端会结束。后续只切换节点会保留终端进程，但网络连接会重新建立。",
        confirm: "启用并重启",
      }))
    )
      return;
    mutate("route", { node_ids: nodeIDs, port: Number(port) });
  };
  const measure = async (ids) => {
    if (measuring.current) return;
    measuring.current = true;
    setPending("delay");
    setError("");
    try {
      // A small worker pool bounds probes without changing the selected group.
      let index = 0;
      await Promise.all(
        Array.from({ length: Math.min(4, ids.length) }, async () => {
          while (index < ids.length && alive.current) {
            const id = ids[index++];
            try {
              const result = await api("/proxy/delay", { node_id: id });
              if (alive.current)
                setDelays((old) => ({ ...old, [id]: `${result.delay} ms` }));
            } catch {
              if (alive.current)
                setDelays((old) => ({ ...old, [id]: "不可用 / 未载入" }));
            }
          }
        }),
      );
    } finally {
      measuring.current = false;
      if (alive.current) setPending("");
    }
  };
  const submit = () => {
    let value = {
      id: form.id || "",
      name: form.name,
      url: form.url,
      content: form.content,
      interval_hours: form.interval_hours,
      host_download: form.host_download,
    };
    if (mode === "manual" && !form.id) {
      const node = { ...manual, port: Number(manual.port) };
      value = {
        ...form,
        name: form.name || manual.name,
        url: "",
        content: JSON.stringify({ proxies: [node] }),
        interval_hours: 0,
      };
    }
    mutate(form.id ? "edit" : "import", value);
  };
  const savedRoute = proxy?.route?.length
    ? proxy.route
    : proxy?.selected
      ? [proxy.selected]
      : [""];
  const route = routeDraft ?? savedRoute;
  const routeChanged = JSON.stringify(route) !== JSON.stringify(savedRoute);
  useEffect(() => {
    onDirty?.(routeChanged || initializing && Boolean(form));
    return () => onDirty?.(false);
  }, [onDirty, routeChanged, initializing, Boolean(form)]);
  if (!proxy)
    return <div className="proxy-loading" role={error ? "alert" : "status"}>{error || "正在读取代理配置…"}</div>;
  const allNodes = proxy.sources.flatMap((s) =>
    s.nodes.map((n) => ({ ...n, source: s.name, sourceID: s.id })),
  );
  const selected = allNodes.find((n) => n.id === proxy.selected);
  const routeValid =
    route.every((id) => allNodes.some((n) => n.id === id)) &&
    new Set(route).size === route.length;
  const routeName = (id) => allNodes.find((n) => n.id === id)?.name || "待选择";
  const updateHop = (index, id) =>
    setRouteDraft(route.map((current, i) => (i === index ? id : current)));
  const nodes = allNodes.filter(
    (n) =>
      (!source || n.sourceID === source) &&
      `${n.name} ${n.source} ${n.type}`
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  return (
    <div className="proxy-manager">
      <div className="section-title">
        <div className="proxy-heading">
          <h3>内置代理</h3>
          <button
            type="button"
            className="icon-button proxy-help-toggle"
            aria-label="代理使用说明"
            title={helpOpen ? "收起代理使用说明" : "查看代理使用说明"}
            aria-expanded={helpOpen}
            aria-controls="proxy-help"
            onClick={() => setHelpOpen((open) => !open)}
          >
            <CircleHelp size={17} aria-hidden="true" />
          </button>
        </div>
        <p>选择最终出口，按需添加中转，供 Cage 内所有程序共用。</p>
      </div>
      {helpOpen && (
        <section
          id="proxy-help"
          className="proxy-help"
          aria-labelledby="proxy-help-title"
        >
          <h4 id="proxy-help-title">代理使用说明</h4>
          <p className="proxy-help-intro">
            Cage 自带代理核心，无需另外安装
            Clash。请先准备你已有的代理订阅或节点信息，Cage 不提供代理节点。
          </p>
          <ol className="proxy-help-steps">
            <li>
              <strong>导入你的代理</strong>
              <p>打开「订阅」→「添加来源」，填写来源名称，再选择一种方式：</p>
              <ul>
                <li>
                  <strong>订阅链接：</strong>从代理服务商处复制 Clash/Mihomo
                  格式的 HTTPS
                  订阅地址，粘贴后点击「导入并验证」。首次还没有可用节点时，勾选「通过主机网络下载这份订阅」；主机需要能访问该地址。
                </li>
                <li>
                  <strong>本地 YAML：</strong>拖入或选择配置文件，或粘贴含节点的
                  YAML 配置，再点击「导入并验证」。可从已有代理客户端导出配置。
                </li>
                <li>
                  <strong>手动节点：</strong>适用于 HTTP CONNECT 或
                  SOCKS5。填写节点名称、服务器地址、端口，以及服务商提供的用户名和密码（如有），再点击「导入并验证」。
                </li>
              </ul>
            </li>
            <li>
              <strong>选择节点并启用</strong>
              <p>
                导入完成后切到「节点」，选择最终出口，再点击「应用代理」。住宅代理无法从国内连接时，选择「链式代理」，在它前面添加可连接的中转节点；节点可以来自不同订阅。首次启用时若环境正在运行，需要重启；后续切换只重建网络连接。
              </p>
            </li>
            <li>
              <strong>确认可以联网</strong>
              <p>
                {initializing ? "点击“开始安装”，等待客机安装和网络检查完成。以初始化界面的检查结果为准；" : "回到工作台启动环境，等待网络检查通过。以工作台的联网状态为准；"}「测速」只检测节点延迟。Cage
                内的浏览器、终端和开发工具会共用所选节点，无需分别配置代理。
              </p>
            </li>
          </ol>
          <div className="proxy-help-notes">
            <strong>导入失败或无法联网？</strong>
            <p>
              确认复制的是订阅地址，而非服务商登录页或管理页。暂不支持单条分享链接、Base64
              节点列表，以及只引用远程节点列表的配置；请向服务商获取含节点的
              Clash/Mihomo YAML。
            </p>
            <p>
              订阅下载失败时，可检查主机是否能访问订阅地址，或改用本地
              YAML。节点不可用时，换一个节点并查看「诊断」；更换节点后仍需通过出口
              IP 和地区检查。
            </p>
          </div>
        </section>
      )}
      <div className="proxy-summary">
        <Server size={22} />
        <div>
          <strong>
            {selected ? `最终出口 · ${selected.name}` : "尚未配置代理节点"}
          </strong>
          {savedRoute.length > 1 && (
            <span className="proxy-current-route">
              当前链路：{savedRoute.map(routeName).join(" → ")}
            </span>
          )}
          <span>
            {proxy.active
              ? proxy.running
                ? "Cage 自带核心运行中"
                : "核心未就绪"
              : "请选择节点后应用，未配置时无法联网"}{" "}
            · 独立端口 {proxy.port}
          </span>
        </div>
        <span className={`proxy-badge ${proxy.network_open ? "online" : ""}`}>
          {proxy.state_label}
        </span>
      </div>
      {proxy.active && status.location && (
        <p className="field-help">
          最近检测出口：{status.location.ip} · {status.location.country}{" "}
          {status.location.city}。联网是否放行以工作台检查状态为准。
        </p>
      )}
      {!proxy.core_available && (
        <div role="alert" className="error-message">
          安装包缺少随附代理核心，请重新构建或安装完整 Cage 应用。
        </div>
      )}
      <p className="field-help">
        仅允许经节点转发的 IPv4 TCP 和 DNS；公网 UDP、IPv6、ICMP
        持续阻断。切换会关闭旧网络连接并重新检查出口。
      </p>
      <div role="tablist" aria-label="代理管理" className="proxy-tabs">
        {[
          ["nodes", "节点"],
          ["sources", "订阅"],
          ["connections", "连接"],
          ["diagnostics", "诊断"],
        ].map(([id, label]) => (
          <button
            key={id}
            role="tab"
            aria-selected={tab === id}
            onClick={() => setTab(id)}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === "nodes" && (
        <>
          {allNodes.length > 0 && (
            <section
              className="proxy-route"
              aria-labelledby="proxy-route-title"
            >
              <div className="proxy-route-heading">
                <div>
                  <h4 id="proxy-route-title">配置连接方式</h4>
                  <p>直接使用一个出口，或经中转连接住宅代理。</p>
                </div>
                <div
                  className="proxy-route-mode"
                  role="group"
                  aria-label="连接方式"
                >
                  <button
                    type="button"
                    aria-pressed={route.length === 1}
                    disabled={busy}
                    onClick={() => setRouteDraft([route.at(-1)])}
                  >
                    单节点
                  </button>
                  <button
                    type="button"
                    aria-pressed={route.length > 1}
                    disabled={busy}
                    onClick={() => {
                      if (route.length === 1) setRouteDraft(["", route[0]]);
                    }}
                  >
                    链式代理
                  </button>
                </div>
              </div>
              <ol className="proxy-route-hops">
                {route.map((id, index) => {
                  const final = index === route.length - 1;
                  const label = final
                    ? "最终出口"
                    : `中转节点${route.length > 2 ? ` ${index + 1}` : ""}`;
                  return (
                    <li
                      key={index}
                      className={`proxy-route-hop ${final ? "is-exit" : ""}`}
                    >
                      <span className="proxy-hop-number" aria-hidden="true">
                        {index + 1}
                      </span>
                      <div className="proxy-hop-control">
                        <label htmlFor={`proxy-hop-${index}`}>{label}</label>
                        <InlineSelect
                          id={`proxy-hop-${index}`}
                          aria-label={label}
                          value={id}
                          disabled={busy}
                          onValueChange={(next) => updateHop(index, next)}
                        >
                          <option value="" disabled>
                            请选择{label}
                          </option>
                          {Boolean(id) &&
                            !allNodes.some((n) => n.id === id) && (
                              <option value={id} disabled>
                                节点已被移除，请重新选择
                              </option>
                            )}
                          {proxy.sources.map((src) => (
                            <optgroup key={src.id} label={src.name}>
                              {src.nodes.map((n) => (
                                <option
                                  key={n.id}
                                  value={n.id}
                                  disabled={route.some(
                                    (hop, i) => i !== index && hop === n.id,
                                  )}
                                >
                                  {n.name}
                                </option>
                              ))}
                            </optgroup>
                          ))}
                        </InlineSelect>
                        {final && <small>目标网站看到此节点的出口 IP</small>}
                      </div>
                      {!final && (
                        <button
                          className="icon-button proxy-hop-remove"
                          type="button"
                          disabled={busy}
                          aria-label={`移除${label}`}
                          title={`移除${label}`}
                          onClick={() =>
                            setRouteDraft(route.filter((_, i) => i !== index))
                          }
                        >
                          <X size={15} />
                        </button>
                      )}
                    </li>
                  );
                })}
              </ol>
              {route.length > 1 && route.length < allNodes.length && (
                <button
                  type="button"
                  className="proxy-route-add"
                  disabled={busy || route.length >= allNodes.length}
                  onClick={() =>
                    setRouteDraft([...route.slice(0, -1), "", route.at(-1)])
                  }
                >
                  <Plus size={14} />
                  添加中转
                </button>
              )}
              <div className="proxy-route-preview" aria-label="待应用链路">
                <Route size={16} aria-hidden="true" />
                <span>Cage</span>
                {route.map((id, index) => (
                  <React.Fragment key={index}>
                    <ArrowRight size={13} aria-hidden="true" />
                    <strong>{routeName(id)}</strong>
                  </React.Fragment>
                ))}
                <ArrowRight size={13} aria-hidden="true" />
                <span>目标网站</span>
              </div>
              <div className="proxy-route-actions">
                <span aria-live="polite">
                  {routeChanged
                    ? "有未应用的代理修改"
                    : proxy.active
                      ? "当前配置已应用"
                      : "选择完成后应用代理"}
                </span>
                <div>
                  {routeDraft !== null && (
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => setRouteDraft(null)}
                    >
                      撤销修改
                    </button>
                  )}
                  <button
                    type="button"
                    className={routeChanged || !proxy.active ? "primary" : ""}
                    disabled={busy || !proxy.core_available || !routeValid}
                    onClick={() => applyRoute(route)}
                  >
                    {pending === "route" ? (
                      <LoaderCircle size={14} className="spin" />
                    ) : !routeChanged && proxy.active ? (
                      <RefreshCw size={14} />
                    ) : (
                      <Check size={14} />
                    )}
                    {routeChanged || !proxy.active ? "应用代理" : "重新应用"}
                  </button>
                </div>
              </div>
              {proxy.route_error && (
                <p role="alert" className="proxy-source-error">
                  <SafetyMessage message={proxy.route_error} />
                </p>
              )}
              {error && (
                <div role="alert" className="error-message">
                  <SafetyMessage message={error} />
                </div>
              )}
            </section>
          )}
          {allNodes.length > 0 && (
            <div className="proxy-node-heading">
              <h4>可用节点</h4>
              <button
                type="button"
                disabled={busy}
                onClick={() => {
                  setTab("sources");
                  setForm({ ...emptyForm });
                  setMode("url");
                }}
              >
                <Plus size={14} />
                添加来源
              </button>
            </div>
          )}
          <div className="proxy-toolbar">
            <label className="proxy-search">
              <Search size={15} />
              <input
                aria-label="搜索节点"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="搜索节点、来源或协议"
              />
            </label>
            <InlineSelect
              aria-label="节点来源"
              value={source}
              onValueChange={(next) => setSource(next)}
            >
              <option value="">全部来源</option>
              {proxy.sources.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </InlineSelect>
            <button
              disabled={busy || !proxy.running || !nodes.length}
              title="检测 HTTP 往返延迟，通常不含建连开销，不代表网页首开耗时"
              onClick={() => measure(nodes.map((n) => n.id))}
            >
              <Activity size={14} />
              测速
            </button>
          </div>
          {!allNodes.length ? (
            <div className="proxy-empty">
              <Download size={26} />
              <strong>添加第一份节点来源</strong>
              <p>支持 Clash/Mihomo YAML 订阅、本地配置或手动节点。</p>
              <button
                onClick={() => {
                  setTab("sources");
                  setForm({ ...emptyForm });
                  setMode("url");
                }}
              >
                <Plus size={15} />
                添加来源
              </button>
            </div>
          ) : (
            <div className="proxy-node-list">
              {nodes.map((n) => (
                <div
                  className={`proxy-node ${n.id === route.at(-1) ? "selected" : ""}`}
                  key={n.id}
                >
                  <button
                    className="proxy-node-choice"
                    disabled={
                      busy ||
                      !proxy.core_available ||
                      route.slice(0, -1).includes(n.id)
                    }
                    title={
                      route.slice(0, -1).includes(n.id)
                        ? "此节点已用作中转"
                        : "选为最终出口，点击应用代理后生效"
                    }
                    onClick={() => updateHop(route.length - 1, n.id)}
                    aria-pressed={n.id === route.at(-1)}
                    aria-label={`选择节点 ${n.name}`}
                  >
                    <span className="proxy-radio">
                      {n.id === route.at(-1) && <Check size={12} />}
                    </span>
                    <span>
                      <strong>{n.name}</strong>
                      <small>
                        {n.source} · {n.type.toUpperCase()}
                      </small>
                    </span>
                  </button>
                  {savedRoute.includes(n.id) && (
                    <span className="proxy-node-role">
                      {n.id === proxy.selected ? "当前出口" : "当前中转"}
                    </span>
                  )}
                  <span className="proxy-delay">
                    {delays[n.id] || "未测速"}
                  </span>
                  <button
                    aria-label={`测速 ${n.name}`}
                    title="检测此节点到 www.gstatic.com 的 HTTP 往返延迟，通常不含建连开销，不代表网页首开耗时"
                    disabled={busy || !proxy.running}
                    onClick={() => measure([n.id])}
                  >
                    <Activity size={14} />
                  </button>
                </div>
              ))}
              {!nodes.length && (
                <p className="field-help">没有符合搜索条件的节点。</p>
              )}
            </div>
          )}
        </>
      )}
      {tab === "sources" && (
        <>
          <label className="check-line">
            <input
              type="checkbox"
              checked={hostRefresh}
              onChange={(e) => setHostRefresh(e.target.checked)}
            />
            手动更新订阅时通过主机网络下载
          </label>
          <p className="field-help">
            自动更新始终使用当前配置的代理，不会切换到主机下载。
          </p>
          <div className="proxy-toolbar">
            <span>
              {proxy.sources.length} 个来源 · {allNodes.length} 个节点
            </span>
            <button
              disabled={busy}
              onClick={() => {
                setForm({ ...emptyForm });
                setMode("url");
              }}
            >
              <Plus size={14} />
              添加来源
            </button>
          </div>
          {proxy.sources.map((s) => (
            <div className="proxy-source" key={s.id}>
              <div>
                <strong>{s.name}</strong>
                <small>
                  {s.remote ? s.host : "本地导入"} · {s.nodes.length} 个节点 ·{" "}
                  {s.interval_hours && s.remote
                    ? `每 ${s.interval_hours} 小时更新`
                    : "手动更新"}
                </small>
                <small>
                  更新于 {new Date(s.updated_at).toLocaleString()}
                  {usageLabel(s.usage) && ` · ${usageLabel(s.usage)}`}
                </small>
                {s.error && (
                  <span className="proxy-source-error">{s.error}</span>
                )}
                {s.skipped?.length > 0 && (
                  <details className="proxy-source-skipped">
                    <summary>
                      已导入 {s.nodes.length} 个节点，跳过 {s.skipped.length} 个
                      · 查看原因
                    </summary>
                    <ul>
                      {s.skipped.map((issue) => (
                        <li key={issue.index}>
                          第 {issue.index} 个节点
                          {issue.name && `「${issue.name}」`}
                          {issue.type && `（${issue.type}）`}：{issue.reason}
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
              </div>
              <div className="proxy-source-actions">
                {s.remote && (
                  <button
                    disabled={busy}
                    aria-label={`更新 ${s.name}`}
                    onClick={() =>
                      mutate("refresh", {
                        id: s.id,
                        host_download: hostRefresh,
                      })
                    }
                  >
                    <RefreshCw size={14} />
                  </button>
                )}
                <button
                  disabled={busy}
                  aria-label={`编辑 ${s.name}`}
                  onClick={() => {
                    setForm({
                      ...emptyForm,
                      id: s.id,
                      name: s.name,
                      interval_hours: s.interval_hours,
                      remote: s.remote,
                    });
                    setMode("url");
                  }}
                >
                  <Pencil size={14} />
                </button>
                <button
                  disabled={busy}
                  aria-label={`删除 ${s.name}`}
                  onClick={async () => {
                    if (
                      await onConfirm({
                        title: `删除来源“${s.name}”？`,
                        body: s.nodes.some((n) => n.id === proxy.selected)
                          ? "当前节点来自此来源。删除后 Cage 会保持离线，直到你选择其他节点。"
                          : "将移除此来源及其节点，本地项目文件不受影响。",
                        confirm: "删除来源",
                      })
                    )
                      mutate("delete", { id: s.id });
                  }}
                >
                  <Trash2 size={14} />
                </button>
              </div>
            </div>
          ))}
          {form && (
            <div className="proxy-import">
              <h4>{form.id ? "编辑来源" : "添加节点来源"}</h4>
              {!form.id && (
                <div className="proxy-tabs">
                  {[
                    ["url", "订阅链接"],
                    ["file", "本地 YAML"],
                    ["manual", "手动节点"],
                  ].map(([id, label]) => (
                    <button
                      key={id}
                      className={mode === id ? "selected" : ""}
                      onClick={() => {
                        setMode(id);
                        setForm((old) => ({ ...old, content: "", url: "" }));
                      }}
                    >
                      {label}
                    </button>
                  ))}
                </div>
              )}
              <label>
                来源名称
                <input
                  value={form.name}
                  maxLength={100}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="例如：日常代理"
                />
              </label>
              {mode === "url" && (!form.id || form.remote) && (
                <>
                  <label>
                    {form.id ? "新订阅链接（留空保留）" : "订阅链接"}
                    <input
                      value={form.url}
                      type="url"
                      autoComplete="off"
                      onChange={(e) =>
                        setForm({ ...form, url: e.target.value })
                      }
                      placeholder="https://…"
                    />
                  </label>
                  <label>
                    自动更新间隔
                    <InlineSelect
                      aria-label="自动更新间隔"
                      value={form.interval_hours}
                      onValueChange={(next) =>
                        setForm({
                          ...form,
                          interval_hours: Number(next),
                        })
                      }
                    >
                      {[
                        [0, "仅手动更新"],
                        [1, "每小时"],
                        [6, "每 6 小时"],
                        [24, "每天"],
                        [168, "每周"],
                      ].map(([v, label]) => (
                        <option value={v} key={v}>
                          {label}
                        </option>
                      ))}
                    </InlineSelect>
                  </label>
                  {!form.id && (
                    <label className="check-line">
                      <input
                        type="checkbox"
                        checked={form.host_download}
                        onChange={(e) =>
                          setForm({ ...form, host_download: e.target.checked })
                        }
                      />
                      通过主机网络下载这份订阅
                    </label>
                  )}
                  <p className="field-help">
                    默认经当前配置的代理下载。首次没有可用节点时，可勾选主机下载或导入本地文件；失败不会自动改用其他路径。
                  </p>
                </>
              )}
              {mode === "file" && !form.id && (
                <ProxyYAMLInput
                  content={form.content}
                  disabled={busy}
                  onReadingChange={setReadingFile}
                  onContentChange={(content) =>
                    setForm((old) => ({ ...old, content }))
                  }
                  onFileRead={(value, content) =>
                    setForm((old) => ({
                      ...old,
                      content,
                      name:
                        old.name ||
                        value.name.replace(/\.[^.]+$/, "").slice(0, 100),
                      interval_hours: 0,
                    }))
                  }
                />
              )}
              {mode === "manual" && !form.id && (
                <>
                  <div className="field-row">
                    <label>
                      节点名称
                      <input
                        value={manual.name}
                        onChange={(e) =>
                          setManual({ ...manual, name: e.target.value })
                        }
                      />
                    </label>
                    <label>
                      协议
                      <InlineSelect
                        aria-label="协议"
                        value={manual.type}
                        onValueChange={(next) =>
                          setManual({ ...manual, type: next })
                        }
                      >
                        <option value="socks5">SOCKS5</option>
                        <option value="http">HTTP CONNECT</option>
                      </InlineSelect>
                    </label>
                  </div>
                  <div className="field-row">
                    <label>
                      服务器
                      <input
                        value={manual.server}
                        onChange={(e) =>
                          setManual({ ...manual, server: e.target.value })
                        }
                        placeholder="域名或 IP"
                      />
                    </label>
                    <label>
                      端口
                      <input
                        type="number"
                        min="1"
                        max="65535"
                        value={manual.port}
                        onChange={(e) =>
                          setManual({ ...manual, port: e.target.value })
                        }
                      />
                    </label>
                  </div>
                  <div className="field-row">
                    <label>
                      用户名
                      <input
                        value={manual.username}
                        autoComplete="off"
                        onChange={(e) =>
                          setManual({ ...manual, username: e.target.value })
                        }
                      />
                    </label>
                    <label>
                      密码
                      <input
                        type="password"
                        value={manual.password}
                        autoComplete="new-password"
                        onChange={(e) =>
                          setManual({ ...manual, password: e.target.value })
                        }
                      />
                    </label>
                  </div>
                </>
              )}
              <div className="proxy-form-actions">
                <button disabled={busy} onClick={() => setForm(null)}>
                  取消
                </button>
                <button
                  className="primary"
                  disabled={
                    busy ||
                    readingFile ||
                    (!form.id &&
                      (mode === "url"
                        ? !form.url || !form.name
                        : mode === "file"
                          ? !form.content.trim() || !form.name
                          : !manual.name || !manual.server))
                  }
                  onClick={submit}
                >
                  {form.id ? "保存来源" : "导入并验证"}
                </button>
              </div>
            </div>
          )}
        </>
      )}
      {tab === "connections" && (
        <>
          <div className="proxy-metrics">
            <span>
              上传 <strong>{bytes(rates.up)}/s</strong>
              <small>累计 {bytes(connections.uploadTotal)}</small>
            </span>
            <span>
              下载 <strong>{bytes(rates.down)}/s</strong>
              <small>累计 {bytes(connections.downloadTotal)}</small>
            </span>
          </div>
          <p className="field-help">
            显示当前核心的连接与流量，包含 Cage
            自身检测请求；重启核心后累计值重置。
          </p>
          <div className="proxy-node-list">
            {connections.connections.map((c) => (
              <div className="proxy-connection" key={c.id}>
                <span>
                  {c.host}:{c.port}
                  <small>{c.network}</small>
                </span>
                <small>
                  ↑ {bytes(c.upload)} · ↓ {bytes(c.download)}
                </small>
              </div>
            ))}
            {!connections.connections.length && (
              <p className="proxy-empty">暂无活动连接</p>
            )}
          </div>
        </>
      )}
      {tab === "diagnostics" && (
        <>
          <p className="field-help">
            随附核心 {proxy.version || "未运行"}。以下为 Cage
            状态摘要；含凭据风险的核心原始日志不向页面公开。
          </p>
          <div className="proxy-diagnostics" role="log">
            {logs.map((line, i) => (
              <div key={i}>{line}</div>
            ))}
          </div>
          <label>
            独立代理端口
            <input
              type="number"
              min="1024"
              max="65535"
              value={port}
              onChange={(e) => {
                portDirty.current = true;
                setPort(e.target.value);
              }}
            />
          </label>
          <div className="proxy-toolbar">
            <button
              disabled={busy || !selected}
              onClick={async () => {
                if (
                  Number(port) !== proxy.port &&
                  status.vm_running &&
                  !(await onConfirm({
                    title: "更换代理端口并重启环境？",
                    body: "端口变化需要重启环境，当前终端会结束。",
                    confirm: "更换并重启",
                  }))
                )
                  return;
                mutate("restart", { port: Number(port) });
              }}
            >
              <RefreshCw size={14} />
              应用端口并重启核心
            </button>
            <button
              disabled={busy || !proxy.running}
              onClick={() => mutate("stop")}
            >
              <Square size={14} />
              停止核心
            </button>
          </div>
          <p className="field-help">
            Cage 不设置系统代理或主机 TUN。本机其他代理的 TUN
            仍可能影响核心出站，需要结合实际路由验证。
          </p>
        </>
      )}
      {pending && (
        <p className="proxy-progress" role="status">
          <LoaderCircle size={14} className="spin" />
          {pending === "delay"
            ? "正在检测节点延迟…"
            : status.job?.message || "正在处理…"}
        </p>
      )}
      {error && !(tab === "nodes" && allNodes.length > 0) && (
        <div role="alert" className="error-message">
          <SafetyMessage message={error} />
        </div>
      )}
      {notice && (
        <p role="status" className="proxy-notice">
          <Check size={14} />
          {notice}
        </p>
      )}
    </div>
  );
}
