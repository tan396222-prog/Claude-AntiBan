import React from "react";
import { MacOSLogin } from "./macos-login";

export function ProjectRecovery({ recovery, disabled, onRetry, onSettings, onSessionClosed }) {
  return <div className="project-recovery-actions">
    {recovery === "guest-login" && <MacOSLogin disabled={disabled} onSessionClosed={onSessionClosed} />}
    {recovery === "filesystem-permission" && <MacOSLogin disabled={disabled} maintenance="projects"
      onSessionClosed={onSessionClosed} onSettings={onSettings} />}
    {["environment-update", "environment-restart"].includes(recovery) && onSettings &&
      <button type="button" disabled={disabled} onClick={onSettings}>{recovery === "environment-update" ? "查看环境组件" : "打开环境设置"}</button>}
    {recovery === "project-directory" && onSettings &&
      <button type="button" disabled={disabled} onClick={onSettings}>查看诊断记录</button>}
    <button type="button" disabled={disabled} onClick={onRetry}>{recovery === "project-approval" ? "完成项目授权" : "重新连接项目"}</button>
  </div>;
}
