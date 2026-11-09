import { api, native, uploadFile } from "./api";

export const shellFilePaths = (paths) => paths.map(path => `'${path.replaceAll("'", "'\\''")}'`).join(" ") + " ";

// Both native selection handles and browser File objects use the same guest
// upload window. A failed batch is canceled before it can be delivered.
export async function sendFiles(content, target, id, { valid = () => true, onProgress } = {}) {
  const check = () => { if (!valid()) throw new Error("文件接收目标已切换，请返回原位置重试"); };
  check();
  const files = content.files || [];
  if (!content.fileSelection && (!files.length || files.length > 100 || files.some(file => file.size > 2 * 1024 ** 3)))
    throw new Error("每个文件不能超过 2 GB；一次最多 100 个文件。");
  const request = await api("/input/files/start", { target, id, kind: content.kind || "paste", x: content.x, y: content.y });
  const paths = [];
  try {
    check();
    if (content.fileSelection) {
      const result = await native("uploadHostFiles", { target, id: request.id, selection: content.fileSelection });
      paths.push(...(result.paths || []));
    } else {
      for (const file of files) {
        check();
        const result = await uploadFile(`/input/files/${target}/${request.id}?${new URLSearchParams({ name: file.name })}`, file, { onProgress });
        if (result.path) paths.push(result.path);
      }
    }
    check();
    await api("/input/files/finish", { target, id: request.id });
    return { paths };
  } catch (error) {
    await api("/input/files/cancel", { target, id: request.id }).catch(() => {});
    throw error;
  }
}
