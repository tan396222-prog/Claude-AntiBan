// External agents use the same native bridge as this window. No script or
// expression is accepted: native actions retain their existing platform checks.
import { clientStorageKey } from "./client-context.mjs";

export const agentClientPreferences = Object.freeze([
  { key: "cage-active", scoped: true, format: "string", description: "当前标签 ID" },
  { key: "cage-sidebar-collapsed", scoped: true, format: "JSON boolean", description: "侧栏收起状态" },
  { key: "cage-browser-tab-layout", scoped: true, format: 'JSON "horizontal" | "vertical"', description: "浏览器标签排列" },
  { key: "cage-tab-layout", scoped: true, format: "JSON object", description: "按工作区保存的标签名称、固定与顺序" },
  { key: "cage-keyboard", scoped: true, remoteOnly: true, format: 'string "mac" | "native"', description: "远端环境在当前客户端的快捷键习惯；本机使用 settings.keyboard" },
  { key: "cage.audio", scoped: false, format: "JSON {muted:boolean, volume:number 0..1}", description: "当前客户端的声音偏好" },
  { key: "cage-desktop-zoom", scoped: false, format: "string number: 0.75, 0.9, 1, 1.1, 1.25, 1.5", description: "Claude Desktop 显示比例" },
  { key: "cage-recent-environment", scoped: false, format: "string", description: "最近选择的 local 或主机 ID" },
]);

const object = (value) => value !== null && typeof value === "object" && !Array.isArray(value);
function validPreference(key, raw) {
  if (raw === null) return true;
  if (typeof raw !== "string") return false;
  if (["cage-active", "cage-recent-environment"].includes(key)) return true;
  if (key === "cage-keyboard") return ["mac", "native"].includes(raw);
  if (key === "cage-desktop-zoom") return [0.75, 0.9, 1, 1.1, 1.25, 1.5].includes(Number(raw));
  let value;
  try { value = JSON.parse(raw); } catch { return false; }
  if (key === "cage-sidebar-collapsed") return typeof value === "boolean";
  if (key === "cage-browser-tab-layout") return ["horizontal", "vertical"].includes(value);
  if (key === "cage.audio") return object(value) && typeof value.muted === "boolean" && Number.isFinite(value.volume) && value.volume >= 0 && value.volume <= 1 && Object.keys(value).every(field => ["muted", "volume"].includes(field));
  if (key === "cage-tab-layout") {
    if (!object(value)) return false;
    return Object.values(value).every(workspace => object(workspace) && Object.entries(workspace).every(([id, metadata]) => {
      if (id === "order") return Array.isArray(metadata) && metadata.every(item => typeof item === "string");
      if (id === "sessionOrder") return object(metadata) && Object.values(metadata).every(order => Array.isArray(order) && order.every(item => typeof item === "string"));
      return object(metadata) && Object.entries(metadata).every(([field, item]) => field === "title" ? typeof item === "string" : field === "pinned" && typeof item === "boolean");
    }));
  }
  return false;
}

function connectionChanged() {
  const error = new Error("工作环境已切换；未在新环境重放原生操作，请核对原操作结果。");
  error.code = "connection_changed";
  return error;
}

export function clientPreferences(request, context, storage) {
  if (!["get", "set"].includes(request.operation)) throw new Error("客户端偏好操作须为 get 或 set。");
  const keys = new Map(agentClientPreferences.map(item => [item.key, item]));
  const path = (key) => keys.get(key).scoped ? clientStorageKey(key, context) : key;
  if (request.operation === "set") {
    if (!object(request.values) || !Object.keys(request.values).length) throw new Error("请提供要修改的客户端偏好 values。");
    // Validate the whole patch before changing any storage entry.
    for (const [key, raw] of Object.entries(request.values)) {
      if (!keys.has(key)) throw new Error("客户端偏好包含不支持的键。外观请通过 appearance 接口修改。");
      if (keys.get(key).remoteOnly && context.mode !== "remote") throw new Error("本机键盘习惯请通过 settings.keyboard 修改。");
      if (!validPreference(key, raw)) throw new Error("客户端偏好格式无效，请保留对应设置使用的原始字符串格式。");
    }
    const previous = new Map(Object.keys(request.values).map(key => [path(key), storage.getItem(path(key))]));
    try {
      for (const [key, raw] of Object.entries(request.values)) {
        if (raw === null) storage.removeItem(path(key));
        else storage.setItem(path(key), raw);
      }
    } catch (error) {
      for (const [key, raw] of previous) {
        try { if (raw === null) storage.removeItem(key); else storage.setItem(key, raw); } catch { /* Keep the original storage failure. */ }
      }
      throw error;
    }
  }
  const available = agentClientPreferences.filter(item => !item.remoteOnly || context.mode === "remote");
  return {
    connection_id: context.connection_id,
    values: Object.fromEntries(available.map(item => [item.key, storage.getItem(path(item.key))])),
    storage_keys: Object.fromEntries(available.map(item => [item.key, path(item.key)])),
    fields: available,
    reload_required: request.operation === "set",
  };
}

