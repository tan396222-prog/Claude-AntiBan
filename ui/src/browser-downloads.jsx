import React, { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ArrowDownToLine, CircleAlert } from "lucide-react";
import { api, native, nativeAvailable } from "./api";
import { isRemoteClient, runtimeURL } from "./client-context.mjs";
import { Modal } from "./views";

export function useDownloadNotifications(
  downloads = [],
  onStatus,
  uploading = false,
) {
  const previous = useRef(new Map());
  const mountedAt = useRef(Date.now());
  const observed = useRef(new Set()),
    prompted = useRef(new Set()),
    queue = useRef(Promise.resolve());
  useEffect(() => {
    for (const item of downloads) {
      if (["downloading", "saving"].includes(item.state) ||
          (item.target === "desktop" && Date.parse(item.startedAt) >= mountedAt.current))
        observed.current.add(item.id);
      if (item.state === "failed" && previous.current.get(item.id) !== "failed")
        onStatus(
          `“${item.name}”未能保存：${item.error || "请在下载列表中重试"}`,
        );
      if (item.target === "desktop" && observed.current.has(item.id) && previous.current.get(item.id) !== item.state) {
        if (item.state === "downloading") onStatus(`正在下载“${item.name}”，可在侧栏“下载”查看`);
        if (item.state === "saved") onStatus(`“${item.name}”已保存到默认工作区的 Downloads`);
      }
      if (
        nativeAvailable() &&
        !uploading &&
        observed.current.has(item.id) &&
        !prompted.current.has(item.id) &&
        (item.state === "saved" || (item.target !== "desktop" && item.state === "failed" && item.retryable))
      ) {
        prompted.current.add(item.id);
        queue.current = queue.current
          .then(() => saveDownload(item))
          .catch((error) => onStatus(error.message));
      }
    }
    previous.current = new Map(downloads.map((item) => [item.id, item.state]));
  }, [downloads, uploading]);
}

export function useDesktopFiles(ready) {
  const [files, setFiles] = useState({ uploads: [], downloads: [] });
  useEffect(() => {
    if (!ready) { setFiles({ uploads: [], downloads: [] }); return; }
    let disposed = false, timer;
    const poll = async () => {
      try {
        const value = await api("/input/files/desktop/requests");
        if (!disposed) setFiles({ uploads: value.uploads || [], downloads: value.downloads || [] });
      } catch {
        // Closing Desktop invalidates its outstanding file choosers.
        if (!disposed) setFiles((previous) => ({ ...previous, uploads: [] }));
      }
      if (!disposed) timer = setTimeout(poll, 1500);
    };
    void poll();
    return () => { disposed = true; clearTimeout(timer); };
  }, [ready]);
  return files;
}

async function saveDownload(item) {
  if (!nativeAvailable()) {
    const link = document.createElement("a");
    link.href = runtimeURL(`/browser/files/downloads/${encodeURIComponent(item.id)}/content`);
    link.download = item.name;
    link.click();
    return;
  }
  const result = await native("saveBrowserDownload", {
    id: item.id,
    name: item.name,
  });
  if (result?.path)
    window.dispatchEvent(
      new CustomEvent("cage-download-saved", { detail: result }),
    );
  return result;
}

export function BrowserDownloads({
  downloads = [],
  ready,
  onStatus,
  sidebar = false,
}) {
  const remote = isRemoteClient();
  const [open, setOpen] = useState(false),
    [retrying, setRetrying] = useState("");
  const [saved, setSaved] = useState([]);
  useEffect(() => {
    const refresh = () =>
      api("/browser/files/saved")
        .then((value) => setSaved(Array.isArray(value) ? value : []))
        .catch(() => {});
    refresh();
    window.addEventListener("cage-download-saved", refresh);
    return () => window.removeEventListener("cage-download-saved", refresh);
  }, [open]);
  const items = downloads.map((item) => {
    const local = saved.find((entry) => entry.id === item.id);
    return local
      ? { ...item, ...local, localSaved: true, state: "saved", retryable: false, error: "" }
      : item;
  });
  for (const item of saved)
    if (!items.some((entry) => entry.id === item.id))
      items.push({ ...item, localSaved: true, state: "saved" });
  const failed = items.some((item) => item.state === "failed");
  const labels = {
    downloading: "正在下载",
    saving: remote ? "正在保存到云端" : "正在保存到本机",
    saved: remote ? "已保存到云端" : "已保存到本机",
    failed: "保存失败",
    canceled: "下载已取消",
  };
  return (
    <>
      <button
        className={
          sidebar ? "sidebar-link" : "icon-button browser-download-button"
        }
        aria-label={sidebar ? "下载文件" : "浏览器下载"}
        title={failed ? "有下载未能保存，点击查看" : "下载文件"}
        onClick={() => setOpen(true)}
      >
        {failed ? <CircleAlert size={16} /> : <ArrowDownToLine size={16} />}
        {sidebar && "下载"}
      </button>
      {open &&
        createPortal(
          <Modal title="下载文件" onClose={() => setOpen(false)}>
            <div className="browser-download-list">
              {!items.length && (
                <p>
                  暂无下载。下载完成后可另存为到本机；取消时，原文件仍保留在{remote ? "云端" : ""}默认工作区的
                  Downloads 文件夹。
                </p>
              )}
              {items.map((item) => (
                <article key={item.id} className="browser-download-item">
                  <strong>{item.name}</strong>
                  <p>
                    {item.localSaved ? "已保存到本机" : labels[item.state] || "状态待确认"}
                    {item.state === "downloading" &&
                      item.totalBytes > 0 &&
                      ` · ${Math.min(100, Math.floor((item.receivedBytes / item.totalBytes) * 100))}%`}
                  </p>
                  {item.path && <p className="download-path">{remote ? item.localSaved ? "本机：" : "云端：" : ""}{item.path}</p>}
                  {item.error && <p role="status">{item.error}</p>}
                  {(item.state === "saved" || (item.target !== "desktop" && item.retryable)) && (
                    <div className="download-actions">
                      <button
                        disabled={Boolean(retrying)}
                        onClick={async () => {
                          setRetrying(item.id);
                          try {
                            await saveDownload(item);
                          } catch (error) {
                            onStatus(error.message);
                          } finally {
                            setRetrying("");
                          }
                        }}
                      >
                        {retrying === item.id ? "正在保存…" : "另存为…"}
                      </button>
                      {nativeAvailable() &&
                        item.state === "saved" &&
                        (!remote || item.localSaved) &&
                        [
                          ["open", "打开文件"],
                          ["reveal", "在文件夹中显示"],
                        ].map(([operation, label]) => (
                          <button
                            key={operation}
                            onClick={async () => {
                              try {
                                await native("browserDownloadAction", {
                                  operation,
                                  id: item.id,
                                });
                              } catch (error) {
                                onStatus(error.message);
                              }
                            }}
                          >
                            {label}
                          </button>
                        ))}
                    </div>
                  )}
                  {item.retryable && (
                    <button
                      disabled={!ready || Boolean(retrying)}
                      onClick={async () => {
                        setRetrying(item.id);
                        try {
                          await api(item.target === "desktop" ? "/input/files/download-retry" : "/browser/download-retry",
                            { id: item.id, ...(item.target === "desktop" ? { target: "desktop" } : {}) });
                        } catch (error) {
                          onStatus(error.message);
                        } finally {
                          setRetrying("");
                        }
                      }}
                    >
                      {retrying === item.id ? "正在重试…" : "重试保存"}
                    </button>
                  )}
                </article>
              ))}
            </div>
          </Modal>,
          document.body,
        )}
    </>
  );
}
