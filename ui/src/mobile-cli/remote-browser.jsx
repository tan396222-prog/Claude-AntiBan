import React, { useEffect, useRef, useState } from "react";
import { createPortal, flushSync } from "react-dom";
import { ArrowDownLeft, ArrowLeft, ArrowRight, BookOpen, Check, CornerDownLeft, Delete, Globe2, Keyboard, LoaderCircle, Maximize, Monitor, RotateCw, ZoomIn, X } from "lucide-react";
import { PhoneFavorites } from "./phone-favorites";
import { connectDisplay, framePoint } from "./browser-session.mjs";
import "./remote-browser.css";

function Tool({icon: Icon, label, ...props}) {
  return <button type="button" className="icon-button" aria-label={label} title={label} {...props}>
    <Icon size={19} aria-hidden="true" />
  </button>;
}

function AddressBar({frame, goToAddress, onNotice}) {
  const [address, setAddress] = useState(null);
  const [invalid, setInvalid] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const submission = useRef(false);
  const current = frame?.url === "about:blank" ? "" : frame?.url || "";
  const navigate = async event => {
    event.preventDefault();
    if (submission.current) return;
    const field = event.currentTarget.elements[0];
    const raw = field.value.trim();
    let url;
    try {
      if (!raw) throw new Error();
      url = new URL(raw.includes("://") ? raw : "https://" + raw);
      if (!["https:", "http:"].includes(url.protocol) || url.username || url.password || url.href.length > 16384) throw new Error();
    } catch {
      setInvalid(true); onNotice("请输入有效的 HTTP 或 HTTPS 网址。");
      return;
    }
    submission.current = true; setSubmitting(true); onNotice("");
    try {
      if (await goToAddress(url.href) && field.value.trim() === raw) {
        field.blur();
        setAddress(null);
      }
    } catch (error) {
      onNotice(error.message);
    } finally {
      submission.current = false; setSubmitting(false);
    }
  };
  return <form className="remote-address" onSubmit={navigate}>
        <Globe2 size={16} aria-hidden="true" />
        <input aria-label="远程网页地址" aria-invalid={invalid} inputMode="url" enterKeyHint="go" autoCapitalize="none"
          autoCorrect="off" autoComplete="off" spellCheck={false} placeholder="输入网址" disabled={!frame}
          value={address ?? current} onFocus={event => event.target.select()}
          onChange={event => {setAddress(event.target.value); setInvalid(false); onNotice("");}}
          onKeyDown={event => {if (event.key === "Escape") {setAddress(null); setInvalid(false); onNotice(""); event.target.blur();}}} />
        <button type="submit" aria-label="前往网址" aria-busy={submitting} disabled={!frame || submitting}>{submitting ? <LoaderCircle className="spin" size={17} /> : <ArrowDownLeft size={17} />}</button>
      </form>;
}

function RemoteKeyboard({frame, busy, send, onClose}) {
  const [draft, setDraft] = useState("");
  const insert = async event => {
    event.preventDefault();
    if (!draft || !frame.focus || busy || new TextEncoder().encode(draft).length > 8192) return;
    if (await send({type: "text", focus: frame.focus, text: draft}, frame)) setDraft("");
  };
  const tooLong = new TextEncoder().encode(draft).length > 8192;
  return <form className="remote-keyboard" onSubmit={insert}>
    <input autoFocus type={frame.password ? "password" : "text"} aria-label="待插入文字" placeholder={frame.password ? "输入密码" : "输入文字"}
      autoComplete="off" autoCapitalize="none" spellCheck={false} autoCorrect="off" enterKeyHint="done"
      maxLength={8192} value={draft} onChange={e => setDraft(e.target.value)}
      onKeyDown={e => {if (e.key === "Enter" && (e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229)) e.preventDefault();}} />
    <button type="submit" className="icon-button" aria-label="插入文字" title="插入文字" disabled={busy || !draft || tooLong}><Check size={19} /></button>
    <Tool label="删除前一字符" icon={Delete} disabled={busy || !!draft} onClick={() => send({type: "key", key: "Backspace", focus: frame.focus}, frame)} />
    <Tool label="发送回车" icon={CornerDownLeft} disabled={busy || !!draft} onClick={() => send({type: "key", key: "Enter", focus: frame.focus}, frame)} />
    <Tool label="收起输入框" icon={X} onClick={onClose} />
    {tooLong && <p role="alert" className="remote-note">单次输入最多 8192 字节，请分段输入。</p>}
  </form>;
}

