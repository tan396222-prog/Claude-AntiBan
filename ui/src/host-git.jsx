import React, { useEffect, useRef, useState } from "react";
import { LoaderCircle, ShieldCheck } from "lucide-react";
import { api } from "./api";
import { isRemoteClient } from "./client-context.mjs";
import { Modal } from "./views";

export function HostGitPreparation({ onClose, onReady }) {
  const machine = isRemoteClient() ? "云端主机" : "本机";
  const [state, setState] = useState(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");
  const alive = useRef(true);
  const check = async (enable = false) => {
    setBusy(true);
    setError("");
    try {
      const next = await api(
        "/host/git",
        enable ? { confirm: true } : undefined,
      );
      if (!alive.current) return;
      setState(next);
      if (next.ready) onReady();
    } catch (e) {
      if (alive.current) setError(e.message);
    } finally {
      if (alive.current) setBusy(false);
    }
  };
  useEffect(() => {
    alive.current = true;
    check();
    return () => {
      alive.current = false;
    };
  }, []);
  // The welcome page shows progress; open a dialog only when input is needed.
  if (!state && !error) return null;
  return (
    <Modal
      title={`启动前，保护${machine} Git`}
      onClose={busy ? undefined : onClose}
      footer={
        <>
          <button disabled={busy} onClick={onClose}>
            暂不启动
          </button>
          <button disabled={busy} onClick={() => check()}>
            重新检查
          </button>
          {state?.can_enable && (
            <button
              className="primary"
              disabled={busy}
              onClick={() => check(true)}
            >
              {busy ? (
                <LoaderCircle size={16} className="spin" />
              ) : (
                <ShieldCheck size={16} />
              )}
              启用保护并继续
            </button>
          )}
        </>
      }
    >
      <div className="modal-body host-git-preparation">
        {busy && !state ? (
          <p role="status">正在检查{machine} Git，尚未修改任何设置…</p>
        ) : (
          <>
            <p>{state?.message}</p>
            {state?.can_enable && (
              <>
                <p>
                  Claude 可以修改共享文件。启用这项保护后，{machine} Git
                  会忽略未经你明确指定的裸仓库，避免共享目录里的配置在{machine}
                  上执行命令。
                </p>
                <p>
                  点击“启用保护并继续”将修改{machine}当前用户的一项全局 Git 设置：
                </p>
                <pre>git config --global safe.bareRepository explicit</pre>
                <p>
                  普通的 <code>.git</code>{" "}
                  项目、提交记录和远程地址保持不变。使用裸仓库时，需要通过{" "}
                  <code>--git-dir</code> 明确指定。设置会影响{machine}其他使用 Git
                  的工具。
                </p>
              </>
            )}
            {!state?.can_enable && !error && (
              <p>完成安装或修复后点击“重新检查”，无需重新配置代理。</p>
            )}
          </>
        )}
        {error && (
          <p className="start-error" role="alert">
            {error}
          </p>
        )}
      </div>
    </Modal>
  );
}
