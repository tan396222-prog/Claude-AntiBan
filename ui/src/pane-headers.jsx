import React, { useEffect, useState } from "react";
import { ArrowLeft, ArrowRight, ArrowUpRight, ChevronRight, Copy, FileCode2, FolderOpen,
  Globe2, Maximize2, MoreHorizontal, PanelLeftOpen, Plus, RefreshCw, TerminalSquare, X } from "lucide-react";
import { BrowserVaultBar } from "./password-vault";
import { WorkspaceTabs } from "./workspace-tabs";
import { BrowserFavorites } from "./browser-favorites";
import { BrowserDownloads } from "./browser-downloads";
import { tabName } from "./workspace-state.mjs";
import { shortcut } from "./platform.mjs";
import logo from "./logo.png";

export const ExpandSidebar = ({ onClick }) => <button className="icon-button sidebar-expand"
  aria-label="展开侧栏" onClick={onClick}>
  <img className="sidebar-expand-logo" src={logo} alt="" />
  <PanelLeftOpen className="sidebar-expand-icon" size={18} />
</button>;

export const ExpandPane = ({ onClick }) => <button className="icon-button expand-pane"
  aria-label="退出分屏" title="在整个工作区显示此页" onClick={onClick}><Maximize2 size={14} /></button>;

export function TerminalHeader({ tab, contentWorkspace, project, metadata, terminalDetail, terminalDetailHint,
  onHome, ready, running, busy, onEditor, onNew, onClose, sidebarButton, exitButton, location, triggerRef, menu }) {
  return <header className="chrome terminal-chrome">
    <div className="content-heading">
      {sidebarButton}
      <div className="content-breadcrumb">
        <button
          onClick={onHome}
          title={contentWorkspace}
        >
          <FolderOpen size={15} />
          <span>{project}</span>
        </button>
        {tab && (
          <>
            <ChevronRight size={13} />
            <span className="current-title" title={tabName(tab, metadata, project)}>
              {tabName(tab, metadata, project)}
            </span>
            {terminalDetail && <span className="terminal-detail" title={terminalDetailHint}>{terminalDetail}</span>}
          </>
        )}
      </div>
      <div className="heading-actions">
        <button
          className="icon-button"
          aria-label="打开文件"
          title="打开文件"
          onClick={onEditor}
          disabled={!ready || busy}
        >
          <FileCode2 size={15} />
        </button>
        <button
          className="icon-button"
          aria-label="新建终端"
          aria-haspopup="menu"
          data-terminal-location={location}
          ref={triggerRef}
          title={shortcut("新建终端 ⌘⇧↵")}
          onClick={onNew}
          disabled={!running || busy}
        >
          <TerminalSquare size={17} />
        </button>
        {tab && (
          <button
            className="icon-button"
            aria-label="关闭当前标签"
            title={shortcut("关闭当前标签 ⌘W")}
            onClick={onClose}
            disabled={busy}
          >
            <X size={16} />
          </button>
        )}
        {exitButton}
      </div>
    </div>
    {menu}
    {busy && <div className="progress-line" />}
  </header>;
}

