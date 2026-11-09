# Claude 防封 宣传片

一支约 61 秒的宣传片，横屏 1920×1080 和竖屏 1080×1920 两个版本，60 fps，画面和配乐都由代码生成。

| 时间 | 段落 | 内容 |
|---|---|---|
| 0–4s | 冷开场 | 还原 “Your account is on hold” 停用页，滚到 “Reviews take about 10 days.”，随后缩成证据卡，砸出损失：订阅打水漂、余额不能用、项目停摆。 |
| 4–15s | 痛点 | VPS、指纹浏览器、远程 Mac mini 的月账单；全局代理把整台电脑拖下水；传统虚拟机又重又慢还得自己配；“就不能，简单一点吗？” |
| 15–20s | 方案 | 像素 Logo 落笼，定位语“为 Claude 量身定制的轻量虚拟机”，开箱即用，只需配置自己的代理。 |
| 20–43s | 真实工作台 | 用当前版本工作台的真实截图做底：「网络与隔离检查」11 项逐个打勾、外网放行 → 状态栏的出口与时区 → 内置 Claude Desktop 对话 → 项目上新建「Claude 终端」进入 Claude Code → 授权页在 Cage 浏览器打开并完成登录 → 回到终端干活。 |
| 43–49s | 多账号调度 | “Claude 账号”面板：多个订阅的 5h / 7d 额度，新建终端按负载和余量自动分配，额度用尽的账号本轮跳过。 |
| 49–53s | 卖点汇总 | 轻量开箱即用 · 自带泄漏审查 · 与主机隔离 · 开源接受审查。 |
| 53–57s | 写给相遇 | 角色插画与“把 Claude 关起来，是为了更好地保护我们的相遇。” |
| 57–61s | 片尾 | Logo、平台、主页与 GitHub 地址、非隶属声明。 |

痛点里的账单金额、停用页日期、节点名、账号邮箱与额度、终端对话都是示意内容，改文案直接编辑 `index.html`。

## 文件

- `index.html`：舞台、各幕布局和样式。
- `promo.js`：每一帧都是时间的纯函数 `render(t)`，预览、拖动和导出看到的画面完全一致。
- `timeline.js`：画面与配乐共用的时间点，改节奏只改这里。
- `plates/`：第三幕的底图，是真实工作台界面的 2x 截图。横屏用 1440×860 的窗口，竖屏用 760×1100 的窄窗口（`plates/portrait/`，工作台自带的窄屏布局），竖屏因此能整宽展示、字号够大。`layout.js` 记录侧栏按钮、状态栏、检查弹窗每一行等位置；Claude Desktop、终端文字、浏览器页面和逐行打勾由 `promo.js` 叠加绘制。
- `capture-plates.mjs`：用模拟接口加载 `internal/workbench/web` 里构建好的工作台，按横竖两种窗口尺寸重新截取 `plates/` 并生成 `layout.js`。
- `soundtrack.mjs`：按时间轴合成配乐与音效，输出 `out/soundtrack.wav`。
- `render.mjs`：本地静态服务 + 无头 Chrome 逐帧截图 + ffmpeg 编码；`--portrait` 输出竖屏。
- `chrome.mjs`：在 macOS、Windows、Linux 上查找 Chrome / Chromium / Edge。

## 使用

需要 Node.js 18+ 和本机的 Chrome、Chromium 或 Edge（macOS、Windows、Linux 的常见安装位置会自动查找，也可以用 `CHROME_PATH` 指定）。ffmpeg 由 `ffmpeg-static` 提供；如果 npm 提示安装脚本未授权，执行 `npm install-scripts approve ffmpeg-static` 后重新安装。

```bash
cd promo
npm install
npm run preview          # 打开打印出的地址实时预览：空格播放/暂停，←/→ 跳转，?t=12.5 定位
npm run render           # 合成配乐并导出横屏 out/claude-cage-promo.mp4
npm run render:vertical  # 竖屏 9:16，导出 out/claude-cage-promo-vertical.mp4（预览加 ?portrait）
npm run render:draft     # 960×540 / 30 fps 快速草稿
npm run plates           # 工作台界面更新后，先构建 UI，再重截第三幕底图
node render.mjs --frames 8.6,24,33   # 只导出这些时间点的 PNG
```

页面使用 Google Fonts（Inter、Noto Sans SC、Noto Serif SC、JetBrains Mono），渲染时需要能访问字体服务；离线时会退回系统字体，排版会有轻微差异。