// Login windows accept one printable ASCII key at a time. No form draft,
// clipboard, text insertion or implicit Enter is used for these fields.
function LoginKeyboard({frame, busy, session, onClose}) {
  const keys = useRef([]);
  const sending = useRef(false);
  const [sequence, wake] = useState(0);
  const [notice, setNotice] = useState("");
  useEffect(() => () => { keys.current.length = 0; }, []);
  useEffect(() => {
    if (busy || sending.current || !keys.current.length || !session?.canSend(frame, frame.focus)) return;
    const key = keys.current.shift();
    sending.current = true;
    void session.send({type:"key", key, focus:frame.focus}, frame).then(ok => {
      if (!ok) {
        keys.current.length = 0;
        setNotice("输入焦点或连接已变化，请检查远程输入框。");
      }
      sending.current = false;
      if (keys.current.length) wake(value => value + 1);
    });
  }, [frame, busy, session, sequence]);
  const press = key => {
    if (keys.current.length >= 32) {setNotice("请稍候，前面的按键正在发送。"); return;}
    keys.current.push(key); setNotice(""); wake(value => value + 1);
  };
  return <div className="remote-keyboard">
    <input type="password" autoFocus aria-label="逐键登录输入" placeholder="逐键输入英文或符号"
      autoComplete="off" autoCapitalize="none" spellCheck={false} autoCorrect="off" enterKeyHint="done"
      onPaste={e => e.preventDefault()} onDrop={e => e.preventDefault()}
      onInput={e => {
        const value = e.currentTarget.value;
        e.currentTarget.value = "";
        if (e.nativeEvent.inputType === "deleteContentBackward") {press("Backspace"); return;}
        if (!e.nativeEvent.isComposing && /^[ -~]$/.test(value)) press(value);
        else if (value) setNotice("登录键盘支持逐个输入英文、数字和符号。");
      }}
      onKeyDown={e => {
        if (["Backspace","Tab","Enter"].includes(e.key) && !e.nativeEvent.isComposing) {
          e.preventDefault(); press(e.key);
        }
      }} />
    <Tool label="删除前一字符" icon={Delete} onClick={() => press("Backspace")} />
    <Tool label="发送回车" icon={CornerDownLeft} onClick={() => press("Enter")} />
    <Tool label="收起输入框" icon={X} onClick={onClose} />
    {notice && <p className="remote-note" role="alert">{notice}</p>}
  </div>;
}

function remoteHost(url) {
  try { return new URL(url).hostname; } catch { return ""; }
}

