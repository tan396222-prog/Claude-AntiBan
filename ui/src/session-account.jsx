import React, { useEffect, useRef, useState } from "react";
import { InlineSelect } from "./inline-select";
import "./session-account.css";

const failures = {rate_limit:"请求受到限流，请核对额度后选择等待或接续",authentication_failed:"账号登录失效，请重新登录",account_on_hold:"账号受限，自动接续已停止",overloaded:"服务暂时过载，保留原账号",server_error:"服务暂时异常，保留原账号"};

// Desktop and mobile use the same session operation and durable request ID.
export function SessionAccount({ id, request, onChanged, compact = false }) {
  const [open,setOpen]=useState(false), [view,setView]=useState(null), [busy,setBusy]=useState(false), [error,setError]=useState("");
  const [target,setTarget]=useState(""), [confirmed,setConfirmed]=useState(false), [continueTask,setContinueTask]=useState(false);
  const pending=useRef(null), current=useRef({request,onChanged}); current.current={request,onChanged};
  const terminalID=useRef(null);
  const path="/sessions/"+id+"/account";
  useEffect(() => {
    let live=true,timer;
    const load=async()=>{
      try {
        const result=await current.current.request(path,{body:{action:"inspect"}});
        if(live) {
          setView(result); setTarget(previous=>result.candidates.some(a=>a.id===previous)?previous:(result.candidates[0]?.id || ""));
          const next=result.session.desktop_terminal_id;
          if(terminalID.current && next && next!==terminalID.current)current.current.onChanged?.(result.session);
          terminalID.current=next;
        }
      } catch(e) { if(live && open) setError(e.message); }
      if(live) timer=setTimeout(load,open?2000:10000);
    };
    void load(); return()=>{live=false;clearTimeout(timer);};
  },[id,path,open]);
  const change=async(body)=>{
    if(busy)return; setBusy(true);setError("");
    try {
      const result=await current.current.request(path,{body}); setView(result);current.current.onChanged?.(result.session);
      if(body.action==="switch") {pending.current=null;setConfirmed(false);}
    } catch(e){setError(e.message);} finally{setBusy(false);}
  };
  if(!view?.supported)return null;
  const session=view.session, sw=session.account_switch;
  const switching=sw && sw.phase!=="completed";
  return <section className={`session-account ${compact?"session-account--compact":""}`} aria-label="会话账号">
    <button type="button" className="quiet-button" aria-expanded={open} onClick={()=>setOpen(v=>!v)}>
      {session.account_label || "Claude 账号"} · {session.account_failure?"需要处理":switching?"正在交接":"会话绑定"}
    </button>
    {open && <div className="session-account-options">
      <p>当前账号：{session.account_label || session.account_id}。会话重连会保持此账号。</p>
      {session.account_failure && <p role="status">{failures[session.account_failure] || "本轮发生错误，原会话已保留"}</p>}
      {sw && <p role="status">{sw.phase==="completed"?"已接续至":"接续目标："}{sw.target_label}{sw.error && ` · ${sw.error}`}</p>}
      <label>此会话切换授权<InlineSelect aria-label="此会话切换授权" disabled={busy || switching} value={view.switch_policy} onValueChange={policy=>change({action:"policy",policy})}>
        <option value="ask">每次询问</option><option value="allow">额度耗尽后自动接续</option><option value="wait">始终等待原账号</option>
      </InlineSelect></label>
      {!switching && <>
        <label>接续账号<InlineSelect aria-label="接续账号" value={target} disabled={busy || !view.candidates.length} onValueChange={value=>{setTarget(value);setConfirmed(false);pending.current=null;}}>
          {view.candidates.length?view.candidates.map(a=><option key={a.id} value={a.id}>{a.label}</option>):<option value="">无可用账号</option>}
        </InlineSelect></label>
        {!view.candidates.length && <p>在账号设置中，将允许共享会话历史的账号设为同一接续分组。</p>}
        <p>历史将由目标账号继续处理，缓存可能重建。执行中的工具、后台任务和未确认结果会阻止切换。</p>
        <label className="check-line"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)} />允许将此会话历史交给所选账号</label>
        <label className="check-line"><input type="checkbox" checked={continueTask} onChange={e=>{setContinueTask(e.target.checked);pending.current=null;}} />恢复后继续未完成的任务</label>
        <button type="button" disabled={busy || !target || !confirmed} onClick={()=>{
          pending.current ||= {action:"switch",target,request_id:crypto.randomUUID(),source_id:view.source_id,confirm:true,continue:continueTask};
          void change(pending.current);
        }}>{busy?"正在交接…":"切换并恢复历史"}</button>
      </>}
      {switching && sw.phase!=="starting" && <button disabled={busy} onClick={()=>change({action:"switch",request_id:sw.id,source_id:view.source_id,target,confirm:true})}>重试账号交接</button>}
      {switching && sw.phase==="preparing" && <button disabled={busy} onClick={()=>change({action:"cancel",request_id:sw.id})}>取消交接，保留原账号</button>}
      {switching && sw.phase==="starting" && <p>正在等待目标账号连接。请保留当前页面，或从会话列表重新打开。</p>}
      {error && <p role="alert">{error}</p>}
    </div>}
  </section>;
}
