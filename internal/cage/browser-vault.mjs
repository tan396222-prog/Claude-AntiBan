import { randomUUID } from "node:crypto";

const world = "cage-password-vault";
const binding = "cageVaultSubmitted";

// This function executes in an isolated world. Page scripts cannot invoke its
// binding or replace its built-ins. Only the top document participates.
export function vaultPage() {
  if (window !== window.top || globalThis.cageVault) return;
  const visible = el => el instanceof HTMLInputElement && !el.disabled && !el.readOnly &&
    el.type !== "hidden" && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const fields = root => [...root.querySelectorAll("input")].filter(visible);
  const username = list => list.find(el => el.autocomplete === "username") ||
    list.find(el => el.type === "email") || list.find(el =>
      ["text", "tel"].includes(el.type) && !/one-time-code|otp|search|captcha/i.test(`${el.autocomplete} ${el.name} ${el.id}`));
  const state = globalThis.cageVault = {
    document: crypto.randomUUID(), deadline: 0, lease: "", previousUsername: "", previousAt: 0,
    fill(value) {
      if (Date.now() >= state.deadline || value.lease !== state.lease || location.origin !== value.origin ||
          location.protocol !== "https:" || value.document !== state.document) return false;
      const list = fields(document);
      const passwords = list.filter(el => el.type === "password" && el.autocomplete !== "new-password");
      // Multiple independent forms require the user to focus the intended one.
      let password = passwords.includes(document.activeElement) ? document.activeElement : null;
      if (!password && document.activeElement?.form) password = passwords.find(el => el.form === document.activeElement.form);
      if (!password && passwords.length === 1) password = passwords[0];
      if (!password && passwords.length > 1) return false;
      const user = username(password?.form ? fields(password.form) : list);
      if (!password && !user) return false;
      const set = (el, text) => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(el, text);
        el.dispatchEvent(new Event("input", { bubbles: true }));
        el.dispatchEvent(new Event("change", { bubbles: true }));
      };
      if (user) set(user, value.username);
      if (password) set(password, value.password);
      state.previousUsername = value.username;
      state.previousAt = Date.now();
      (password || user).focus();
      return true;
    },
  };
  let last = "", lastAt = 0;
  function capture(root) {
    if (location.protocol !== "https:" || Date.now() >= state.deadline) return;
    const list = fields(root || document), user = username(list);
    if (user?.value) { state.previousUsername = user.value; state.previousAt = Date.now(); }
    const passwords = list.filter(el => el.type === "password" && el.value);
    const password = passwords.at(-1)?.value || "";
    if (!password && !user?.value) return;
    const account = user?.value || (Date.now() - state.previousAt < 120000 ? state.previousUsername : "");
    if (password.length > 8192 || account.length > 1024) return;
    const payload = JSON.stringify({ origin: location.origin, username: account, password,
      document: state.document, lease: state.lease });
    // Avoid duplicate submit/click notifications without retaining a password.
    const signature = `${state.document}:${account}:${password.length}`;
    if (signature === last && Date.now() - lastAt < 800) return;
    last = signature; lastAt = Date.now();
    globalThis.cageVaultSubmitted(payload);
  }
  document.addEventListener("submit", event => { if (event.isTrusted) capture(event.target); }, true);
  document.addEventListener("click", event => {
    if (!event.isTrusted) return;
    const button = event.target.closest?.("button, input[type=submit], [role=button]");
    if (!button) return;
    const label = `${button.textContent || ""} ${button.value || ""} ${button.getAttribute("aria-label") || ""}`;
    if (button.type === "submit" || /log\s*in|sign\s*in|登录|登入|下一步|继续|next|continue|修改密码|保存密码|change password/i.test(label)) capture(button.form || document);
  }, true);
  document.addEventListener("keydown", event => {
    if (event.isTrusted && event.key === "Enter" && event.target instanceof HTMLInputElement)
      capture(event.target.form || document);
  }, true);
}
const source = `(${vaultPage.toString()})()`;

