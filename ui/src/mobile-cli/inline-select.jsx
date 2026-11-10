import React, { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown } from "lucide-react";
import { useSelectPopup } from "../select-popup";

// The floating options keep the combobox focused without shifting the page.
export function InlineSelect({ label, value, options, onChange, onLongPress, footer, disabled, icon }) {
  const id = useId();
  const root = useRef(null);
  const trigger = useRef(null);
  const list = useRef(null);
  const hold = useRef(null);
  const held = useRef(false);
  const origin = useRef(null);
  const [open, setOpen] = useState(false);
  const [cursor, setCursor] = useState(value);
  const selected = options.find(item => item.value === value);
  const visible = open && !disabled;
  const position = useSelectPopup(visible, trigger, list, options.length);
  const cancelHold = () => { clearTimeout(hold.current); hold.current = null; };
  useEffect(() => {
    if (!visible) return;
    const outside = event => { if (!root.current.contains(event.target) && !list.current?.contains(event.target)) setOpen(false); };
    document.addEventListener("click", outside, true);
    return () => document.removeEventListener("click", outside, true);
  }, [visible]);
  useEffect(() => () => clearTimeout(hold.current), []);
  const choose = item => {
    setOpen(false);
    trigger.current.focus();
    onChange(item.value);
  };
  const requestDisconnect = item => {
    if (!onLongPress) return;
    held.current = true;
    setOpen(false);
    onLongPress(item.value);
  };
  const press = (event, item) => {
    cancelHold();
    held.current = false;
    origin.current = { x: event.clientX, y: event.clientY };
    if (onLongPress && event.button === 0) hold.current = setTimeout(() => requestDisconnect(item), 550);
  };
  const keys = event => {
    if (event.nativeEvent.isComposing) return;
    const index = Math.max(0, options.findIndex(item => item.value === cursor));
    if (event.key === "Escape") {event.preventDefault(); setOpen(false);}
    else if (event.key === "Tab") setOpen(false);
    else if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      const next = event.key === "Home" ? 0 : event.key === "End" ? options.length - 1
        : Math.max(0, Math.min(options.length - 1, index + (visible ? (event.key === "ArrowUp" ? -1 : 1) : 0)));
      setCursor(options[next]?.value); setOpen(true);
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      if (visible) {const item = options.find(item => item.value === cursor); if (item) choose(item);}
      else {setCursor(value); setOpen(true);}
    } else if (onLongPress && event.key === "F10" && event.shiftKey) {
      event.preventDefault();
      const item = options.find(item => item.value === (visible ? cursor : value));
      if (item) requestDisconnect(item);
    }
  };
  const pointerHandlers = item => ({
    onPointerDown: event => press(event, item),
    onPointerUp: cancelHold,
    onPointerCancel: cancelHold,
    onPointerLeave: cancelHold,
    onPointerMove: event => {
      if (origin.current && Math.hypot(event.clientX - origin.current.x, event.clientY - origin.current.y) > 10) cancelHold();
    },
    onContextMenu: event => {
      if (onLongPress) {event.preventDefault(); cancelHold(); if (!held.current) requestDisconnect(item);}
    },
  });
  return <div className="inline-select" ref={root}>
    <button type="button" className="inline-select-trigger" ref={trigger} role="combobox" aria-label={label}
      aria-haspopup="listbox" aria-expanded={visible} aria-controls={visible ? id : undefined}
      aria-activedescendant={visible ? id + "-" + options.findIndex(item => item.value === cursor) : undefined}
      aria-describedby={footer ? id + "-hint" : undefined} disabled={disabled} onKeyDown={keys}
      {...(selected ? pointerHandlers(selected) : {})}
      onClick={() => {if (held.current) {held.current = false; return;} setCursor(value); setOpen(!visible);}}>
      {icon}<span>{selected?.label || label}</span><ChevronDown size={14} aria-hidden="true" />
    </button>
    {visible && createPortal(<div className="inline-select-popover" ref={list} style={position}>
      <div className="inline-select-options" role="listbox" id={id} aria-label={label + "选项"}>
        {options.map((item, index) => <div key={item.value} role="option" id={id + "-" + index}
          aria-selected={item.value === value} data-active={item.value === cursor || undefined}
          {...pointerHandlers(item)} onMouseDown={event => event.preventDefault()}
          onClick={() => {if (held.current) {held.current = false; return;} choose(item);}}>
          <span>{item.optionLabel || item.label}</span>{item.value === value && <Check size={15} aria-hidden="true" />}
        </div>)}
      </div>
      {footer && <p className="inline-select-hint" id={id + "-hint"}>{footer}</p>}
    </div>, document.body)}
  </div>;
}
