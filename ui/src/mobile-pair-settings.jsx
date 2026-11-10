import React, { useEffect, useRef, useState } from "react";
import { api, writeClipboard } from "./api";
import { QrCode } from "lucide-react";

export function MobilePairSettings({ relay, savedRelay, onChange, onConfirm }) {
  const [custom, setCustom] = useState(Boolean(relay));
  const [token, setToken] = useState("");
  const [code, setCode] = useState(null);
  const [paired, setPaired] = useState(false);
  const [device, setDevice] = useState(null);
  const [power, setPower] = useState(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const statusRevision = useRef(0);
  useEffect(() => {
    let active = true;
    const check = async () => {
      const revision = statusRevision.current;
      try {
        const state = await api("/mobile-pair");
        if (!active || revision !== statusRevision.current) return;
        setPaired(state.paired);
        setDevice(state.paired ? state.device ?? null : null);
        setPower(state.paired ? state.keep_awake ?? null : null);
        if (state.paired) setCode(null);
        else if (code && Date.now() >= Date.parse(code.expires)) {
          setCode(null);
          setNotice("二维码已过期，请重新生成。");
        } else if (code && state.status.includes("未完成")) {
          setCode(null);
          setNotice(state.status);
        }
      } catch (e) {
        if (active && revision === statusRevision.current) setNotice(e.message);
      }
    };
    check();
    const timer = setInterval(check, code ? 1500 : 3000);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [code]);
  async function generate() {
    setBusy(true);
    setNotice("");
    setCode(null);
    try {
      setCode(
        await api("/mobile-pair", {
          relay: savedRelay,
          admin: custom ? token : "",
        }),
      );
    } catch (e) {
      setNotice(e.message);
    } finally {
      setBusy(false);
    }
  }
  async function remove() {
    if (
      !(await onConfirm({
        title: "移除配对手机？",
        body: device?.model
          ? `移除 ${device.model} 后，这部手机需要重新扫码才能访问工作台。`
          : "这部手机需要重新扫码才能访问工作台。",
        confirm: "移除手机",
      }))
    )
      return;
    setBusy(true);
    try {
      await api("/mobile-pair", {}, "DELETE");
      statusRevision.current++;
      setPaired(false);
      setDevice(null);
      setPower(null);
      setNotice("");
    } catch (e) {
      setNotice(e.message);
    } finally {
      setBusy(false);
    }
  }
  // Center the QR and its actions. Relay controls use the same theme below.
  return (
    <section className="remote-link-settings" aria-label="远程连接">
      <div className="section-title">
        <h3>远程连接</h3>
        <p>
          手机配对后可以打开远程浏览器和 Claude Desktop，也可以继续 Claude Code 会话、处理工具审批。
        </p>
      </div>
      <section className="remote-pair-card" aria-label="手机配对">
        <h4>{paired ? "手机已配对" : "用手机连接 Cage"}</h4>
        {paired && (
          <p className="remote-pair-device" aria-label="已配对手机">
            {device?.model ? (
              <>
                <strong>{device.model}</strong>
                <span>{device.platform === "ios" ? "iOS" : "Android"}</span>
              </>
            ) : (
              <span>型号待获取，请更新手机端 Cage 后重新连接。</span>
            )}
          </p>
        )}
        {paired ? (
          <p role="status">在手机上点「重新连接」即可打开工作台。</p>
        ) : (
          <p className="remote-pair-instruction">
            打开手机 Cage，选择「连接电脑」，用「扫码连接」扫描二维码，或选择「使用配对码」粘贴电脑上复制的配对码。
          </p>
        )}
        {paired && power?.active && <p className="note">远程待机已请求主机保持运行，屏幕可正常熄灭。移除配对或结束后台服务后恢复原系统休眠策略。</p>}
        {paired && power?.error && <p role="alert">主机防休眠未生效：{power.error}</p>}
        {code && (
          <div className="remote-pair-code">
            <img
              src={code.image}
              alt="手机配对二维码"
              width="320"
              height="320"
            />
            <p className="field-help">二维码约两分钟有效，过期后可刷新。</p>
          </div>
        )}
        {!code && !paired && (
          <div className="remote-pair-placeholder" aria-hidden="true">
            <QrCode size={64} strokeWidth={1.25} />
          </div>
        )}
        <div className="remote-pair-actions">
          {paired ? (
            <button type="button" disabled={busy} onClick={remove}>
              移除配对手机
            </button>
          ) : (
            <button
              type="button"
              className="primary"
              disabled={busy || relay !== savedRelay || (custom && !relay)}
              onClick={generate}
            >
              <QrCode size={18} />
              {busy ? "正在生成…" : code ? "刷新二维码" : "生成配对二维码"}
            </button>
          )}
          {code && (
            <button
              type="button"
              onClick={async () => {
                try {
                  await writeClipboard(code.manual);
                  setNotice("一次性配对码已复制，可在手机上粘贴。");
                } catch {
                  setNotice("复制失败，请使用二维码。");
                }
              }}
            >
              复制配对码
            </button>
          )}
        </div>
        {!paired && (
          <p className="remote-pair-privacy">
            只将二维码或配对码交给你希望授权的手机。
          </p>
        )}
        {notice && <p role="status">{notice}</p>}
      </section>
      <section className="remote-relay-settings" aria-label="中继设置">
        <div className="remote-relay-row">
          <div>
            <h4>中继服务</h4>
            <p className="field-help">
              通过中继连接手机与电脑，可使用官方或自建服务。
            </p>
          </div>
          <div
            className="remote-relay-options"
            role="group"
            aria-label="中继服务"
          >
            <button
              type="button"
              aria-pressed={!custom}
              onClick={() => {
                setCustom(false);
                setToken("");
                onChange("");
                setCode(null);
              }}
            >
              Cage 官方中继
            </button>
            <button
              type="button"
              aria-pressed={custom}
              onClick={() => setCustom(true)}
            >
              自建中继
            </button>
          </div>
        </div>
        {custom && (
          <>
            <label>
              自建中继地址
              <input
                value={relay}
                placeholder="https://relay.example.com"
                onChange={(e) => {
                  onChange(e.target.value);
                  setToken("");
                  setCode(null);
                }}
              />
            </label>
            <label>
              管理令牌（可选）
              <input
                type="password"
                autoComplete="off"
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
            </label>
          </>
        )}
        {relay !== savedRelay && (
          <small>保存设置后，可使用新中继生成配对码。</small>
        )}
      </section>
    </section>
  );
}