export function BrowserHeader({ vault, onManageVault, tab, tabs, metadata, ready, busy, focused, compact, triggerRef,
  sidebarButton, exitButton, splitDrag, onSelect, onClose, onRename, onPin, onReorder, onNew,
  location, menu, back, forward, action, onNavigate, onOpen, downloads, onStatus, onRemote, onEditor, onRecover, recoveringEditor }) {
  const [address, setAddress] = useState("");
  const [toolsOpen, setToolsOpen] = useState(false);
  const url = tab.url === "about:blank" ? "" : tab.url || "";
  useEffect(() => { setAddress(url); }, [tab.id, url]);
  return <header className={`chrome browser-chrome ${compact ? "split-chrome" : ""}`} data-browser-tab={tab.id}>
    <div className="browser-tab-bar">
      {sidebarButton}
      <WorkspaceTabs
        tabs={tabs}
        saved={metadata}
        active={tab.id}
        label="浏览器标签页"
        horizontal
        busy={busy}
        ready={ready}
        onSelect={onSelect}
        onClose={onClose}
        onRename={onRename}
        onPin={onPin}
        splitDrag={splitDrag}
        onReorder={onReorder}
      />
      <button
        className="icon-button browser-new-tab"
        ref={triggerRef}
        data-terminal-location={location}
        aria-label="新建网页"
        title={shortcut("新建网页 ⌘T")}
        disabled={!ready || busy}
        onClick={onNew}
      >
        <Plus size={17} />
      </button>
      {exitButton}
    </div>
    <div className="navigation-row">
      <div className="nav-actions">
        <button
          className="icon-button"
          aria-label="后退"
          disabled={!ready || !back}
          onClick={() =>
            action("/browser/back", { id: tab.id })
          }
        >
          <ArrowLeft size={17} />
        </button>
        <button
          className="icon-button"
          aria-label="前进"
          disabled={!ready || !forward}
          onClick={() =>
            action("/browser/forward", { id: tab.id })
          }
        >
          <ArrowRight size={17} />
        </button>
        <button
          className="icon-button"
          aria-label="重新加载网页"
          disabled={!ready}
          onClick={() =>
            action("/browser/reload", { id: tab.id })
          }
        >
          <RefreshCw size={15} />
        </button>
      </div>
      <form className="address-bar" onSubmit={event => { event.preventDefault(); onNavigate(address); }}>
        <Globe2 size={13} />
        <input
          aria-label="网页地址或搜索"
          value={address}
          onChange={(e) => setAddress(e.target.value)}
          onFocus={(e) => e.target.select()}
          placeholder={
            ready
              ? "输入网址或搜索，在 Cage 内打开"
              : "启动环境后浏览网页"
          }
          disabled={!ready}
          spellCheck="false"
        />
        <button
          className="address-go"
          type="submit"
          aria-label="打开地址"
          disabled={!ready}
        >
          <ArrowUpRight size={15} />
        </button>
      </form>
      {compact && <button className="icon-button" aria-label="更多网页工具" aria-expanded={toolsOpen}
        onClick={() => setToolsOpen(!toolsOpen)}><MoreHorizontal size={16} /></button>}
      <div className={`browser-tools ${toolsOpen ? "open" : ""}`}>
        <BrowserFavorites current={tab} shortcuts={focused} ready={ready && !busy} onOpen={onOpen} />
        <BrowserDownloads downloads={downloads} ready={ready && !busy} onStatus={onStatus} />
        {tab.kind === "editor" && <button className="icon-button" aria-label="恢复编辑器" title="单独重启编辑器"
          disabled={!ready || busy || recoveringEditor} onClick={onRecover}>
          <RefreshCw size={15} className={recoveringEditor ? "spin" : ""} />
        </button>}
        <div className="clipboard-actions">
          <button
            className="icon-button"
            aria-label="复制到本机"
            title="复制到本机"
            disabled={!ready}
            onClick={() => onRemote("copy")}
          >
            <Copy size={15} />
          </button>
          <button
            className="icon-button"
            aria-label="粘贴到 Cage"
            title="粘贴到 Cage"
            disabled={!ready}
            onClick={() => onRemote("paste")}
          >
            <svg
              width="15"
              height="15"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.7"
            >
              <path d="M9 5H5v16h14V5h-4M9 3h6v4H9zM8 12h8M8 16h5" />
            </svg>
          </button>
          <button
            className="icon-button"
            aria-label="文本传递"
            title="文本传递"
            disabled={!ready}
            onClick={() => onRemote("transfer")}
          >
            <MoreHorizontal size={17} />
          </button>
        </div>
        <button
          className="icon-button files-button"
          title="打开文件编辑器"
          aria-label="打开文件编辑器"
          disabled={!ready || busy}
          onClick={onEditor}
        >
          <FileCode2 size={17} />
        </button>
      </div>
    </div>
    {vault && tab.kind !== "editor" && <BrowserVaultBar tab={tab} vault={vault} ready={ready} onManage={onManageVault} />}
    {menu}
    {busy && <div className="progress-line" />}
  </header>;
}
