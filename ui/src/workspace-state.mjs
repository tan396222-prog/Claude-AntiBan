export function readLocal(key, fallback) {
  try {
    return JSON.parse(localStorage.getItem(key)) ?? fallback;
  } catch {
    return fallback;
  }
}

export function orderedTabs(tabs, saved) {
  const order = saved.order || [];
  return [...tabs].sort((a, b) => {
    const pinned =
      Number(Boolean(saved[b.id]?.pinned)) -
      Number(Boolean(saved[a.id]?.pinned));
    if (pinned) return pinned;
    const index = (id) =>
      order.includes(id) ? order.indexOf(id) : order.length;
    return index(a.id) - index(b.id);
  });
}

export const projectName = (path, defaultWorkspace) =>
  path === defaultWorkspace && path?.endsWith("-workspace")
    ? "默认工作区"
    : path?.split(/[\\/]/).filter(Boolean).at(-1) || "新项目";

export function tabName(tab, saved, project) {
  return (
    saved[tab.id]?.title ||
    (tab.kind === "editor"
      ? `文件 · ${tab.workspace ? projectName(tab.workspace) : project}`
      : tab.title) ||
    "新标签页"
  );
}

// The focused tab and its neighbour retain their screen positions on focus changes.
export function focusTab(layout, id) {
  if (layout.active === id) return layout;
  if (!id) return { active: "" };
  if (layout.other === id)
    return { active: id, other: layout.active, side: layout.side === "left" ? "right" : "left" };
  if (layout.other && id.startsWith("terminal:") !== layout.active.startsWith("terminal:") &&
      id.startsWith("terminal:") === layout.other.startsWith("terminal:"))
    return { ...focusTab(layout, layout.other), active: id };
  return { ...layout, active: id };
}

export function splitTab(layout, tabs, id, side) {
  if (!tabs.some(tab => tab.id === id)) return layout;
  const opposite = layout.side === side ? layout.other : layout.active;
  const other = [opposite, layout.other, layout.active, ...tabs.map(tab => tab.id)]
    .find(candidate => candidate !== id && tabs.some(tab => tab.id === candidate));
  return other ? { active: id, other, side } : layout;
}
