import React, { useEffect, useId, useRef, useState } from "react";
import { AlertCircle, FileText, LoaderCircle, Upload } from "lucide-react";

// The file is the main input; editable content is secondary. Import remains
// the form's primary action. Reuse the proxy form's spacing and theme tokens.
export function ProxyYAMLInput({
  content,
  disabled,
  onContentChange,
  onFileRead,
  onReadingChange,
}) {
  const input = useRef(null);
  const request = useRef(0);
  const dragDepth = useRef(0);
  const errorID = useId();
  const helpID = useId();
  const [dragging, setDragging] = useState(false);
  const [reading, setReading] = useState(false);
  const [selected, setSelected] = useState(null);
  const [edited, setEdited] = useState(false);
  const [error, setError] = useState("");
  const hasFiles = (event) =>
    Array.from(event.dataTransfer?.types || []).includes("Files");

  useEffect(() => {
    // A missed drop must not navigate the WebView away from the settings form.
    const preventFileNavigation = (event) => {
      if (hasFiles(event)) event.preventDefault();
    };
    const resetDrag = (event) => {
      preventFileNavigation(event);
      dragDepth.current = 0;
      setDragging(false);
    };
    window.addEventListener("dragover", preventFileNavigation);
    window.addEventListener("drop", resetDrag);
    window.addEventListener("dragend", resetDrag);
    return () => {
      request.current++;
      onReadingChange(false);
      window.removeEventListener("dragover", preventFileNavigation);
      window.removeEventListener("drop", resetDrag);
      window.removeEventListener("dragend", resetDrag);
    };
  }, [onReadingChange]);

  const readFiles = async (files) => {
    if (disabled || !files.length) return;
    const current = ++request.current;
    setReading(false);
    onReadingChange(false);
    setError("");
    if (files.length !== 1) {
      setError("一次只能导入一个配置文件，请逐个添加来源。");
      return;
    }
    const value = files[0];
    if (!/\.(ya?ml|json)$/i.test(value.name)) {
      setError("请选择 .yaml、.yml 或 .json 配置文件。");
      return;
    }
    if (value.size > 8 * 1024 * 1024) {
      setError("文件超过 8 MiB，请精简配置后重试。");
      return;
    }
    setReading(true);
    onReadingChange(true);
    try {
      const text = await value.text();
      if (current !== request.current) return;
      if (!text.trim()) {
        setError("文件内容为空，请选择含节点的配置文件。");
        return;
      }
      onFileRead(value, text);
      setSelected({ name: value.name, size: value.size });
      setEdited(false);
    } catch {
      if (current === request.current)
        setError("文件读取失败，请重新选择或粘贴配置。");
    } finally {
      if (current === request.current) {
        setReading(false);
        onReadingChange(false);
      }
    }
  };
  const fileSize =
    selected &&
    (selected.size < 1024
      ? `${selected.size} B`
      : selected.size < 1024 ** 2
        ? `${(selected.size / 1024).toFixed(1)} KiB`
        : `${(selected.size / 1024 ** 2).toFixed(1)} MiB`);

  return (
    <div
      className={`proxy-yaml-input${dragging ? " is-dragging" : ""}`}
      onDragEnter={(event) => {
        if (!hasFiles(event)) return;
        event.preventDefault();
        dragDepth.current++;
        if (!disabled) setDragging(true);
      }}
      onDragOver={(event) => {
        if (!hasFiles(event)) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = disabled ? "none" : "copy";
      }}
      onDragLeave={(event) => {
        if (!hasFiles(event)) return;
        dragDepth.current = Math.max(0, dragDepth.current - 1);
        if (!dragDepth.current) setDragging(false);
      }}
      onDrop={(event) => {
        if (!hasFiles(event)) return;
        event.preventDefault();
        event.stopPropagation();
        dragDepth.current = 0;
        setDragging(false);
        readFiles(Array.from(event.dataTransfer.files));
      }}
    >
      <input
        hidden
        type="file"
        ref={input}
        disabled={disabled}
        accept=".yaml,.yml,.json,text/yaml,application/yaml,application/json"
        onChange={(event) => {
          const files = Array.from(event.target.files || []);
          event.target.value = "";
          readFiles(files);
        }}
      />
      <button
        type="button"
        className="proxy-yaml-dropzone"
        disabled={disabled}
        aria-label={selected ? "重新选择 YAML 文件" : "选择 YAML 文件"}
        aria-describedby={`${helpID}${error ? ` ${errorID}` : ""}`}
        aria-busy={reading}
        onClick={() => input.current?.click()}
      >
        <span className="proxy-yaml-icon" aria-hidden="true">
          {reading ? (
            <LoaderCircle size={22} className="spin" />
          ) : selected ? (
            <FileText size={22} />
          ) : (
            <Upload size={22} />
          )}
        </span>
        <span className="proxy-yaml-file">
          <strong>
            {dragging
              ? "松开以读取配置文件"
              : reading
                ? "正在读取配置…"
                : selected
                  ? selected.name
                  : "将配置文件拖到这里"}
          </strong>
          <span>
            {selected ? "点击或拖入其他文件替换" : "也可以点击选择文件"}
          </span>
        </span>
        <span className="proxy-yaml-choose" aria-hidden="true">
          {selected ? "更换文件" : "选择文件"}
        </span>
      </button>
      <p id={helpID} className="proxy-yaml-help">
        支持 YAML / YML / JSON，单个文件不超过 8 MiB
      </p>
      <p className="proxy-yaml-status" role="status">
        {reading
          ? "正在读取文件，请稍候。"
          : selected
            ? `${fileSize} · ${edited ? "内容已编辑" : "文件已读取"}，待导入验证`
            : ""}
      </p>
      {error && (
        <p id={errorID} className="proxy-yaml-error" role="alert">
          <AlertCircle size={15} aria-hidden="true" />
          {error}
        </p>
      )}
      <label>
        {selected ? "配置内容" : "或直接粘贴配置"}
        <textarea
          aria-label="节点 YAML"
          rows={7}
          value={content}
          disabled={disabled || reading}
          onChange={(event) => {
            setError("");
            setEdited(true);
            onContentChange(event.target.value);
          }}
          spellCheck={false}
          placeholder="proxies: …"
        />
      </label>
      <p className="field-help">
        只导入节点和明确的代理链，Cage 自行管理 DNS、监听端口及出站规则。
      </p>
    </div>
  );
}