export async function executeAgentNative(item, { native, getContext, storage }) {
  const context = getContext();
  if (!item.connection_id || item.connection_id !== context.connection_id) throw connectionChanged();
  const request = item.request;
  if (!object(request) || typeof request.action !== "string") throw new Error("原生请求格式无效。");
  let result;
  if (request.action === "clientPreferences") result = clientPreferences(request, context, storage);
  else {
    const { action, connection_id: ignoredConnection, ...extra } = request;
    void ignoredConnection;
    result = await native(action, extra);
    // Native session tickets belong to the transport. An agent can request a
    // refresh, but never receives the launch URL or its secret fragment.
    if (action === "refreshSession") result = { refreshed: true };
  }
  if (getContext().connection_id !== item.connection_id) throw connectionChanged();
  return result === undefined ? null : result;
}

export function createAgentNativeConsumer({ api, native, nativeAvailable, getContext, storage, reload, getOrigin = () => globalThis.location?.origin, schedule = setTimeout, cancel = clearTimeout, interval = 750, onError = () => {} }) {
  let stopped = true;
  let timer;
  let running = false;
  let completion;
  let proof;
  const registerWindow = async () => {
    const entry = await native("refreshSession");
    let url;
    try { url = new URL(entry?.url); } catch { throw new Error("无法验证原生窗口入口，请重新连接 Cage。"); }
    if (url.origin !== getOrigin() || !url.hash.slice(1)) throw new Error("原生窗口入口已改变；未把验证票据发送到其他地址，请重新连接 Cage。");
    const value = await api("/agent/native/window", { ticket: url.hash.slice(1) });
    if (typeof value?.window_proof !== "string" || !value.window_proof) throw new Error("原生窗口认证未完成，请重新连接 Cage。");
    proof = value.window_proof;
  };
  const poll = async () => {
    if (stopped || running) return;
    if (!nativeAvailable()) { timer = schedule(poll, interval); return; }
    running = true;
    try {
      if (!proof) await registerWindow();
      if (!completion) {
        if (stopped) return;
        const item = await api("/agent/native?consumer=window", undefined, undefined, { "X-Cage-Agent-Window": proof });
        if (item?.id) {
          if (stopped) {
            completion = { id: item.id, body: { error: "桌面窗口已关闭，原生操作未执行。" } };
          } else {
            try {
              const result = await executeAgentNative(item, { native, getContext, storage });
              completion = { id: item.id, body: { result }, reload: result?.reload_required === true };
            } catch (error) {
              completion = { id: item.id, body: { error: error?.message || "原生操作未完成，请核对当前窗口。" } };
            }
          }
        }
      }
      if (completion) {
        const pending = completion;
        await api(`/agent/native/${encodeURIComponent(pending.id)}/complete`, pending.body, undefined, { "X-Cage-Agent-Window": proof });
        completion = undefined;
        if (pending.reload && !stopped) { stopped = true; reload(); }
      }
    } catch (error) {
      // Only a completion receipt is retried. An accepted native action is
      // never re-executed after a lost connection or lost response.
      if (error?.status === 401 || error?.status === 403) proof = undefined;
      onError(error);
    } finally {
      running = false;
      if (!stopped) timer = schedule(poll, interval);
    }
  };
  return {
    start() { if (!stopped) return; stopped = false; void poll(); },
    stop() { stopped = true; if (timer !== undefined) cancel(timer); },
  };
}

let installedConsumer;
export function installAgentNativeConsumer(dependencies, environment = globalThis) {
  if (installedConsumer || !environment.document || !environment.window) return;
  const consumer = createAgentNativeConsumer({
    ...dependencies,
    storage: {
      getItem: key => environment.localStorage.getItem(key),
      setItem: (key, value) => environment.localStorage.setItem(key, value),
      removeItem: key => environment.localStorage.removeItem(key),
    },
    reload: () => environment.location.reload(),
    getOrigin: () => environment.location.origin,
  });
  installedConsumer = consumer;
  const start = () => { if (dependencies.nativeAvailable()) consumer.start(); };
  environment.window.addEventListener("cage-session-ready", start);
  environment.window.addEventListener("pagehide", () => consumer.stop());
  environment.window.addEventListener("pageshow", start);
  // A fresh launch URL must first be exchanged by ClientRoot/AccountProvider.
  // Existing windows without a fragment already hold their session cookie.
  if (!environment.location?.hash) start();
}
