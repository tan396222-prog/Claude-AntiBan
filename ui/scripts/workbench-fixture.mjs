// Isolated browser fixture. No request from this page reaches the running Cage service.
import { createRequire } from "node:module";
import { defaultAppearance, presets } from "../src/appearance-state.mjs";
const require = createRequire(import.meta.url);
export async function createWorkbench({
  viewport = { width: 1280, height: 850 },
  recordVideo,
  favoritesRequest,
  mockWebSockets = true,
  url,
  nativeRequest,
  clientRequest,
  startupFailure = false,
  clientContext = { mode: "local", connection_id: "local", client_platform: "darwin", server_platform: "darwin", connection: { state: "disconnected" } },
} = {}) {
  const engines = require(
    process.env.CAGE_PLAYWRIGHT_MODULE || "playwright",
  );
  const browser = await engines[process.env.CAGE_TEST_BROWSER || "chromium"].launch({ headless: true, executablePath: process.env.CAGE_TEST_BROWSER_PATH });
  const context = await browser.newContext({ viewport, recordVideo });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const state = {
    appearance: structuredClone(defaultAppearance),
    codexAppearance: {
      ...structuredClone(defaultAppearance),
      uiFontSize: 16,
      codeFontSize: 13,
      lightTheme: {
        ...structuredClone(defaultAppearance.lightTheme),
        ...presets.light.solarized,
        preset: "solarized",
      },
      darkTheme: {
        ...structuredClone(defaultAppearance.darkTheme),
        ...presets.dark.ayu,
        preset: "ayu",
      },
    },
    failAppearance: false,
    chosenFolder: "/Users/shihao/Projects/design-lab",
    failSave: false,
    failJob: false,
    hostGit: {
      state: "ok",
      ready: true,
      can_enable: false,
      message: "本机 Git 安全保护已启用。",
    },
    failHostGit: false,
    saves: [],
    requests: [],
    terminalConnections: 0,
    account: { exists: true, unlocked: true, username: "林间工作室", avatar: "" },
    accountPassword: "fixture-password",
    failAccountSave: false,
    settings: {
      workspace: "/Users/shihao/.claude-cage-workspace",
      browser_home_url: "https://www.bing.com/",
      dns_url: "https://cloudflare-dns.com/dns-query",
      timezone: "Asia/Shanghai",
      locale: "zh_CN.UTF-8",
      browser_language: "zh-CN",
      auto_timezone: true,
      auto_language: false,
      keyboard: "mac",
      exit_policy: { mode: "dynamic", scope: "country", country_code: "US" },
    },
    status: {
      environment_directory: "/Users/shihao/.claude-cage-workspace",
      environment_workspace: true,
      projects: ["/Users/shihao/.claude-cage-workspace"],
      ready: false,
      vm_running: false,
      keyboard: "mac",
      job: { running: false },
      can_restore: true,
    },
    tabs: [],
    terminals: [],
    active: "",
    sequence: 0,
  };
  await page.exposeFunction("chooseTestFolder", () => state.chosenFolder);
  await page.exposeFunction("requestNativeForTest", async (body) => nativeRequest ? nativeRequest(body) : null);
  await page.addInitScript(() => {
    window.webkit = {
      messageHandlers: {
        cage: {
          postMessage: async (body) => {
            const { action } = body;
            if (action === "chooseFolder") return window.chooseTestFolder();
            if (action === "clipboardRead") return "fixture clipboard";
            return window.requestNativeForTest(body);
          },
        },
      },
    };
  });
  if (mockWebSockets) await page.routeWebSocket("**/api/**", (socket) => {
    if (socket.url().includes("/terminals/")) {
      state.terminalConnections++;
      socket.send(
        Buffer.from(
          "\r\n\u001b[1mCage\u001b[0m  ·  Linux terminal\r\n\r\n~/Projects/design-lab  $ ",
        ),
      );
    } else socket.close();
  });
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api", "");
    const body = request.postDataJSON();
    state.requests.push({ path, method: request.method(), body: path.startsWith("/account") ? undefined : body });
    if (clientRequest && path.startsWith("/client/")) return clientRequest(route, path, body);
    if (path === "/client/context") return route.fulfill({ json: clientContext });
    const accountView = () => ({ ...state.account, background_running: state.status.vm_running });
    if (path === "/account") return route.fulfill({ json: accountView() });
    if (path.startsWith("/account/")) {
      const action = path.split("/").at(-1);
      if (!["create", "unlock"].includes(action) && !state.account.unlocked) return route.fulfill({ status: 423, body: "请先解锁 Cage" });
      if (action === "create") {
        if (state.account.exists || !body.username?.trim() || body.password?.length < 8 || body.password !== body.confirm_password) return route.fulfill({ status: 400, body: "账户信息无效" });
        Object.assign(state.account, { exists: true, unlocked: true, username: body.username.trim(), avatar: body.avatar || "" });
        state.accountPassword = body.password;
      } else if (action === "unlock") {
        if (body.password !== state.accountPassword) return route.fulfill({ status: 400, body: "密码不正确" });
        state.account.unlocked = true;
        state.account.require_authentication = false;
      } else if (action === "profile") {
        if (state.failAccountSave) return route.fulfill({ status: 400, body: "资料未能保存，请重试" });
        Object.assign(state.account, { username: body.username.trim(), avatar: body.avatar });
      } else if (action === "password") {
        if (body.old_password !== state.accountPassword) return route.fulfill({ status: 400, body: "密码不正确" });
        if (body.password?.length < 8 || body.password !== body.confirm_password) return route.fulfill({ status: 400, body: "新密码无效" });
        state.accountPassword = body.password;
      } else if (action === "lock") { state.account.unlocked = false; state.account.require_authentication = true; }
      else if (action === "suspend") state.account.unlocked = false;
      else if (action === "logout") {
        Object.assign(state.status, { ready: false, vm_running: false });
        state.account.unlocked = false;
        state.account.devices = {};
        state.account.require_authentication = true;
        state.terminals = [];
        state.status.job = { id: "account-logout", kind: "stop", running: false };
        return route.fulfill({ json: state.status.job });
      }
      return route.fulfill({ json: accountView() });
    }
    if (!state.account.unlocked && !["/session", "/launch", "/appearance"].includes(path)) return route.fulfill({ status: 423, body: "Cage 已锁定，请先登录" });
    if (startupFailure && path === "/status") return route.fulfill({ status: 503, body: "测试：控制服务暂不可用" });
    let result = {};
    if (path === "/favorites") {
      if (favoritesRequest) return route.fulfill(await favoritesRequest(request));
      return route.fulfill({ json: { items: [], folders: [] } });
    }
    if (path === "/host/git") {
      if (request.method() === "POST") {
        if (!body?.confirm)
          return route.fulfill({ status: 400, body: "请先确认启用保护" });
        if (state.failHostGit)
          return route.fulfill({
            status: 500,
            body: "未能保存 Git 安全设置，请检查写入权限后重试。",
          });
        state.hostGit = {
          state: "ok",
          ready: true,
          can_enable: false,
          message: "本机 Git 安全保护已启用。",
        };
      }
      return route.fulfill({ json: state.hostGit });
    }
    if (path === "/appearance/codex")
      return route.fulfill({ json: state.codexAppearance });
    if (path === "/appearance") {
      if (request.method() === "POST") {
        if (state.failAppearance)
          return route.fulfill({
            status: 500,
            body: "外观设置未能保存，请重试",
          });
        state.appearance = structuredClone(body);
      }
      return route.fulfill({ json: state.appearance });
    }
    if (path === "/proxy")
      result = {
        active: true,
        selected: "fixture-node",
        sources: [
          {
            id: "fixture-source",
            name: "测试来源",
            nodes: [{ id: "fixture-node", name: "测试节点", type: "http" }],
            remote: false,
            interval_hours: 0,
          },
        ],
        port: 17896,
        core_available: true,
        running: true,
        state: state.status.vm_running ? "ready" : "prepared",
        state_label: state.status.vm_running ? "已联网" : "待启动环境",
        network_open: Boolean(state.status.vm_running),
      };
    else if (path === "/settings" && request.method() === "GET")
      result = state.settings;
    else if (path === "/status")
      result = { ...state.status, workspace: state.settings.workspace };
    else if (path === "/projects/open") {
      if (state.failJob) return route.fulfill({ status: 400, body: "项目文件夹连接失败，原项目保持不变" });
      if (!body.add && !state.status.projects.includes(body.workspace)) return route.fulfill({ status: 400, body: "请选择已授权的项目" });
      if (body.add && !state.status.projects.includes(body.workspace)) state.status.projects.push(body.workspace);
      if (body.activate) {
        state.settings.workspace = body.workspace;
        state.status.environment_workspace = body.workspace === state.status.environment_directory;
      }
      result = { workspace: body.workspace };
    }
    else if (path === "/settings") {
      state.saves.push(body);
      if (state.failSave)
        return route.fulfill({
          status: 400,
          json: { error: "保存失败，请重试" },
        });
      const id = "job-" + ++state.sequence;
      const switching = state.settings.workspace !== body.workspace;
      if (!state.failJob) {
        Object.assign(state.settings, body);
        state.status.keyboard = body.keyboard;
        if (switching) {
          if (!state.status.projects.includes(body.workspace))
            state.status.projects.push(body.workspace);
          state.status.environment_workspace = body.workspace.endsWith(
            "/.claude-cage-workspace",
          );
        }
      }
      state.status.job = {
        id,
        kind: switching ? "project" : "settings",
        running: false,
        error: state.failJob ? "项目文件夹连接失败，原项目保持不变" : "",
      };
      result = { id };
    } else if (path === "/start") {
      state.status.job = { id: "start", kind: "start", running: false };
      state.settings.workspace = "/Users/shihao/.claude-cage-workspace";
      state.status.environment_workspace = true;
      state.status.ready = true;
      state.status.vm_running = true;
      state.status.safety = {
        gate: {
          mode: "open",
          until: new Date(Date.now() + 3600000).toISOString(),
        },
      };
      result = { id: "start" };
    } else if (path === "/stop") {
      state.status.ready = false;
      state.status.vm_running = false;
    } else if (path === "/browser/tabs")
      result = { tabs: state.tabs, active: state.active, split: state.split || null };
    else if (path === "/browser/split") {
      state.split = body.left ? { left: body.left, right: body.right, fraction: 0.5 } : null;
      result = { split: state.split };
    }
    else if (path === "/browser/events")
      return route.fulfill({
        contentType: "text/event-stream",
        body: ": fixture\n\n",
      });
    else if (path === "/vnc-credentials") result = { password: "fixture-only" };
    else if (path === "/terminals" && request.method() === "GET")
      result = state.terminals;
    else if (path === "/terminals") {
      if (
        !state.status.projects.includes(
          body?.workspace || state.settings.workspace,
        )
      )
        return route.fulfill({
          status: 400,
          body: "请先通过打开项目添加该文件夹",
        });
      const tab = {
        id: "term-" + ++state.sequence,
        title: "终端 " + (state.terminals.length + 1),
        workspace: body?.workspace || state.settings.workspace,
      };
      state.terminals.push(tab);
      result = tab;
    } else if (
      path.startsWith("/terminals/") &&
      request.method() === "DELETE"
    ) {
      state.terminals = state.terminals.filter(
        (tab) => tab.id !== path.split("/").at(-1),
      );
    } else if (path === "/browser/new" || path === "/browser/editor") {
      const workspace = body?.workspace || state.settings.workspace;
      if (path.endsWith("editor") && !state.status.projects.includes(workspace))
        return route.fulfill({
          status: 400,
          body: "请先通过打开项目添加该文件夹",
        });
      let tab =
        path.endsWith("editor") &&
        state.tabs.find(
          (tab) => tab.kind === "editor" && tab.workspace === workspace,
        );
      if (!tab) {
        tab = {
          id: "web-" + ++state.sequence,
          kind: path.endsWith("editor") ? "editor" : "web",
          workspace: path.endsWith("editor") ? workspace : undefined,
          title: path.endsWith("editor") ? "文件编辑器" : "新标签页",
          url: body?.url || state.settings.browser_home_url,
        };
        state.tabs.push(tab);
      }
      state.active = tab.id;
      result = tab;
    } else if (path === "/browser/activate") state.active = body.id;
    else if (path === "/browser/close") {
      state.tabs = state.tabs.filter((tab) => tab.id !== body.id);
      if (state.active === body.id) state.active = state.tabs.at(-1)?.id || "";
    } else if (path === "/browser/navigate")
      Object.assign(
        state.tabs.find((tab) => tab.id === body.id),
        { url: body.url, title: "本地预览" },
      );
    else if (path === "/settings/probe")
      result = {
        country: "United States",
        city: "Los Angeles",
        ip: "192.0.2.10",
        timezone: "America/Los_Angeles",
        checked_at: new Date().toISOString(),
      };
    return route.fulfill({ json: result });
  });
  await page.goto(url || process.env.CAGE_UI_URL || "http://127.0.0.1:5198");
  if (!startupFailure && clientContext.mode !== "none") await page.getByRole("heading", { name: "先启动 Cage 环境" }).waitFor();
  return { browser, context, page, state, errors };
}

export async function chooseInline(picker, value) {
  await picker.click();
  await picker.locator("..").getByRole("option").and(picker.page().locator(`[data-value=${JSON.stringify(String(value))}]`)).click();
}
