import { shortcut } from "./platform.mjs";
import React, { useEffect, useState } from "react";
import {
  ExternalLink,
  ChevronDown,
  FolderOpen,
  Globe2,
  Home,
  Monitor,
  MessageCircle,
  PanelLeftClose,
  Plus,
  Settings2,
  Server,
  SquareTerminal,
} from "lucide-react";
import { EnvironmentShield } from "./environment-shield";
import logo from "./logo.png";
import { projectName } from "./workspace-state.mjs";
import { WorkspaceTabs } from "./workspace-tabs";
import { AccountMenu } from "./account";
import { useSSHNavigation } from "./remote-hosts";

export function WorkspaceSidebar({
  workspace,
  defaultWorkspace,
  projects,
  tabs,
  saved,
  active,
  home,
  busy,
  ready,
  running,
  runtimeLabel,
  network,
  onHome,
  onOpenProject,
  onProjectSelect,
  onSelect,
  onClose,
  onRename,
  onPin,
  onReorder,
  splitDrag,
  onNewTerminal,
  terminalMenuAt,
  onBrowser,
  downloadAction,
  onClaudeWeb,
  onClaudeDesktop,
  desktopOpen,
  onEditor,
  onSettings,
  onAccountConfirm,
  onAccountStatus,
  onSafety,
  onCollapse,
  updateAction,
}) {
  const ssh = useSSHNavigation();
  const [collapsed, setCollapsed] = useState({});
  const activeWorkspace = tabs.find((tab) => tab.id === active)?.workspace;
  useEffect(() => {
    if (activeWorkspace)
      setCollapsed((previous) => ({ ...previous, [activeWorkspace]: false }));
  }, [active, activeWorkspace]);
  if (ssh?.expanded) return null;
  const healthy = ready && network.mode === "open";
  return (
    <aside className="workspace-sidebar" aria-label="项目与终端">
      <div className="sidebar-top">
        <span className="sidebar-brand">
          <img className="brand-logo" src={logo} alt="" />
          Cage
        </span>
        <button
          className="icon-button"
          onClick={onCollapse}
          aria-label="收起侧栏"
          title="收起侧栏"
        >
          <PanelLeftClose size={17} />
        </button>
      </div>
      <nav className="sidebar-navigation" aria-label="主导航">
        <button
          className={`sidebar-link ${home ? "selected" : ""}`}
          onClick={onHome}
        >
          <Home size={17} />
          工作台
        </button>
        <button
          className={`sidebar-link ${!desktopOpen && !home && tabs.some((tab) => tab.id === active && tab.kind !== "terminal") ? "selected" : ""}`}
          onClick={onBrowser}
          disabled={!ready || busy}
          aria-label="打开浏览器"
          title="打开浏览器"
        >
          <Globe2 size={17} />
          浏览器
        </button>
        {ssh && <button className="sidebar-link ssh-navigation" onClick={ssh.open} aria-label="SSH 主机"><Server size={17} />SSH</button>}
        <div className="claude-shortcuts">
          <button className="sidebar-link" onClick={onClaudeWeb} disabled={!ready || busy} title="在 Cage 浏览器打开 claude.ai"><MessageCircle size={17} />claude.ai</button>
          <button className={`sidebar-link ${desktopOpen ? "selected" : ""}`} onClick={onClaudeDesktop} disabled={!running || busy} title="安装或打开 Claude Desktop"><Monitor size={17} />Claude Desktop</button>
        </div>
        {downloadAction}
      </nav>
      <div className="sidebar-scroll">
        <div className="sidebar-section-heading">
          <span>项目</span>
          <button
            className="icon-button"
            onClick={() => onOpenProject()}
            aria-label="添加项目"
            title="添加项目"
            disabled={busy || !running}
          >
            <Plus size={15} />
          </button>
        </div>
        {projects.map((path) => {
          const project = projectName(path, defaultWorkspace);
          const terminals = tabs.filter(
            (tab) => tab.kind === "terminal" && tab.workspace === path,
          );
          const expanded = !collapsed[path];
          return (
            <section
              className="sidebar-project"
              key={path}
              aria-label={`项目 ${project}`}
            >
              <div
                className={`project-heading ${workspace === path ? "current-project" : ""}`}
              >
                <button
                  className="project-disclosure"
                  title={path}
                  aria-expanded={expanded}
                  onClick={() => {
                    setCollapsed((previous) => ({
                      ...previous,
                      [path]: expanded,
                    }));
                    onProjectSelect(path);
                  }}
                >
                  <ChevronDown
                    size={13}
                    className={expanded ? "" : "collapsed"}
                  />
                  <FolderOpen size={16} />
                  <span>{project}</span>
                </button>
                <button
                  className="icon-button"
                  aria-label={`打开 ${project} 的编辑器`}
                  title="在 code-server 中打开项目目录"
                  disabled={!ready || busy}
                  onClick={() => onEditor(path)}
                >
                  <ExternalLink size={15} />
                </button>
                <button
                  className="icon-button"
                  aria-label={`在 ${project} 新建终端`}
                  title="新建终端"
                  aria-haspopup="menu"
                  data-terminal-location={path}
                  disabled={!running || busy}
                  onClick={(event) => {
                    setCollapsed((previous) => ({
                      ...previous,
                      [path]: false,
                    }));
                    onNewTerminal(path, event.currentTarget);
                  }}
                >
                  <SquareTerminal size={15} />
                </button>
              </div>
              {terminalMenuAt(path)}
              <div
                className={`project-children ${expanded ? "expanded" : ""}`}
                inert={!expanded}
              >
                <div>
                  {!!terminals.length && (
                    <WorkspaceTabs
                      tabs={terminals}
                      saved={saved}
                      active={home || desktopOpen ? "" : active}
                      label={`${project} 的终端`}
                      busy={busy}
                      ready={ready}
                      onSelect={onSelect}
                      onClose={onClose}
                      onRename={onRename}
                      onPin={onPin}
                      onReorder={onReorder}
                      splitDrag={splitDrag}
                    />
                  )}
                  {!terminals.length && !running && (
                    <p className="sidebar-empty">启动后可新建终端</p>
                  )}
                </div>
              </div>
            </section>
          );
        })}
        {!projects.length && (
          <p className="sidebar-empty">打开本地文件夹开始工作</p>
        )}
      </div>
      <div className="sidebar-bottom">
        <button className="sidebar-status" onClick={onSafety}>
          <i className={`status-dot ${network.mode}`} />
          <span>{healthy ? "环境运行正常" : runtimeLabel}</span>
          <EnvironmentShield size={17} healthy={healthy} />
        </button>
        <div className="sidebar-footer-actions">
          <AccountMenu onSettings={onSettings} onConfirm={onAccountConfirm} onStatus={onAccountStatus} busy={busy} />
          <button className="icon-button" aria-label="设置" title={shortcut("设置 ⌘,")} onClick={() => onSettings("keyboard")}><Settings2 size={17} /></button>
          {updateAction}
        </div>
      </div>
    </aside>
  );
}
