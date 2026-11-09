// A hidden, disposable context avoids touching account tabs, browser cookies,
// focus or the visible tab strip while reading Chromium's actual settings.
export async function auditBrowser(
  cdp,
  auditContexts,
  network,
  expectedTimezone,
) {
  const { browserContextId } = await cdp("Target.createBrowserContext", {
    disposeOnDetach: true,
  });
  auditContexts.add(browserContextId);
  let targetId;
  try {
    ({ targetId } = await cdp("Target.createTarget", {
      url: "about:blank",
      browserContextId,
      background: true,
      hidden: true,
    }));
    const { sessionId } = await cdp("Target.attachToTarget", {
      targetId,
      flatten: true,
    });
    if (network) {
      const navigation = await cdp(
        "Page.navigate",
        { url: "http://198.18.0.254/" },
        sessionId,
        15000,
      );
      if (navigation.errorText) throw new Error("诊断页面无法连接");
      let loaded = false;
      const deadline = Date.now() + 3000;
      while (Date.now() < deadline) {
        try {
          const state = await cdp(
            "Runtime.evaluate",
            {
              expression:
                "location.hostname === '198.18.0.254' && document.readyState === 'complete'",
              returnByValue: true,
            },
            sessionId,
            500,
          );
          if (state.result?.value === true) {
            loaded = true;
            break;
          }
        } catch {
          /* A navigation may replace the initial execution context. */
        }
        await new Promise((resolve) => setTimeout(resolve, 25));
      }
      if (!loaded) throw new Error("诊断页面尚未就绪");
    }
    const expression = `(async () => {
      const result = {timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        expected_timezone: new Intl.DateTimeFormat('en', {timeZone:${JSON.stringify(expectedTimezone)}}).resolvedOptions().timeZone,
        language: navigator.language, languages: [...navigator.languages], platform: navigator.platform};
      if (${network ? "true" : "false"}) {
        if (location.hostname !== '198.18.0.254') throw new Error('diagnostic navigation incomplete');
        try {
          const response = await fetch('/egress', {cache:'no-store', credentials:'omit', signal:AbortSignal.timeout(14000)});
          result.network = {status:response.status, ...await response.json()};
        } catch { result.network = {status:0, error:'probe_unavailable'}; }
      }
      return result;
    })()`;
    const response = await cdp(
      "Runtime.evaluate",
      { expression, awaitPromise: true, returnByValue: true },
      sessionId,
      16000,
    );
    if (response.exceptionDetails || !response.result?.value)
      throw new Error("浏览器诊断未完成");
    return response.result.value;
  } finally {
    try {
      // Hidden targets are owned by the CDP connection, not the context's tab
      // windows. Chromium 156 crashes after its 60s disposal timeout if a
      // hidden page still references the context. Close it before disposal.
      if (targetId) await cdp("Target.closeTarget", { targetId });
      await cdp("Target.disposeBrowserContext", { browserContextId });
      auditContexts.delete(browserContextId);
    } catch {
      // If closing the page fails, leave its context alive and excluded from
      // user tabs. Disposing it anyway would recreate the browser crash.
    }
  }
}
