import { isMac, terminalModifier } from "./platform.mjs";
import React, {
  useEffect,
  useRef,
  useState,
  useImperativeHandle,
  forwardRef,
} from "react";
import { useAppearance } from "./appearance";
import { terminalTheme, systemCodeFont } from "./appearance-state.mjs";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import RFB from "../.generated/novnc/core/rfb.js";
import { api, writeClipboard, nativeAvailable } from "./api";
import { runtimeSocketURL } from "./client-context.mjs";
import { readClipboardContent, transferFiles, hasFiles, sendImage } from "./image-transfer";
import { sendFiles, shellFilePaths } from "./file-transfer";
import { installRemoteInput } from "./remote-input";
import { useWindowVisibility } from "./window-visibility";
import { displayGeometry, displaySizeSync } from "./remote-display.mjs";
import { RefreshCw, Clipboard, ArrowUpRight } from "lucide-react";
import "@xterm/xterm/css/xterm.css";

export const RemoteView = forwardRef(function RemoteView(
  { visible, focused = true, keyboard, tabId, copyScope = tabId, onStatus, onFallback, desktop = false, zoom = 1 },
  ref,
) {
  const windowVisible = useWindowVisibility();
  const screenVisible = visible && windowVisible;
  const box = useRef(),
    rfb = useRef(),
    input = useRef(),
    current = useRef({ visible, keyboard, onStatus, onFallback }),
    displayResize = useRef();
  current.current = { visible: screenVisible && focused, keyboard, tabId, onStatus, onFallback, zoom };
  const failures = useRef(0);
  const [connection, setConnection] = useState("connecting"),
    [attempt, setAttempt] = useState(0),
    [sizeError, setSizeError] = useState(false);
  useEffect(() => { displayResize.current?.update(); }, [zoom]);
  useEffect(() => { input.current?.cancelCopy(); }, [copyScope]);
  useImperativeHandle(ref, () => ({
    focus: () => rfb.current?.focus(),
    edit: (action) =>
      box.current?.contains(document.activeElement)
        ? input.current?.edit(action)
        : false,
    copy: () => input.current?.copy(),
    receiveCopy: (value) => input.current?.receiveCopy(value),
    paste: () => input.current?.readAndPaste(),
    pasteText: (text) => input.current?.paste(text),
    transfer: () =>
      onFallback(input.current?.text() || "", "可在此使用本机复制、粘贴。"),
  }));
  useEffect(() => {
    if (!screenVisible) return;
    let disposed = false,
      client,
      resizeObserver,
      sizeSync,
      reconnectTimer;
    setConnection("connecting");
    setSizeError(false);
    const retry = () => {
      if (disposed) return;
      setConnection("disconnected");
      clearTimeout(reconnectTimer);
      reconnectTimer = setTimeout(
        () => setAttempt((v) => v + 1),
        Math.min(1000 * 2 ** failures.current++, 5000),
      );
    };
    const desktopPrefix = desktop ? "/claude-desktop" : "";
    api(`${desktopPrefix}/vnc-credentials`)
      .then(({ password }) => {
        if (disposed) return;
        client = new RFB(
          box.current,
          runtimeSocketURL(`${desktopPrefix}/vnc`),
          { credentials: { password } },
        );
        rfb.current = client;
        client.scaleViewport = true;
        client.resizeSession = false;
        client.background = "#ffffff";
        client.qualityLevel = 8;
        client.compressionLevel = 2;
        client.addEventListener("connect", () => {
          if (disposed) return;
          failures.current = 0;
          setConnection("connected");
          sizeSync = displaySizeSync(
            (size) => api(desktop ? "/claude-desktop/resize" : "/browser/resize", size),
            { onError: () => setSizeError(true), onSuccess: () => setSizeError(false) },
          );
          const resize = () => sizeSync.update(displayGeometry(
            box.current.getBoundingClientRect(), { desktop, zoom: current.current.zoom },
          ));
          displayResize.current = { update: resize, retry: () => sizeSync.retry() };
          resizeObserver = new ResizeObserver(resize);
          resizeObserver.observe(box.current);
          resize();
          input.current = installRemoteInput(client, box.current, {
            setCopyContext: desktop ? undefined : (context, valid) => client.cageRunInput(async () => {
              if (!valid()) return;
              try { await api("/browser/copy-context", { id: current.current.tabId, context }); }
              catch {
                if (valid()) {
                  input.current?.cancelCopy();
                  current.current.onStatus("复制按钮同步暂未就绪，可使用复制快捷键或文本传递");
                }
                // A metadata failure must not cancel the user's queued click.
              }
            }),
            pasteContent: (content) => {
              const id = current.current.tabId;
              const valid = () => !disposed && current.current.visible && current.current.tabId === id;
              return client.cagePasteContent(content,
                (payload) => {
                  if (!valid()) throw new Error("输入目标已切换，请重新粘贴图片");
                  return payload.files || payload.fileSelection
                    ? sendFiles(payload, desktop ? "desktop" : "browser", id, { valid })
                    : sendImage(payload, desktop ? "desktop" : "browser", id);
                }, valid);
            },
            insertText: (text) => {
              if (desktop) return client.cagePasteText(text);
              const id = current.current.tabId;
              return client.cageRunInput(() => {
                if (disposed || !current.current.visible || current.current.tabId !== id)
                  throw new Error("Input target changed");
                return api("/browser/insert-text", { id, text });
              });
            },
            insertComposition: (id, value) => client.cageRunInput(async () => {
              if (value.mode !== "cancel" && (disposed || !current.current.visible ||
                  (desktop ? "desktop" : current.current.tabId) !== id))
                throw new Error("输入位置已切换，请重新输入");
              const result = await api("/input", { target: desktop ? "desktop" : "browser", id, ...value });
              if (value.mode !== "cancel" && !result.available)
                throw new Error("输入位置已失去焦点，请重新输入");
              return result;
            }),
            tabId: () => desktop ? "desktop" : current.current.tabId,
            caretRect: async (session) => {
              const id = desktop ? "desktop" : current.current.tabId;
              const value = await api("/input", { target: desktop ? "desktop" : "browser", id, mode: "status", session });
              return !disposed && current.current.visible &&
                (desktop ? "desktop" : current.current.tabId) === id ? value : null;
            },
            mode: () => current.current.keyboard,
            isActive: () => current.current.visible,
            onStatus: (t) => current.current.onStatus(t),
            onFallback: (...args) => current.current.onFallback(...args),
          });
          if (current.current.visible && !document.activeElement?.closest(".chrome")) client.focus();
        });
        client.addEventListener("disconnect", () => {
          if (disposed) return;
          input.current?.dispose();
          input.current = null;
          sizeSync?.dispose();
          resizeObserver?.disconnect();
          displayResize.current = null;
          retry();
        });
        client.addEventListener("securityfailure", () => {
          retry();
        });
      })
      .catch(() => {
        retry();
      });
    return () => {
      disposed = true;
      sizeSync?.dispose();
      displayResize.current = null;
      clearTimeout(reconnectTimer);
      resizeObserver?.disconnect();
      input.current?.dispose();
      input.current = null;
      client?.disconnect();
      rfb.current = null;
    };
  }, [attempt, screenVisible, desktop]);
  useEffect(() => {
    if (focused && screenVisible && connection === "connected") {
      const t = setTimeout(() => { if (!document.activeElement?.closest(".chrome")) rfb.current?.focus(); }, 80);
      return () => clearTimeout(t);
    }
  }, [focused, screenVisible, connection]);
  return (
    <div className={`remote-view ${visible ? "" : "concealed"}`}>
      <div className="remote-canvas" ref={box} />
      {sizeError && connection === "connected" && (
        <div className="remote-size-warning connection-banner" role="alert">
          画面尺寸未能同步，显示比例可能不准确。
          <button onClick={() => displayResize.current?.retry()}>重试调整</button>
        </div>
      )}
      {connection !== "connected" && (
        <div className="view-overlay">
          <RefreshCw className={connection === "connecting" ? "spin" : ""} />
          <h3>
            {connection === "connecting" ? "正在连接画面" : "画面连接已断开"}
          </h3>
          <p>后台任务不会因画面断开而自动结束。</p>
          {connection === "disconnected" && (
            <button
              className="primary"
              onClick={() => setAttempt((v) => v + 1)}
            >
              重新连接画面
            </button>
          )}
        </div>
      )}
    </div>
  );
});

