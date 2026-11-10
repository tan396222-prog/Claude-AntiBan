import React, { useEffect, useState } from "react";
import { Film, Image as Picture } from "lucide-react";
import { request, sessionPath } from "./transport.mjs";

export function projectMediaKind(reference) {
  if (
    !reference ||
    /^(?:[a-z][\w+.-]*:(?![\\/])|[a-z][\w+.-]+:|[\\/]{2})/i.test(reference)
  )
    return null;
  if (/\.(?:png|jpe?g|gif|webp)$/i.test(reference)) return "image";
  return /\.(?:mp4|m4v|webm|mov)$/i.test(reference) ? "video" : null;
}

export function webURL(reference) {
  if (!/^https?:\/\//i.test(reference)) return null;
  try {
    const url = new URL(reference);
    return url.username || url.password ? null : url.href;
  } catch {
    return null;
  }
}

export function ProjectMedia({ reference, label, sessionID }) {
  const kind = projectMediaKind(reference);
  const [attempt, setAttempt] = useState(0);
  const [media, setMedia] = useState(null);
  const [error, setError] = useState("");
  const [progress, setProgress] = useState(0);
  const Icon = kind === "video" ? Film : Picture;
  const title = label || reference.split(/[\\/]/).pop();
  useEffect(() => {
    if (!attempt) return;
    const controller = new AbortController();
    let objectURL;
    async function load() {
      try {
        let offset = 0,
          version = "",
          size,
          mime;
        const chunks = [];
        do {
          const part = await request(sessionPath(sessionID) + "/media", {
            body: { reference, offset, version },
            signal: controller.signal,
          });
          if (controller.signal.aborted) return;
          if (
            !Number.isSafeInteger(part.size) ||
            part.size <= 0 ||
            part.size > 48 * 1024 * 1024 ||
            !part.version ||
            (offset &&
              (part.version !== version ||
                part.mime !== mime ||
                part.size !== size)) ||
            !/^(?:image\/(?:png|jpeg|webp|gif)|video\/(?:mp4|webm|quicktime))$/.test(
              part.mime,
            )
          )
            throw Error("文件预览数据无效。");
          version = part.version;
          size = part.size;
          mime = part.mime;
          const bytes = Uint8Array.from(atob(part.data), (c) =>
            c.charCodeAt(0),
          );
          if (!bytes.length || offset + bytes.length > size)
            throw Error("文件传输不完整。");
          chunks.push(bytes);
          offset += bytes.length;
          setProgress(Math.round((offset * 100) / size));
        } while (offset < size);
        objectURL = URL.createObjectURL(new Blob(chunks, { type: mime }));
        setMedia(objectURL);
      } catch (e) {
        if (!controller.signal.aborted)
          setError(
            e.status === 404
              ? "未找到媒体文件，或电脑端 Cage 需要更新。"
              : e.message,
          );
      }
    }
    void load();
    return () => {
      controller.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [attempt, reference, sessionID]);
  function reset() {
    setAttempt(0);
    setMedia(null);
    setError("");
    setProgress(0);
  }
  const loading = attempt > 0 && !media && !error;
  return (
    <span className="project-media">
      <button
        className="media-trigger"
        type="button"
        aria-expanded={!!attempt}
        title={reference}
        onClick={() => {
          if (attempt && !error) reset();
          else {
            setMedia(null);
            setError("");
            setProgress(0);
            setAttempt((n) => n + 1);
          }
        }}
      >
        <Icon size={18} />
        <span>{title}</span>
        <small>
          {error
            ? "重试"
            : loading
              ? progress + "% · 取消"
              : media
                ? "收起"
                : kind === "video"
                  ? "加载视频"
                  : "查看图片"}
        </small>
      </button>
      {error && (
        <span className="media-error" role="status">
          {error}
        </span>
      )}
      {media &&
        !error &&
        (kind === "video" ? (
          <video
            src={media}
            controls
            playsInline
            preload="metadata"
            aria-label={title}
            onError={() => setError("此设备无法播放该视频编码。")}
          />
        ) : (
          <img
            src={media}
            alt={title}
            onError={() => setError("此设备无法解码该图片。")}
          />
        ))}
    </span>
  );
}
