import React, { Children, useEffect, useId, useRef, useState } from "react";
import { Check, ChevronDown } from "lucide-react";
import "./inline-select.css";

// Keep focus on the control while its list occupies space below it.
export function InlineSelect({
  children,
  value,
  onValueChange,
  disabled,
  editable = false,
  ...props
}) {
  const id = useId();
  const root = useRef(null);
  const control = useRef(null);
  const list = useRef(null);
  const [open, setOpen] = useState(false);
  const [cursor, setCursor] = useState(null);
  const options = Children.toArray(children).flatMap((child) => {
    const group = child.type === "optgroup" ? child.props.label : null;
    return (group ? Children.toArray(child.props.children) : [child]).map(
      ({ props: option }) => ({
        value: String(option.value),
        label:
          Children.toArray(option.children).join("") || String(option.value),
        disabled: option.disabled,
        group,
      }),
    );
  });
  const selected = options.find((option) => option.value === String(value));
  const filtered =
    editable && !selected && value
      ? options.filter((option) =>
          (option.label + " " + option.value)
            .toLowerCase()
            .includes(value.toLowerCase()),
        )
      : options;
  const visible = open && !disabled && filtered.length > 0;
  const active = filtered.findIndex(
    (item) => item.value === cursor && !item.disabled,
  );
  useEffect(() => {
    if (visible && active >= 0) {
      const row = list.current.querySelector(`[data-index="${active}"]`);
      if (row.offsetTop < list.current.scrollTop)
        list.current.scrollTop = row.offsetTop;
      else if (
        row.offsetTop + row.offsetHeight >
        list.current.scrollTop + list.current.clientHeight
      )
        list.current.scrollTop =
          row.offsetTop + row.offsetHeight - list.current.clientHeight;
    }
  }, [visible, active]);
  useEffect(() => {
    if (!open) return;
    // Collapse after the outside click lands so reflow cannot move its target.
    const outside = (event) => {
      if (!root.current.contains(event.target)) setOpen(false);
    };
    document.addEventListener("click", outside, true);
    return () => document.removeEventListener("click", outside, true);
  }, [open]);
  const show = () => {
    setCursor(selected?.value ?? null);
    setOpen(true);
  };
  const choose = (item) => {
    if (item.disabled) return;
    control.current.focus();
    setOpen(false);
    onValueChange(item.value);
  };
  const keydown = (event) => {
    const key = event.key;
    if (event.nativeEvent.isComposing) return;
    if (key === "Tab") {
      setOpen(false);
    } else if (key === "Escape" && open) {
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
    } else if (key === "Enter" || (!editable && key === " ")) {
      if (editable && (!visible || active < 0)) return;
      event.preventDefault();
      if (!visible) show();
      else if (active >= 0) choose(filtered[active]);
      else setOpen(false);
    } else if (
      ["ArrowUp", "ArrowDown"].includes(key) ||
      (!editable && ["Home", "End"].includes(key))
    ) {
      event.preventDefault();
      const enabled = filtered.filter((item) => !item.disabled);
      const index = enabled.findIndex((item) => item.value === cursor);
      let next =
        key === "Home"
          ? 0
          : key === "End"
            ? enabled.length - 1
            : Math.min(
                enabled.length - 1,
                Math.max(0, index + (key === "ArrowUp" ? -1 : 1)),
              );
      if (!visible && selected && key.startsWith("Arrow"))
        next = enabled.indexOf(selected);
      setCursor(enabled[next]?.value ?? null);
      setOpen(true);
    }
  };
  const common = {
    ...props,
    ref: control,
    disabled,
    role: "combobox",
    "aria-expanded": visible,
    "aria-controls": visible ? id : undefined,
    "aria-activedescendant":
      visible && active >= 0 ? id + "-" + active : undefined,
    onKeyDown: keydown,
  };
  return (
    <span ref={root} className="inline-select">
      {editable ? (
        <input
          {...common}
          aria-autocomplete="list"
          autoComplete="off"
          value={value}
          onClick={show}
          onChange={(event) => {
            onValueChange(event.target.value);
            setCursor(null);
            setOpen(true);
          }}
        />
      ) : (
        <button
          {...common}
          type="button"
          value={value}
          className="inline-select-trigger"
          onClick={() => {
            control.current.focus();
            open ? setOpen(false) : show();
          }}
        >
          <span>{selected?.label || value}</span>
          <ChevronDown size={14} aria-hidden="true" />
        </button>
      )}
      {visible && (
        <span
          ref={list}
          id={id}
          role="listbox"
          aria-label={(props["aria-label"] || "") + "选项"}
          className="inline-select-list"
          onMouseDown={(event) => event.preventDefault()}
          onClick={(event) => event.preventDefault()}
        >
          {filtered.map((item, index) => (
            <React.Fragment key={item.value}>
              {item.group && item.group !== filtered[index - 1]?.group && (
                <span className="inline-select-group" aria-hidden="true">
                  {item.group}
                </span>
              )}
              <span
                role="option"
                id={id + "-" + index}
                data-index={index}
                data-value={item.value}
                data-active={index === active || undefined}
                aria-disabled={Boolean(item.disabled)}
                aria-selected={item === selected}
                onClick={() => choose(item)}
              >
                <span>
                  {editable && item.label !== item.value
                    ? item.value + " · "
                    : ""}
                  {item.label}
                </span>
                {item === selected && <Check size={14} aria-hidden="true" />}
              </span>
            </React.Fragment>
          ))}
        </span>
      )}
    </span>
  );
}