export function TerminalView({ session, visible, focused = true, onStatus, onControls }) {
  const { appearance, theme, variant } = useAppearance();
  const box = useRef(),
    instance = useRef(),
    fit = useRef(),
    socket = useRef();
  const [connected, setConnected] = useState(false),
    [attempt, setAttempt] = useState(0);
  const visibleRef = useRef(visible);
  visibleRef.current = visible;
  const focusedRef = useRef(focused);
  focusedRef.current = focused;
  useEffect(() => {
    let disposed = false, emittingPaste = false, inputChain = Promise.resolve();
    const enqueue = (action) => {
      const result = inputChain.then(action);
      inputChain = result;
      const reset = () => { if (inputChain === result) inputChain = Promise.resolve(); };
      result.then(reset, reset);
      return result;
    };
    const terminal = new Terminal({
      fontFamily: theme.fonts.code || systemCodeFont,
      fontSize: appearance.codeFontSize,
      lineHeight: 1.3,
      cursorBlink: true,
      macOptionClickForcesSelection: true,
      minimumContrastRatio: 4.5,
      scrollback: 6000,
      theme: terminalTheme(theme, variant),
    });
    const fitter = new FitAddon();
    terminal.loadAddon(fitter);
    terminal.open(box.current);
    instance.current = terminal;
    fit.current = fitter;
    const ws = new WebSocket(
      runtimeSocketURL(`/terminals/${session.id}/stream`),
    );
    ws.binaryType = "arraybuffer";
    socket.current = ws;
    const resize = () => {
      if (!visibleRef.current) return;
      try {
        fitter.fit();
        if (ws.readyState === WebSocket.OPEN)
          ws.send(
            JSON.stringify({
              type: "resize",
              cols: terminal.cols,
              rows: terminal.rows,
            }),
          );
      } catch {}
    };
    ws.onopen = () => {
      setConnected(true);
      resize();
      if (visibleRef.current && focusedRef.current) terminal.focus();
    };
    ws.onmessage = (event) => terminal.write(new Uint8Array(event.data));
    ws.onclose = () => setConnected(false);
    const send = (data) => {
      if (disposed || ws.readyState !== WebSocket.OPEN) throw new Error("终端已断开，请重新连接后重试");
      ws.send(JSON.stringify({ type: "input", data }));
    };
    terminal.onData((data) => {
      if (emittingPaste) send(data);
      else void enqueue(() => send(data)).catch(() => {});
    });
    const receive = (content) => {
      // Attach rejection immediately, even while another image is in flight.
      const ready = Promise.resolve(content).then(value => ({ value }), error => ({ error }));
      onStatus("正在传递剪贴板内容…");
      void enqueue(async () => {
        const { value, error } = await ready;
        if (error) throw error;
        if (disposed || !visibleRef.current || !focusedRef.current || ws.readyState !== WebSocket.OPEN) throw new Error("终端已切换或断开，请重新粘贴");
        let text = value.text || "";
        if (value.files || value.fileSelection) {
          const result = await sendFiles(value, "terminal", session.id, { valid: () => !disposed && visibleRef.current && focusedRef.current && ws.readyState === WebSocket.OPEN });
          text = shellFilePaths(result.paths);
        }
        if (value.png) {
          const result = await sendImage(value, "terminal", session.id);
          text = `'${result.path}' `;
        }
        if (disposed || !visibleRef.current || !focusedRef.current) throw new Error("终端已切换，请回到原终端重新粘贴");
        // Honor xterm's bracketed-paste mode without enqueuing behind this job.
        emittingPaste = true;
        try { terminal.paste(text); } finally { emittingPaste = false; }
        onStatus(value.png || value.files || value.fileSelection ? "附件已保存到 Cage，路径已插入终端" : "已粘贴");
      }).catch((error) => { if (!disposed) onStatus(error.message); });
    };
    const host = box.current;
    const onPaste = (event) => {
      if (!visibleRef.current) return;
      event.preventDefault(); event.stopImmediatePropagation();
      const files = transferFiles(event.clipboardData);
      if (files.length) receive({ files });
      else if (event.clipboardData?.types.includes("text/plain")) receive({ text: event.clipboardData.getData("text/plain") });
    };
    const onDragOver = (event) => {
      if (!visibleRef.current || !hasFiles(event.dataTransfer)) return;
      event.preventDefault(); event.dataTransfer.dropEffect = "copy";
    };
    const onDrop = (event) => {
      if (!visibleRef.current || !hasFiles(event.dataTransfer)) return;
      event.preventDefault(); event.stopPropagation();
      receive({ files: transferFiles(event.dataTransfer), kind: "drop" });
      terminal.focus();
    };
    host.addEventListener("paste", onPaste, true);
    host.addEventListener("dragover", onDragOver);
    host.addEventListener("drop", onDrop);
    const copySelection = () => {
      const text = terminal.getSelection();
      if (!text) {
        onStatus(terminal.modes.mouseTrackingMode === "none"
          ? "请先拖选要复制的终端文字"
          : `当前程序接管了鼠标。请按住 ${isMac ? "Option" : "Shift"} 拖选文字，再复制。`);
        return;
      }
      void writeClipboard(text)
        .then(() => onStatus("已复制终端选中文本"))
        .catch(() => onStatus("无法写入本机剪贴板"));
    };
    onControls?.({
      edit(action) {
        if (!box.current?.contains(document.activeElement)) return false;
        if (action === "copy") {
          copySelection();
          return true;
        }
        if (action === "paste") {
          receive(readClipboardContent());
          return true;
        }
        if (action === "selectAll") {
          terminal.selectAll();
          return true;
        }
        return false;
      },
    });
    terminal.attachCustomKeyEventHandler((event) => {
      if (!terminalModifier(event) || event.type !== "keydown") return true;
      const key = event.key.toLowerCase();
      if (key === "c") {
        event.preventDefault();
        copySelection();
        return false;
      }
      if (key === "v") {
        if (!nativeAvailable()) return true;
        event.preventDefault();
        receive(readClipboardContent());
        return false;
      }
      if (key === "a") {
        event.preventDefault();
        terminal.selectAll();
        return false;
      }
      if (["+", "=", "-"].includes(key)) {
        event.preventDefault();
        terminal.options.fontSize = Math.max(
          10,
          Math.min(26, terminal.options.fontSize + (key === "-" ? -1 : 1)),
        );
        resize();
        return false;
      }
      return !terminalModifier(event);
    });
    const observer = new ResizeObserver(resize);
    observer.observe(box.current);
    resize();
    return () => {
      disposed = true;
      host.removeEventListener("paste", onPaste, true);
      host.removeEventListener("dragover", onDragOver);
      host.removeEventListener("drop", onDrop);
      onControls?.(null);
      observer.disconnect();
      ws.close();
      terminal.dispose();
      instance.current = null;
    };
  }, [session.id, attempt]);
  useEffect(() => {
    if (visible) {
      fit.current?.fit();
      if (focused) instance.current?.focus();
      const ws = socket.current,
        t = instance.current;
      if (ws?.readyState === WebSocket.OPEN && t)
        ws.send(JSON.stringify({ type: "resize", cols: t.cols, rows: t.rows }));
    }
  }, [visible, focused]);
  useEffect(() => {
    if (!instance.current) return;
    instance.current.options.theme = terminalTheme(theme, variant);
    instance.current.options.fontFamily = theme.fonts.code || systemCodeFont;
    instance.current.options.fontSize = appearance.codeFontSize;
    if (visibleRef.current) fit.current?.fit();
  }, [theme, variant, appearance.codeFontSize]);
  return (
    <div className={`terminal-view ${visible ? "" : "concealed"}`}>
      <div className="terminal-host" ref={box} />
      {!connected && (
        <div className="terminal-note">
          {session.closed
            ? "终端会话已结束。可点击项目右侧的终端图标新建终端。"
            : "正在连接终端…"}
          {!session.closed && (
            <button onClick={() => setAttempt((v) => v + 1)}>
              <RefreshCw size={13} />
              重新连接
            </button>
          )}
        </div>
      )}
    </div>
  );
}

export { Modal } from "./modal";
