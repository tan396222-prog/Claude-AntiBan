import React, { useLayoutEffect, useRef } from "react";
import { Sparkles, TerminalSquare } from "lucide-react";

export function TerminalMenu({ choice, busy, onClose, onSelect }) {
  const menu = useRef(null);
  const close = useRef(onClose);
  close.current = onClose;
  useLayoutEffect(() => {
    const node = menu.current;
    const anchor = choice.anchor;
    if (!anchor?.isConnected) {
      close.current();
      return;
    }
    anchor.setAttribute("aria-expanded", "true");
    anchor.setAttribute("aria-controls", "terminal-create-menu");
    node.querySelector("button:not(:disabled)")?.focus();
    const dismiss = (restoreFocus = false) => {
      if (restoreFocus) anchor.focus();
      close.current();
    };
    const outside = (event) => {
      if (!node.contains(event.target) && !anchor.contains(event.target)) dismiss();
    };
    const keydown = (event) => {
      if (event.key === "Escape" || event.key === "Tab") {
        if (event.key === "Escape") event.preventDefault();
        event.stopPropagation();
        dismiss(true);
      } else if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        const items = [...node.querySelectorAll("button:not(:disabled)")];
        const index = items.indexOf(document.activeElement);
        const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1
          : (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
        items[next]?.focus();
      }
    };
    const closeCommand = () => dismiss(true);
    document.addEventListener("click", outside, true);
    document.addEventListener("keydown", keydown, true);
    document.addEventListener("cage-close-modal", closeCommand);
    return () => {
      anchor.setAttribute("aria-expanded", "false");
      anchor.removeAttribute("aria-controls");
      document.removeEventListener("click", outside, true);
      document.removeEventListener("keydown", keydown, true);
      document.removeEventListener("cage-close-modal", closeCommand);
    };
  }, [choice]);

  return (
    <div ref={menu} id="terminal-create-menu" className="menu terminal-create-menu" role="menu" aria-label="新建终端">
      <button role="menuitem" disabled={busy} onClick={() => onSelect("shell")}>
        <TerminalSquare size={16} />普通终端
      </button>
      <button role="menuitem" disabled={busy} onClick={() => onSelect("claude")}>
        <Sparkles size={16} />Claude 终端
      </button>
    </div>
  );
}