export class BrowserVault {
  constructor({ cdp, active, tab, now = () => Date.now() }) {
    Object.assign(this, { cdp, active, tab, now });
    this.sessions = new Map();
    this.pending = new Map();
    this.usernames = new Map();
    this.lease = "";
    this.deadline = 0;
    this.blocked = [];
    this.timer = setInterval(() => this.prune(), 1000);
    this.timer.unref();
  }
  prune() {
    if (this.now() >= this.deadline) { this.pending.clear(); this.usernames.clear(); this.lease = ""; }
    for (const [id, item] of this.usernames) if (this.now() >= item.expires) this.usernames.delete(id);
    for (const [id, item] of this.pending) if (this.now() >= item.expires) this.pending.delete(id);
  }
  async attach(tab, sid) {
    if (this.tab(tab)?.login) return;
    const entry = { tab, sid, top: "", contexts: new Map() };
    this.sessions.set(sid, entry);
    await this.cdp("Runtime.enable", {}, sid);
    const { frameTree } = await this.cdp("Page.getFrameTree", {}, sid);
    entry.top = frameTree.frame.id;
    await this.cdp("Runtime.addBinding", { name: binding, executionContextName: world }, sid);
    await this.cdp("Page.addScriptToEvaluateOnNewDocument", { source, worldName: world }, sid);
    const { executionContextId } = await this.cdp("Page.createIsolatedWorld", { frameId: entry.top, worldName: world }, sid);
    entry.contexts.set(executionContextId, entry.top);
    await this.cdp("Runtime.evaluate", { expression: source, contextId: executionContextId }, sid);
  }
  remove(tab) {
    this.usernames.delete(tab);
    for (const [sid, value] of this.sessions) if (value.tab === tab) this.sessions.delete(sid);
    for (const [id, value] of this.pending) if (value.tab === tab) this.pending.delete(id);
  }
  event(event) {
    const entry = this.sessions.get(event.sessionId);
    if (!entry) return;
    const p = event.params || {};
    if (event.method === "Runtime.executionContextCreated" && p.context.name === world)
      entry.contexts.set(p.context.id, p.context.auxData?.frameId);
    if (event.method === "Runtime.executionContextDestroyed") entry.contexts.delete(p.executionContextId);
    if (event.method === "Runtime.executionContextsCleared") entry.contexts.clear();
    if (event.method === "Page.frameNavigated" && !p.frame.parentId) entry.top = p.frame.id;
    if (event.method !== "Runtime.bindingCalled" || p.name !== binding ||
        entry.contexts.get(p.executionContextId) !== entry.top) return;
    this.prune();
    if (!this.lease || entry.tab !== this.active() || this.tab(entry.tab)?.login || p.payload.length > 16000) return;
    try {
      const value = JSON.parse(p.payload);
      const origin = new URL(value.origin).origin;
      if (!origin.startsWith("https://") || origin !== value.origin || value.lease !== this.lease ||
          typeof value.password !== "string" || value.password.length > 8192 ||
          typeof value.username !== "string" || value.username.length > 1024 || this.blocked.includes(origin)) return;
      if (value.username) this.usernames.set(entry.tab, { origin, username: value.username, expires: this.now() + 120000 });
      if (!value.password) return;
      const previous = this.usernames.get(entry.tab);
      if (!value.username && previous?.origin === origin) value.username = previous.username;
      // The context's security origin is additionally checked inside the page;
      // the binding is available only to our isolated-world code.
      for (const [id, old] of this.pending) if (old.tab === entry.tab) this.pending.delete(id);
      const id = randomUUID();
      this.pending.set(id, { id, tab: entry.tab, origin, username: value.username,
        password: value.password, expires: this.now() + 120000 });
      while (this.pending.size > 8) this.pending.delete(this.pending.keys().next().value);
    } catch {}
  }
  context(entry) {
    return [...entry.contexts].find(([, frame]) => frame === entry.top)?.[0];
  }
  async evaluate(entry, expression) {
    const contextId = this.context(entry);
    if (!contextId) throw new Error("登录页面已变化，请稍后重试");
    const result = await this.cdp("Runtime.evaluate", { expression, contextId, returnByValue: true }, entry.sid);
    if (result.exceptionDetails) throw new Error("网页未能处理密码操作");
    return result.result?.value;
  }
  async request(input) {
    this.prune();
    if (input.action === "lock") {
      if (input.lease === this.lease) { this.pending.clear(); this.usernames.clear(); this.lease = ""; this.deadline = 0; }
      return { ok: true };
    }
    if (input.action === "status") {
      if (!/^[a-f0-9]{64}$/.test(input.lease)) throw new Error("解锁状态无效");
      if (this.lease !== input.lease) { this.pending.clear(); this.usernames.clear(); }
      this.lease = input.lease;
      this.deadline = this.now() + 6500;
      this.blocked = Array.isArray(input.blocked) ? input.blocked : [];
      for (const [id, value] of this.pending) if (this.blocked.includes(value.origin)) this.pending.delete(id);
      const result = { available: true, origin: "", document: "", candidates: [], entries: [] };
      await Promise.all([...this.sessions.values()].map(async entry => {
        try {
          const value = await this.evaluate(entry, `(() => {
            const v = globalThis.cageVault;
            if (!v || location.protocol !== 'https:') return null;
            if (v.lease !== ${JSON.stringify(this.lease)}) { v.previousUsername = ""; v.previousAt = 0; }
            v.lease = ${JSON.stringify(this.lease)}; v.deadline = Date.now() + 6500;
            return {origin: location.origin, document: v.document};
          })()`);
          if (value && entry.tab === input.tab && entry.tab === this.active() && !this.tab(entry.tab)?.login)
            Object.assign(result, value);
        } catch {}
      }));
      result.candidates = [...this.pending.values()].map(({ expires, ...value }) => value);
      return result;
    }
    if (!this.lease || input.lease !== this.lease) throw new Error("保险箱已锁定，请重试");
    if (input.action === "candidate") {
      const value = this.pending.get(input.candidate);
      if (!value) throw new Error("登录记录已过期，请重新登录");
      return value;
    }
    if (input.action === "dismiss") { this.pending.delete(input.candidate); return { ok: true }; }
    if (input.action === "fill") {
      const entry = [...this.sessions.values()].find(value => value.tab === input.tab);
      if (!entry || input.tab !== this.active() || this.tab(input.tab)?.login) throw new Error("当前网页已切换");
      const filled = await this.evaluate(entry, `globalThis.cageVault?.fill(${JSON.stringify(input)}) === true`);
      if (filled) this.usernames.set(entry.tab, { origin: input.origin, username: input.username, expires: this.now() + 120000 });
      if (!filled) throw new Error("未找到可填写的登录框，或页面已变化");
      return { ok: true };
    }
    throw new Error("密码操作无效");
  }
}
