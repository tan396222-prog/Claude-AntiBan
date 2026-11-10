import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

for (const viewport of [{ width: 1280, height: 900 }, { width: 1920, height: 1080 }, { width: 390, height: 700 }]) {
  const nativeCalls = [], inputs = [], closed = [];
  let finish = false, sessionID = 0, frameMime = "image/jpeg";
  const fixture = await createWorkbench({
    viewport,
    nativeRequest: (message) => nativeCalls.push(message),
    async beforeNavigate(page, state) {
      state.status.ready = true; state.status.vm_running = true;
      const images = await page.evaluate(() => {
        const canvas = document.createElement("canvas");
        canvas.width = 1920; canvas.height = 1200;
        const ctx = canvas.getContext("2d");
        ctx.fillStyle = "#526482"; ctx.fillRect(0, 0, 1920, 1200);
        ctx.fillStyle = "#ddd"; ctx.beginPath(); ctx.arc(960, 1000, 32, 0, Math.PI*2); ctx.fill();
        ctx.fillStyle = "white"; ctx.font = "20px sans-serif"; ctx.textAlign = "center";
        ctx.fillText("Cage", 960, 1050);
        ctx.fillStyle = "#ffffff88"; ctx.fillRect(880, 1070, 160, 30);
        ctx.fillStyle = "white"; ctx.font = "14px sans-serif";
        ctx.fillText("Enter Password", 960, 1091);
        return Object.fromEntries(["image/jpeg", "image/png"].map((m) => [m, canvas.toDataURL(m).split(",")[1]]));
      });
      await page.route("**/api/account", (r) => r.fulfill({json:{exists:true, unlocked:true, trusted_local:true, guest_system:"macos"}}));
      await page.route("**/api/terminals", (r) => r.fulfill({json:[]}));
      await page.route("**/api/macos/login", (r) => {
        const q = r.request().postDataJSON();
        if (q.action === "open") return r.fulfill({json:{session:"trial-" + (++sessionID)}});
        if (q.action === "close") { closed.push(q.session); return r.fulfill({json:{closed:true}}); }
        if (q.action === "frame") return r.fulfill({json:finish ? {closed:true} :
          {image:images[frameMime], ...(frameMime === "image/jpeg" ? {mime:frameMime} : {})}});
        inputs.push(q);
        return r.fulfill({json:{}});
      });
    },
  });
  const { page, browser, errors } = fixture;
  async function waitInput(count) {
    for (let i=0; inputs.length < count && i<150; i++) await new Promise((r) => setTimeout(r, 20));
    assert.equal(inputs.length, count);
  }
  try {
    if (viewport.width < 600) await page.getByRole("button",{name:"收起侧栏",exact:true}).click();
    const trigger=page.getByRole("button",{name:"登录客机账户"});
    await trigger.click({timeout:7000});
    const screen=page.getByRole("dialog",{name:"登录虚拟 Mac"});
    const img=screen.getByRole("img");
    const input=screen.getByLabel(/^客机登录键盘/);
    await img.waitFor();
    await page.waitForFunction(() => document.activeElement?.classList.contains("mac-login-keyboard") &&
      document.querySelector(".mac-login-canvas img")?.naturalWidth === 1920);
    assert.equal(await screen.evaluate((el) => el.parentElement === document.body),true);
    assert.equal(await page.locator("#root").evaluate((el) => el.inert),true);
    const panel=await screen.locator(".mac-login-panel").boundingBox();
    assert.ok(panel.width < viewport.width && panel.height < viewport.height, "login should use a compact panel");
    const stage=screen.locator(".mac-login-stage");
    const area=await stage.boundingBox();
    const focus=screen.getByRole("button",{name:"登录区域", exact:true});
    assert.equal(await focus.getAttribute("aria-pressed"),"true");
    const initial=await img.boundingBox();
    // Avatar, password and the lower login message must all remain in the crop.
    for (const [x,y] of [[.5,.82],[.5,.9],[.5,.96]]) {
      assert.ok(initial.x+initial.width*x >= area.x && initial.x+initial.width*x < area.x+area.width);
      assert.ok(initial.y+initial.height*y >= area.y && initial.y+initial.height*y < area.y+area.height);
    }
    const passwordX=Math.round(initial.x+initial.width*.5), passwordY=Math.round(initial.y+initial.height*.9);
    await page.mouse.click(passwordX,passwordY);
    await waitInput(1);
    assert.ok(Math.abs(inputs[0].x-512)<=1 && Math.abs(inputs[0].y-576)<=1);
    inputs.length=0;
    if (process.env.CAGE_LOGIN_SCREENSHOTS) await page.screenshot({path:`${process.env.CAGE_LOGIN_SCREENSHOTS}/login-${viewport.width}.png`});
    assert.equal(await page.evaluate(() => window.cageEditAction("paste")),false);
    await screen.getByRole("button",{name:"完整画面"}).click();
    const fit=await img.boundingBox();
    assert.ok(Math.abs(fit.width/fit.height - 1.6)<.005);
    if (fit.x > area.x+1) await page.mouse.click((area.x+fit.x)/2,area.y+area.height/2);
    else await page.mouse.click(area.x+area.width/2,(area.y+area.height+fit.y+fit.height)/2);
    assert.equal(inputs.length,0,"letterbox click leaked");
    const fitX=Math.round(fit.x+fit.width/2), fitY=Math.round(fit.y+fit.height/2);
    await page.mouse.click(fitX,fitY);
    await waitInput(1);
    assert.equal(inputs[0].x,Math.floor((fitX-fit.x)*1024/fit.width));
    assert.equal(inputs[0].y,Math.floor((fitY-fit.y)*640/fit.height));
    await focus.click();
    const zoom=await img.boundingBox();
    assert.ok(zoom.width > 3*fit.width);
    await page.mouse.move(area.x+area.width/2,area.y+area.height/2);
    await page.mouse.down(); await page.mouse.move(area.x+area.width/2+90,area.y+area.height/2+55,{steps:5}); await page.mouse.up();
    const pan=await img.boundingBox();
    assert.ok(pan.x>zoom.x+60 && pan.y>zoom.y+30);
    const x=Math.round(area.x+area.width*.65), y=Math.round(area.y+area.height*.6);
    await page.mouse.click(x,y);
    await waitInput(2);
    assert.equal(inputs[1].x,Math.floor((x-pan.x)*1024/pan.width));
    assert.equal(inputs[1].y,Math.floor((y-pan.y)*640/pan.height));
    await input.press("Enter"); await waitInput(3); assert.equal(inputs[2].code,36);
    assert.equal(await screen.isVisible(), true, "Return must wait for the guest to confirm login");
    await input.evaluate((el) => {
      const data=new DataTransfer(); data.setData("text/plain","Z7");
      el.dispatchEvent(new ClipboardEvent("paste",{bubbles:true,cancelable:true,clipboardData:data}));
    });
    await waitInput(5);
    assert.deepEqual(inputs.slice(-2).map(({code,shift})=>[code,shift]),[[6,true],[26,false]]);
    assert.equal(await input.inputValue(),"");
    await screen.getByRole("button",{name:"完整画面"}).click();
    assert.deepEqual(await img.boundingBox(),fit);
    await page.evaluate(() => window.cageNativeAction("closeTab"));
    await screen.waitFor({state:"detached"});
    await page.waitForFunction(() => document.activeElement?.textContent === "登录客机账户");
    assert.ok(!await page.locator("#root").evaluate((el)=>el.inert));
    frameMime="image/png";
    await trigger.click();
    await page.waitForFunction(() => document.querySelector(".mac-login-canvas img")?.src.startsWith("data:image/png;"));
    assert.equal(await focus.getAttribute("aria-pressed"),"true", "reopening should restore the login crop");
    finish=true;
    await screen.waitFor({state:"detached"});
    assert.deepEqual(closed, ["trial-1","trial-2"]);
    assert.deepEqual(nativeCalls.filter((m)=>/fullscreen/i.test(m.action)),[]);
    assert.equal(await page.evaluate(()=>document.fullscreenElement),null);
    assert.deepEqual(errors,[]);
    console.log(`${viewport.width}×${viewport.height}: default login crop, compact panel, coordinate mapping, fit, pan, paste, native close and completion passed.`);
  } catch (error) { console.error("page:",(await page.locator("body").innerText()).slice(0,700), "errors:", errors); throw error; } finally { await browser.close(); }
}

