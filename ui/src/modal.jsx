import React, { useEffect, useRef } from "react";

export function Modal({ title, children, onClose, wide = false, footer }) {
  const panel = useRef(),
    closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    const previous = document.activeElement;
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
      className="modal-scrim"
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
