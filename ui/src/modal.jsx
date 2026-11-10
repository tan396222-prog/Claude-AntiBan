import React, { useEffect, useLayoutEffect, useRef } from "react";

export function Modal({ title, children, onClose, wide = false, footer, anchor }) {
  const panel = useRef(),
    closeRef = useRef(onClose);
  closeRef.current = onClose;
  useLayoutEffect(() => {
    if (!anchor) return;
    const node = panel.current;
    anchor.setAttribute("aria-expanded", "true");
    const position = () => {
      if (!anchor.isConnected) {
        closeRef.current?.();
        return;
      }
      const rect = anchor.getBoundingClientRect();
      const box = node.getBoundingClientRect();
      const left = Math.max(8, Math.min(rect.right - box.width, window.innerWidth - box.width - 8));
      const below = rect.bottom + 6;
      const top = below + box.height <= window.innerHeight - 8
        ? below : Math.max(8, rect.top - box.height - 6);
      node.style.left = `${left}px`;
      node.style.top = `${top}px`;
    };
    position();
    const observer = new ResizeObserver(position);
    observer.observe(node);
    window.addEventListener("resize", position);
    document.addEventListener("scroll", position, true);
    return () => {
      anchor.focus();
      anchor.removeAttribute("aria-expanded");
      observer.disconnect();
      window.removeEventListener("resize", position);
      document.removeEventListener("scroll", position, true);
    };
  }, [anchor]);
  useEffect(() => {
    const previous = anchor || document.activeElement;
    const first = panel.current.querySelector(
      "input:not([disabled]),button:not([disabled]),select:not([disabled])",
    );
    first?.focus();
    const key = (e) => {
      if (
        [...document.querySelectorAll('[role="dialog"]')].at(-1) !==
        panel.current
      )
        return;
      if (e.key === "Escape") {
        if (e.target.closest('[role="combobox"][aria-expanded="true"]')) return;
        e.preventDefault();
        e.stopPropagation();
        closeRef.current?.();
      }
      if (e.key === "Tab") {
        const nodes = [
          ...panel.current.querySelectorAll(
            "input:not([disabled]),button:not([disabled]),select:not([disabled]),textarea:not([disabled]),a[href]",
          ),
        ].filter((n) => n.offsetParent !== null);
        if (!nodes.length) return;
        const index = nodes.indexOf(document.activeElement);
        if (e.shiftKey && index <= 0) {
          e.preventDefault();
          nodes.at(-1).focus();
        } else if (!e.shiftKey && (index === nodes.length - 1 || index < 0)) {
          e.preventDefault();
          nodes[0].focus();
        }
      }
    };
    const closeEvent = () => {
      if (
        [...document.querySelectorAll('[role="dialog"]')].at(-1) ===
        panel.current
      )
        closeRef.current?.();
    };
    document.addEventListener("cage-close-modal", closeEvent);
    document.addEventListener("keydown", key, true);
    return () => {
      document.removeEventListener("keydown", key, true);
      document.removeEventListener("cage-close-modal", closeEvent);
      previous?.focus?.();
    };
  }, []);
  return (
    <div
      className={`modal-scrim${anchor ? " anchored-modal-scrim" : ""}`}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose?.();
      }}
    >
      <section
        ref={panel}
        className={`modal ${wide ? "wide" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <header className="modal-head">
          <h2>{title}</h2>
          {onClose && (
            <button
              className="icon-button close-modal"
              aria-label="关闭面板"
              onClick={onClose}
            >
              ×
            </button>
          )}
        </header>
        {children}
        {footer && <footer className="modal-footer">{footer}</footer>}
      </section>
    </div>
  );
}