// Exercise the first-setup password button through the normal page entry.
// Frame polling must preserve an operation error until a user action succeeds.
{
  let operationError = "请先点选系统授权窗口的密码框，再点击填写授权密码";
  let frameError = "", frames = 0, latestFrame = "";
  const fixture = await createWorkbench({
    async beforeNavigate(page, state) {
      Object.assign(state.account, {initial_mac_setup:true, guest_system:"macos", macos_guest_user:"cage"});
      Object.assign(state.status, {ready:true, vm_running:true});
      const images = await page.evaluate(() => {
        const canvas = document.createElement("canvas");
        canvas.width = 160; canvas.height = 100;
        const paint = canvas.getContext("2d");
        return ["#526482", "#627492"].map(color => {
          paint.fillStyle = color; paint.fillRect(0, 0, canvas.width, canvas.height);
          return canvas.toDataURL("image/png").split(",")[1];
        });
      });
      await page.route("**/api/macos/first-setup", async route => {
        const request = route.request().postDataJSON();
        if (request.action === "permissions") return route.fulfill({json:{screen:false, input:false, projects:false, managed_credential:true}});
        if (request.action === "open") return route.fulfill({json:{session:"error-persistence-fixture"}});
        if (request.action === "frame") {
          frames++;
          if (frameError) return route.fulfill({json:{error:frameError}});
          latestFrame = images[frames % images.length];
          return route.fulfill({json:{image:latestFrame}});
        }
        if (request.action === "fill-password") {
          assert.equal(request.password, undefined, "browser supplied a credential");
          return route.fulfill({json:operationError ? {error:operationError} : {}});
        }
        return route.fulfill({json:{}});
      });
    },
  });
  const {page, browser, errors} = fixture;
  try {
    const screen = page.getByRole("dialog", {name:"首次设置虚拟 Mac"});
    const fill = screen.getByRole("button", {name:"填写授权密码", exact:true});
    await screen.getByRole("img").waitFor();
    await fill.click();
    await screen.getByRole("alert").filter({hasText:operationError}).waitFor();
    const observed = frames;
    for (let attempts = 0; frames < observed + 2 && attempts < 150; attempts++)
      await new Promise(resolve => setTimeout(resolve, 20));
    assert.ok(frames >= observed + 2, "frame polling stopped after an input error");
    await page.waitForFunction(image => document.querySelector(".mac-login-canvas img")?.src.endsWith(image), latestFrame);
    assert.equal(await screen.getByRole("alert").isVisible(), true, "a successful frame erased the password error");
    assert.equal(await screen.getByRole("alert").innerText(), operationError);

    frameError = "授权画面暂时无法读取";
    await page.waitForResponse(async response => response.url().endsWith("/api/macos/first-setup") &&
      response.request().postDataJSON()?.action === "frame" && (await response.json()).error === frameError);
    operationError = "";
    await fill.click();
    await screen.getByRole("alert").filter({hasText:frameError}).waitFor();
    frameError = "";
    await screen.getByRole("alert").waitFor({state:"hidden"});

    operationError = "系统授权窗口或密码框已变化，填写已停止，请重新选择";
    await fill.click();
    await screen.getByRole("alert").filter({hasText:operationError}).waitFor();
    await screen.getByRole("button", {name:"返回设置指引"}).click();
    await screen.waitFor({state:"detached"});
    await page.getByRole("button", {name:"打开客机系统设置", exact:true}).click();
    await screen.getByRole("img").waitFor();
    assert.equal(await screen.getByRole("alert").count(), 0, "reopening retained an old operation error");
    assert.deepEqual(errors, []);
    console.log("First-setup password error persistence, independent frame recovery and close/reopen cleanup passed.");
  } finally { await browser.close(); }
}
