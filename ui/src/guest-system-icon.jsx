import React from "react";

const labels = { macos: "macOS", "windows-11": "Windows", "debian-12": "Debian", "debian-13": "Linux", "ubuntu-22.04": "Ubuntu", "ubuntu-24.04": "Ubuntu" };

export function GuestSystemIcon({ system }) {
  const label = labels[system];
  if (!label) return null;
  return <span className="guest-system-icon" role="img" aria-label={`客户机系统：${label}`} title={`客户机系统：${label}`}>
    {system === "windows-11" ? <svg viewBox="0 0 24 24" aria-hidden="true">
      <path fill="currentColor" d="M2 2h9v9H2zm11 0h9v9h-9zM2 13h9v9H2zm11 0h9v9h-9z" />
    </svg> : system === "macos" ? <svg viewBox="0 0 24 24" aria-hidden="true">
      <path fill="currentColor" d="M14.1 5.2c.1-1.8 1.5-3.5 3.5-3.7.1 1.9-1.3 3.6-3.5 3.7ZM19.6 16.6c-.5 1.4-1.3 2.8-2.1 3.9-.7.9-1.4 1.7-2.5 1.7-1 0-1.5-.6-2.9-.6s-2 .6-3 .6c-1.1 0-1.9-.9-2.7-2-1.6-2.2-2.8-6.2-1.2-8.9.8-1.4 2.3-2.3 3.9-2.3 1.2 0 2.3.7 3 .7.7 0 2-.8 3.5-.7 1.4.1 2.7.7 3.5 1.8-3.2 1.9-2.7 5.2.5 5.8Z" />
    </svg> : <span className="guest-system-icon-linux" aria-hidden="true" />}
  </span>;
}