export function RemoteDisplay({visible, onBack, onReopen, id, tool, start, initialURL, controls, addressSlot}) {
  const desktop = tool === "desktop";
  const name = desktop ? "Claude Desktop" : tool === "claude" ? "Claude.ai" : "远程浏览器";
  const [reading, setReading] = useState(false);
  const [showAddress, setShowAddress] = useState(tool === "browser");
  const [addressNotice, setAddressNotice] = useState("");
  const [favorites, setFavorites] = useState(false);
  const surface = useRef(null);
  const stage = useRef(null);
  const gesture = useRef(null);
  const [state, setState] = useState({});
  const [keyboard, setKeyboard] = useState("");
  const preview = location.protocol === "cage-preview:";
  const frame = visible ? state.frame : null;
  // The guest identifies each editable element within its document. The
  // separate frame token changes on resize and protects input coordinates.
  const login = desktop && frame?.window === "login";
  const focus = desktop && frame?.password && !login ? "" : frame?.focus || "";
  const typing = !!focus && keyboard === focus;
  const hostname = tool === "claude" && frame ? remoteHost(frame.url) : "";
  useEffect(() => {
    if (reading && stage.current) stage.current.scrollLeft = (stage.current.scrollWidth - stage.current.clientWidth) / 2;
  }, [reading]);
  useEffect(() => {
    if (preview) return;
    const session = connectDisplay(stage.current, next => {
      if (!next.frame) {gesture.current = null; setKeyboard(""); setFavorites(false);}
      setState(next);
    }, id, start, tool, initialURL);
    controls.current = session;
    const visibility = () => {
      gesture.current = null; setKeyboard("");
      session.visibility(!document.hidden);
    };
    const pageHide = () => {gesture.current = null; setKeyboard(""); session.visibility(false);};
    document.addEventListener("visibilitychange", visibility);
    const offline = () => {gesture.current = null; session.offline();};
    window.addEventListener("offline", offline);
    window.addEventListener("pagehide", pageHide);
    window.addEventListener("pageshow", visibility);
    visibility();
    return () => {
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("offline", offline);
      window.removeEventListener("pagehide", pageHide);
      window.removeEventListener("pageshow", visibility);
      session.close();
      controls.current = null;
    };
  }, [id, start, tool, initialURL, preview, controls]);

  const send = (payload, expected) => controls.current?.send(payload, expected);
  const point = event => framePoint(surface.current.getBoundingClientRect(), frame, event.clientX, event.clientY);
  const down = event => {
    if (favorites || !frame || (desktop && state.busy) || !event.isPrimary || event.button !== 0) return;
    const location = point(event);
    if (!location) return;
    if (!desktop) controls.current.cancelScroll();
    event.currentTarget.setPointerCapture(event.pointerId);
    gesture.current = {pointer: event.pointerId, location, frame, x:event.clientX, y:event.clientY,
      lastX:0, lastY:0, dragged:false, left:stage.current.scrollLeft, top:stage.current.scrollTop};
  };
  const drag = (start, event) => {
    const dx = start.x - event.clientX, dy = start.y - event.clientY;
    if (!start.dragged && Math.hypot(dx, dy) < 6) return;
    start.dragged = true;
    if (reading) {
      stage.current.scrollLeft = start.left + dx;
      stage.current.scrollTop = start.top + dy;
    } else if (!desktop) {
      const {x, y, scale} = start.location;
      const totalX = Math.round(dx / scale), totalY = Math.round(dy / scale);
      controls.current.scroll(start.frame, {x, y, dx:totalX - start.lastX, dy:totalY - start.lastY});
      start.lastX = totalX; start.lastY = totalY;
    }
  };
  const move = event => {
    const start = gesture.current;
    if (start?.pointer === event.pointerId) drag(start, event);
  };
  const up = event => {
    const start = gesture.current;
    if (start?.pointer !== event.pointerId) return;
    gesture.current = null;
    drag(start, event);
    const {x, y, scale} = start.location;
    if (!start.dragged) void send({type:"tap", x, y}, start.frame);
    else if (desktop && !reading) {
      const delta = value => Math.max(-3200, Math.min(3200, Math.round(value / scale)));
      void send({type:"scroll", x, y, dx:delta(start.x - event.clientX), dy:delta(start.y - event.clientY)}, start.frame);
    }
  };
  const wheel = event => {
    if (favorites || !frame || (desktop && state.busy)) return;
    if (reading) {
      stage.current.scrollBy(event.deltaX, event.deltaY); return;
    }
    const location = point(event);
    if (!location) return;
    const units = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? frame.height : 1;
    const delta = value => Math.max(-3200, Math.min(3200, Math.round(value * units / location.scale)));
    const input = {x:location.x, y:location.y, dx:delta(event.deltaX), dy:delta(event.deltaY)};
    if (desktop) void send({type:"scroll", ...input}, frame);
    else controls.current.scroll(frame, input);
  };
  return <section className="remote-browser" aria-label={name}>
    {!desktop && addressSlot && frame && (showAddress
      ? createPortal(<AddressBar frame={frame} goToAddress={async url => {const opened = await controls.current.navigate(url); if (opened) setFavorites(false); return opened;}} onNotice={setAddressNotice} />, addressSlot)
      : hostname && hostname !== "claude.ai" && createPortal(<span className="remote-domain" title={hostname}><Globe2 size={14} />{hostname}</span>, addressSlot))}
    {frame && state.homeNotice && <p className="remote-home-note" role="status">{state.homeNotice}</p>}
    {frame && showAddress && addressNotice && <p className="remote-note" role="alert">{addressNotice}</p>}
    <div ref={stage} className="remote-stage" onPointerDown={down} onPointerMove={move} onPointerUp={up}
      onPointerCancel={() => {gesture.current = null; controls.current?.cancelScroll();}} onWheel={wheel} onContextMenu={e => {if (!favorites) e.preventDefault();}}>
      {frame && favorites ? <PhoneFavorites frame={frame} navigate={url => controls.current.navigate(url)} onClose={() => setFavorites(false)}/> :
       frame ? <div className={reading ? "display-surface reading" : "display-surface"} ref={surface}>
        <img className="remote-screen" alt={desktop ? "远程 Desktop 画面" : "远程浏览器画面"} src={frame.image} draggable={false} />
      </div> :
        <div className="remote-empty" role={state.error ? "alert" : "status"}>
          {preview ? <><Globe2 aria-hidden="true" /><p>连接真实电脑后，即可使用{name}。</p></> :
            state.error ? <><p>{state.error}</p>
              {state.uncertain && <p className="note">输入结果待确认，请先查看最新画面。</p>}
              <button className="action secondary" onClick={() => {
                setKeyboard(""); setState({});
                if (state.reopen) onReopen();
                else controls.current.refresh();
              }}>{state.reopen ? "重新打开" : "重新读取画面"}</button></> :
              <><LoaderCircle className="spin" aria-hidden="true" /><p>{state.waiting ? `正在启动远程${desktop ? " Claude Desktop" : "浏览器"}…` : "正在读取远程画面…"}</p></>}
        </div>}
    </div>
    {typing ? (login
      ? <LoginKeyboard key={focus} frame={frame} busy={state.busy} session={controls.current} onClose={() => setKeyboard("")} />
      : <RemoteKeyboard key={focus} frame={frame} busy={state.busy} send={send} onClose={() => setKeyboard("")} />) :
    <nav className="remote-tools" aria-label={desktop ? "远程桌面操作" : "远程网页操作"}>
      {desktop ? <>
        <Tool label="断开远程桌面" icon={X} onClick={onBack} />
        <button type="button" className="action" disabled={!frame} aria-pressed={reading} onClick={() => setReading(!reading)}>
          {reading ? <Maximize size={17} /> : <ZoomIn size={17} />}{reading ? "全幅" : "放大阅读"}
        </button>
        {!frame?.shared && frame?.login_supported !== false && <button type="button" className="action" disabled={!frame || state.busy}
          onClick={() => send({type:login ? "desktop" : "login"}, frame)}>
          {login ? <Monitor size={17} /> : <Globe2 size={17} />}{login ? "返回 Desktop" : "登录窗口"}
        </button>}
      </> : <>
        <Tool label="关闭远程浏览器" icon={X} onClick={onBack} />
        <Tool label="上一页" icon={ArrowLeft} disabled={!frame || state.busy || favorites} onClick={() => send({type:"back"}, frame)} />
        <Tool label="下一页" icon={ArrowRight} disabled={!frame || state.busy || favorites} onClick={() => send({type:"forward"}, frame)} />
        {tool === "claude" && <Tool label="网页地址" aria-pressed={showAddress} icon={Globe2} disabled={!frame} onClick={() => {setAddressNotice(""); setShowAddress(!showAddress);}} />}
        {tool === "browser" && <Tool label="收藏夹" icon={BookOpen} disabled={!frame} aria-expanded={favorites}
          onClick={() => {gesture.current = null; controls.current.cancelScroll(); setFavorites(!favorites);}} />}
        <Tool label="重新加载网页" icon={RotateCw} disabled={!frame || state.busy || favorites} onClick={() => send({type:"reload"}, frame)} />
      </>}
      <Tool label="输入文字" icon={Keyboard} disabled={!focus || state.busy || favorites}
        onClick={() => flushSync(() => setKeyboard(focus))} />
    </nav>}
    {desktop && frame?.login_supported === false && <p className="remote-home-note" role="status">需要登录时，请在电脑的 Cage 中完成 Claude Desktop 登录，手机会继续使用同一登录状态。</p>}
  </section>;
}
