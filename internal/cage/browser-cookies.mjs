import { createHash } from "node:crypto";

const key = cookie => createHash("sha256").update(JSON.stringify([
  cookie.name, cookie.domain, cookie.path, cookie.partitionKey || null, Boolean(cookie.partitionKeyOpaque),
])).digest("hex");
const forHost = (cookie, hostname) => cookie.domain.startsWith(".")
  ? hostname === cookie.domain.slice(1) || hostname.endsWith(cookie.domain)
  : hostname === cookie.domain;

export async function browserCookies(cdp, session, tab, input) {
  if (!tab || tab.login) throw new Error("当前标签不可管理 Cookie");
  if (input.url !== undefined && input.url !== tab.url) throw new Error("网页已切换，请重新打开 Cookie 管理");
  const { mode = "list", scope = "site", query = "", page = 0 } = input;
  if (!["list", "details", "delete", "clear"].includes(mode) || !["site", "all"].includes(scope) ||
      typeof query !== "string" || query.length > 200 || !Number.isInteger(page) || page < 0)
    throw new Error("Cookie 操作无效");
  let hostname = "";
  if (scope === "site") {
    const url = new URL(tab.url);
    if (!["http:", "https:"].includes(url.protocol)) throw new Error("当前页面没有网站 Cookie，请选择所有网站");
    hostname = url.hostname;
  }
  const context = tab.browserContextId ? { browserContextId: tab.browserContextId } : {};
  const read = async () => {
    const { cookies } = await cdp("Storage.getCookies", context);
    return cookies.filter(cookie => !hostname || forHost(cookie, hostname));
  };
  let cookies = await read();
  const remove = async cookie => {
    // Opaque partition keys cannot be addressed exactly by Network.deleteCookies.
    if (cookie.partitionKeyOpaque) throw new Error("此 Cookie 的分区不可单独定位，请使用清空所有网站 Cookie");
    await cdp("Network.deleteCookies", {
      name: cookie.name, domain: cookie.domain, path: cookie.path,
      ...(cookie.partitionKey ? { partitionKey: cookie.partitionKey } : {}),
    }, await session(tab.targetId));
  };
  if (mode === "delete" || mode === "details") {
    const cookie = cookies.find(cookie => key(cookie) === input.cookieKey);
    if (!cookie) throw new Error("Cookie 已变化，请刷新列表");
    if (mode === "details") return { value: cookie.value };
    await remove(cookie);
    cookies = await read();
  } else if (mode === "clear") {
    if (scope === "all") await cdp("Storage.clearCookies", context);
    else {
      if (cookies.some(cookie => cookie.partitionKeyOpaque)) throw new Error("此网站含不可单独定位的分区 Cookie，请使用清空所有网站 Cookie");
      for (const cookie of cookies) await remove(cookie);
    }
    cookies = await read();
  }
  const search = query.toLowerCase();
  cookies = cookies.filter(cookie => `${cookie.domain}\n${cookie.name}`.toLowerCase().includes(search))
    .sort((a, b) => a.domain.localeCompare(b.domain) || a.name.localeCompare(b.name) || a.path.localeCompare(b.path));
  const current = Math.min(page, Math.max(0, Math.ceil(cookies.length / 50) - 1));
  return { total: cookies.length, page: current, hostname, incognito: Boolean(tab.incognito),
    cookies: cookies.slice(current * 50, (current + 1) * 50).map(({ value, ...cookie }) => ({
      key: key(cookie), name: cookie.name, domain: cookie.domain, path: cookie.path,
      expires: cookie.expires, session: cookie.session, secure: cookie.secure, httpOnly: cookie.httpOnly,
      sameSite: cookie.sameSite || "", partitionKey: cookie.partitionKey,
      partitionKeyOpaque: Boolean(cookie.partitionKeyOpaque), size: cookie.size,
    })),
  };
}
