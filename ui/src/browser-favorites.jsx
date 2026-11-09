import { InlineSelect } from "./inline-select";
import { shortcut } from "./platform.mjs";
import React, { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  Folder,
  FolderPlus,
  Pencil,
  Search,
  Star,
  Trash2,
  X,
} from "lucide-react";
import { api } from "./api";
import { Modal } from "./views";

// The saved page is primary, folder context secondary, management actions quiet.
// Reuse paper/strong/muted/line tokens, 8/12/16px spacing and existing modal forms.
export function BrowserFavorites({ current, ready, onOpen, shortcuts = true }) {
  const [data, setData] = useState(null);
  const [open, setOpen] = useState(false);
  const [editor, setEditor] = useState(null);
  const [folder, setFolder] = useState("all");
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const search = useRef();
  const toggle = useRef();
  const savingRef = useRef(false);
  const state = useRef();
  const favorite = data?.items.find((item) => item.url === current?.url);
  let canSave = false;
  try {
    const url = new URL(current?.url);
    canSave =
      current?.kind === "web" &&
      ["http:", "https:"].includes(url.protocol) &&
      !url.username &&
      !url.password;
  } catch {
    /* Browser internal pages cannot be bookmarked. */
  }
  const load = async () => {
    try {
      setData(await api("/favorites"));
      setError("");
    } catch (e) {
      setError(e.message);
    }
  };
  const addCurrent = () => {
    if (!canSave || !data) return;
    setError("");
    setEditor({
      kind: "item",
      ...(favorite || {
        title: current.title || current.url,
        url: current.url,
        folder_id: "",
      }),
    });
  };
  const show = () => {
    setOpen(true);
    void load();
  };
  state.current = { open, editor, addCurrent, show, shortcuts };
  useEffect(() => {
    void load();
  }, []);
  useEffect(() => {
    const add = () => state.current.shortcuts && state.current.addCurrent();
    const show = () => state.current.shortcuts && state.current.show();
    const close = () => {
      if (!state.current.editor && !savingRef.current) setOpen(false);
    };
    document.addEventListener("cage-add-favorite", add);
    document.addEventListener("cage-show-favorites", show);
    document.addEventListener("cage-close-modal", close);
    return () => {
      document.removeEventListener("cage-add-favorite", add);
      document.removeEventListener("cage-show-favorites", show);
      document.removeEventListener("cage-close-modal", close);
    };
  }, []);
  useEffect(() => {
    if (!open) return;
    search.current?.focus();
    const key = (event) => {
      if (
        event.key === "Escape" &&
        !state.current.editor &&
        !savingRef.current
      ) {
        event.preventDefault();
        setOpen(false);
      }
    };
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("keydown", key);
      toggle.current?.focus();
    };
  }, [open]);
  const mutation = async (body) => {
    const next = await api("/favorites", body);
    setData(next);
    return next;
  };
  const run = async (operation) => {
    if (savingRef.current) return;
    savingRef.current = true;
    setSaving(true);
    setError("");
    try {
      await operation();
    } catch (e) {
      setError(e.message);
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  };
  const save = (event) => {
    event.preventDefault();
    void run(async () => {
      if (editor.kind === "folder") {
        await mutation({
          action: "save_folder",
          id: editor.id || "",
          title: editor.title,
        });
      } else {
        let folderID = editor.folder_id;
        if (editor.newFolder?.trim()) {
          const next = await mutation({
            action: "save_folder",
            title: editor.newFolder.trim(),
          });
          folderID = next.folders.at(-1).id;
          setEditor((draft) => ({
            ...draft,
            folder_id: folderID,
            newFolder: "",
          }));
        }
        await mutation({
          action: "save",
          id: editor.id || "",
          title: editor.title,
          url: editor.url,
          folder_id: folderID,
        });
      }
      setEditor(null);
    });
  };
  const remove = (id, isFolder = false) =>
    run(async () => {
      await mutation({ action: isFolder ? "delete_folder" : "delete", id });
      if (isFolder) setFolder("");
      else setEditor(null);
    });
  const visible = (data?.items || []).filter(
    (item) =>
      (folder === "all" || item.folder_id === folder) &&
      `${item.title} ${item.url}`.toLowerCase().includes(query.toLowerCase()),
  );
  const selectedFolder = data?.folders.find((item) => item.id === folder);
  return (
    <>
      <div className="favorites-actions">
        <button
          className={`icon-button favorite-star ${favorite ? "saved" : ""}`}
          aria-label={favorite ? "编辑此页收藏" : "收藏此页"}
          title={shortcut(favorite ? "编辑此页收藏 ⌘D" : "收藏此页 ⌘D")}
          disabled={!canSave || !data}
          onClick={addCurrent}
        >
          <Star size={17} fill={favorite ? "currentColor" : "none"} />
        </button>
        <button
          ref={toggle}
          className="icon-button"
          aria-label="收藏夹"
          title={shortcut("收藏夹 ⌘⇧O")}
          aria-expanded={open}
          onClick={() => (open ? setOpen(false) : show())}
        >
          <Folder size={17} />
        </button>
      </div>
      {open &&
        createPortal(
          <>
            <div
              className="favorites-dismiss"
              onClick={() => {
                if (!saving) setOpen(false);
              }}
            />
            <section
              className="favorites-panel"
              role="dialog"
              aria-label="收藏夹"
              data-browser-dialog
            >
              <header>
                <h2>收藏夹</h2>
                <button
                  className="icon-button"
                  aria-label="新建收藏夹"
                  title="新建收藏夹"
                  disabled={!data || saving}
                  onClick={() => {
                    setError("");
                    setEditor({ kind: "folder", title: "" });
                  }}
                >
                  <FolderPlus size={17} />
                </button>
                <button
                  className="icon-button"
                  aria-label="关闭收藏夹"
                  onClick={() => setOpen(false)}
                >
                  <X size={17} />
                </button>
              </header>
              <label className="favorites-search">
                <Search size={15} />
                <input
                  ref={search}
                  aria-label="搜索收藏"
                  placeholder="搜索收藏"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                />
              </label>
              <div className="favorites-folder-select">
                <InlineSelect
                  aria-label="筛选收藏夹"
                  value={folder}
                  onValueChange={(next) => setFolder(next)}
                >
                  <option value="all">全部收藏</option>
                  <option value="">未分类</option>
                  {data?.folders.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.title}
                    </option>
                  ))}
                </InlineSelect>
                {selectedFolder && (
                  <>
                    <button
                      className="icon-button"
                      aria-label="重命名收藏夹"
                      title="重命名收藏夹"
                      disabled={saving}
                      onClick={() => {
                        setError("");
                        setEditor({ kind: "folder", ...selectedFolder });
                      }}
                    >
                      <Pencil size={15} />
                    </button>
                    <button
                      className="icon-button"
                      aria-label="删除收藏夹，收藏移至未分类"
                      title="删除收藏夹，收藏移至未分类"
                      disabled={saving}
                      onClick={() => remove(folder, true)}
                    >
                      <Trash2 size={15} />
                    </button>
                  </>
                )}
              </div>
              {error && !editor && (
                <div className="favorites-error" role="alert">
                  {error}
                  <button onClick={load}>重试读取</button>
                </div>
              )}
              <div className="favorites-list">
                {!data && !error && (
                  <p className="favorites-empty">正在读取收藏…</p>
                )}
                {data && visible.length === 0 && (
                  <div className="favorites-empty">
                    <Star size={24} />
                    <p>{query ? "没有匹配的收藏" : "这里还没有收藏"}</p>
                    <span>
                      {query
                        ? "试试其他名称或网址。"
                        : "点击地址栏旁的星标，保存常用网页。"}
                    </span>
                  </div>
                )}
                {visible.map((item) => (
                  <div className="favorite-row" key={item.id}>
                    <button
                      className="favorite-link"
                      disabled={!ready || saving}
                      title={item.url}
                      onClick={() =>
                        run(async () => {
                          if (await onOpen(item.url)) setOpen(false);
                        })
                      }
                    >
                      <Star size={15} />
                      <span>
                        <strong>{item.title}</strong>
                        <small>{item.url}</small>
                      </span>
                    </button>
                    <button
                      className="icon-button"
                      aria-label={`编辑收藏 ${item.title}`}
                      title="编辑收藏"
                      disabled={saving}
                      onClick={() => {
                        setError("");
                        setEditor({ kind: "item", ...item });
                      }}
                    >
                      <Pencil size={14} />
                    </button>
                  </div>
                ))}
              </div>
              {data && (
                <footer>
                  {visible.length} 个收藏<span>保存在此 Cage 环境</span>
                </footer>
              )}
            </section>
          </>,
          document.body,
        )}
      {editor &&
        createPortal(
          <div className="favorite-editor" data-browser-dialog>
            <Modal
              title={
                editor.kind === "folder"
                  ? editor.id
                    ? "重命名收藏夹"
                    : "新建收藏夹"
                  : editor.id
                    ? "编辑收藏"
                    : "添加收藏"
              }
              onClose={
                saving
                  ? undefined
                  : () => {
                      setEditor(null);
                      setError("");
                    }
              }
              footer={
                <>
                  {editor.kind === "item" && editor.id && (
                    <button
                      className="favorite-remove"
                      disabled={saving}
                      onClick={() => remove(editor.id)}
                    >
                      删除收藏
                    </button>
                  )}
                  <button
                    disabled={saving}
                    onClick={() => {
                      setEditor(null);
                      setError("");
                    }}
                  >
                    取消
                  </button>
                  <button
                    className="primary"
                    type="submit"
                    form="favorite-form"
                    disabled={saving}
                  >
                    {saving ? "保存中…" : "保存"}
                  </button>
                </>
              }
            >
              <form
                id="favorite-form"
                className="favorite-form"
                onSubmit={save}
              >
                <label>
                  名称
                  <input
                    aria-label="收藏名称"
                    required
                    maxLength={250}
                    value={editor.title}
                    onChange={(event) =>
                      setEditor({ ...editor, title: event.target.value })
                    }
                  />
                </label>
                {editor.kind === "item" && (
                  <>
                    <label>
                      网址
                      <input
                        aria-label="收藏网址"
                        type="url"
                        required
                        value={editor.url}
                        onChange={(event) =>
                          setEditor({ ...editor, url: event.target.value })
                        }
                      />
                    </label>
                    <label>
                      收藏夹
                      <InlineSelect
                        aria-label="保存到收藏夹"
                        value={editor.folder_id}
                        disabled={Boolean(editor.newFolder)}
                        onValueChange={(next) =>
                          setEditor({
                            ...editor,
                            folder_id: next,
                          })
                        }
                      >
                        <option value="">未分类</option>
                        {data?.folders.map((item) => (
                          <option key={item.id} value={item.id}>
                            {item.title}
                          </option>
                        ))}
                      </InlineSelect>
                    </label>
                    <label>
                      新建收藏夹（可选）
                      <input
                        aria-label="新收藏夹名称"
                        maxLength={250}
                        placeholder="填写名称后，将收藏保存到新文件夹"
                        value={editor.newFolder || ""}
                        onChange={(event) =>
                          setEditor({
                            ...editor,
                            newFolder: event.target.value,
                          })
                        }
                      />
                    </label>
                  </>
                )}
                {error && (
                  <p role="alert" className="favorites-error">
                    {error}
                  </p>
                )}
              </form>
            </Modal>
          </div>,
          document.body,
        )}
    </>
  );
}
