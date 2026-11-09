import { api, native, nativeAvailable } from "./api";

export const imageLimit = 20 * 1024 * 1024;
const imageTypes = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
]);

export function transferFiles(data) {
  const files = Array.from(data?.files || []);
  if (files.length) return files;
  return Array.from(data?.items || [])
    .filter((item) => item.kind === "file")
    .map((item) => item.getAsFile())
    .filter(Boolean);
}

export function hasFiles(data) {
  return (
    Array.from(data?.types || []).includes("Files") ||
    transferFiles(data).length > 0
  );
}

export async function imagePayload(file) {
  if (!imageTypes.has(file.type))
    throw new Error("请选择 PNG、JPEG、WebP 或 GIF 图片");
  if (!file.size || file.size > imageLimit)
    throw new Error("每张图片不能超过 20 MB");
  const url = URL.createObjectURL(file);
  try {
    const image = new Image();
    await new Promise((resolve, reject) => {
      image.onload = resolve;
      image.onerror = () =>
        reject(new Error("图片无法读取，请重新复制或选择图片"));
      image.src = url;
    });
    if (image.naturalWidth * image.naturalHeight > 40_000_000)
      throw new Error("图片不能超过 4000 万像素");
    // Normalize host bitmap formats and animated images to one PNG frame.
    const canvas = document.createElement("canvas");
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    canvas.getContext("2d").drawImage(image, 0, 0);
    const png = await new Promise((resolve) =>
      canvas.toBlob(resolve, "image/png"),
    );
    if (!png || png.size > imageLimit)
      throw new Error("转换后的图片超过 20 MB，请缩小图片后重试");
    return await new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve({ png: reader.result.split(",")[1] });
      reader.onerror = () => reject(new Error("图片读取失败"));
      reader.readAsDataURL(png);
    });
  } finally {
    URL.revokeObjectURL(url);
  }
}

export async function readClipboardContent() {
  if (nativeAvailable()) {
    const value = await native("clipboardReadContent");
    if (
      !value ||
      (typeof value.png !== "string" && typeof value.text !== "string" && typeof value.fileSelection !== "string")
    )
      throw new Error("剪贴板读取失败，请重新打开新版 Cage 后重试");
    return value;
  }
  if (navigator.clipboard.read) {
    const items = await navigator.clipboard.read();
    for (const item of items) {
      const type = item.types.find((type) => imageTypes.has(type));
      if (type) return imagePayload(await item.getType(type));
    }
    for (const item of items) {
      if (item.types.includes("text/plain"))
        return { text: await (await item.getType("text/plain")).text() };
    }
    throw new Error("剪贴板中没有可粘贴的图片或文本");
  }
  return { text: await navigator.clipboard.readText() };
}

export const sendImage = (payload, target, id) =>
  api("/input/image", { ...payload, target, id });
