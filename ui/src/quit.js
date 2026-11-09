// Only the stop requested by this window can authorize its automatic exit.
export function stopExitState(status, jobId) {
  const job = status.job;
  if (!jobId || job?.id !== jobId || job.kind !== "stop" || job.running)
    return "waiting";
  if (job.error) return "failed";
  return status.vm_running ? "waiting" : "exit";
}

// Exit owns its wait loop: boot failures and a stopped UI poll must not strand it.
export async function stopAndExit({
  stop,
  readStatus,
  exit,
  isCancelled,
  onStatus,
  delay = () => new Promise((resolve) => setTimeout(resolve, 350)),
}) {
  const job = await stop();
  if (!job.id) throw new Error("停止请求未返回任务编号，请重试。");
  while (!isCancelled()) {
    const status = await readStatus();
    if (isCancelled()) return false;
    onStatus(status);
    const state = stopExitState(status, job.id);
    if (state === "failed") throw new Error(status.job.error);
    if (state === "exit") {
      await exit();
      return true;
    }
    await delay();
  }
  return false;
}
