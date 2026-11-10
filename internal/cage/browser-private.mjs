// All user-created private tabs share one off-the-record Chromium context.
// Login and diagnostic contexts have independent owners.
export class PrivateBrowser {
  constructor(cdp, downloadPath) {
    this.cdp = cdp;
    this.downloadPath = downloadPath;
    this.context = "";
  }

  tab(raw) {
    return { ...raw, incognito: Boolean(this.context && raw.browserContextId === this.context) };
  }

  async open(url, activate) {
    await this.prune();
    if (!this.context) {
      const { browserContextId } = await this.cdp("Target.createBrowserContext", { disposeOnDetach: true });
      this.context = browserContextId;
    }
    try {
      await this.cdp("Browser.setDownloadBehavior", {
        browserContextId: this.context, behavior: "allowAndName",
        downloadPath: this.downloadPath, eventsEnabled: true,
      });
      const { targetId } = await this.cdp("Target.createTarget", {
        browserContextId: this.context, url, newWindow: true,
      });
      await activate(this.tab({ targetId, browserContextId: this.context, type: "page", url }));
      return { id: targetId, incognito: true };
    } catch (error) {
      await this.prune();
      throw error;
    }
  }

  async prune() {
    if (!this.context) return;
    const { targetInfos } = await this.cdp("Target.getTargets");
    if (targetInfos.some(tab => tab.type === "page" && tab.browserContextId === this.context)) return;
    await this.cdp("Target.disposeBrowserContext", { browserContextId: this.context });
    this.context = "";
  }
}
