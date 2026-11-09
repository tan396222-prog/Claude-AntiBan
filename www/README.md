# Cage 项目主页

主页地址为 <https://github.com/tan396222-prog/Claude-AntiBan>，面向首次使用者介绍产品、公开安装包、开始使用步骤与使用边界。

在本目录执行：

```sh
npm ci
npm run dev      # http://localhost:4822/Claude-Cage/
npm run check
npm run build    # dist/
```

默认中文入口与其他语言路由位于 `src/pages/`，共用 `src/components/CageHome.astro`。`src/i18n/cage.ts` 从根目录对应 README 提取正文，`src/i18n/languages.ts` 定义下拉框中的 15 种语言及界面短文案。样式位于 `src/styles/cage.css`，工作台截图位于 `public/`，README 共用素材位于根目录 `assets/`。`astro.config.mjs` 的 `site` 和 `base` 分别指定站点域名与项目子路径；内部链接、图片和重定向使用 `import.meta.env.BASE_URL`。自定义部署可通过 `SITE_URL` 和 `SITE_BASE` 覆盖。

“下载应用”在所有语言中打开同一个三平台选择弹窗，列出 macOS、Windows、Linux 的系统要求、可用安装包、版本说明与安装指南。没有公开包的平台提供安装与构建说明。弹窗支持键盘操作、Esc、关闭按钮和点击背景关闭；禁用 JavaScript 时下载区仍提供平台链接。

下载信息来自 `src/data/release.json`，表示可公开下载的版本，可以低于根目录 `VERSION` 所标记的开发版本。三平台附件统一放在 `v<版本>` Release。`downloadURL` 为 macOS 包，`windowsDownloadURL` 和 `linuxDownloadURL` 为同版本可选安装包；预发行条目的 Windows 未签名包通过 `windowsPreview` 保存下载信息，始终标注未签名，不进入签名安装包的自动更新入口。同步时核对安装包及配套源码摘要。从仓库根目录运行 `node www/scripts/sync-release.mjs`，即可从匿名 GitHub API 刷新公开版本；带 `--archive-directory` 时下载并核验三平台包。普通本地构建使用已保存的数据，不要求联网。

GitHub Actions 在网站文件或 README 的普通推送与 PR 中检查和构建；公开 Release 发布或维护者手动运行时部署 GitHub Pages。Release 事件会先下载实际 macOS 安装包与对应 Mihomo 源码，验证摘要、Developer ID 签名、Apple 公证和 Gatekeeper，失败时不会部署这一轮网站。Windows、Linux 的发行验收由对应平台流程负责。发布操作见[公开发行与主页部署](../docs/public-releases.md)。

根目录 README 是维护者确认产品措辞的来源。README 修改触发主页重新构建；发布工作流只刷新下载版本并读取现有译文，不自动生成翻译。中文主页为 `/`，其他语言使用 `/<语言代码>/`，语言范围与根目录 README 导航一致，不提供英语。语言选择器在控件下方悬浮展开链接列表，不改变正文位置。支持键盘展开、链接导航与 Esc 收起，禁用 JavaScript 时仍可展开和跳转。各语言使用独立 URL；阿拉伯语、波斯语、普什图语、达里语使用从右向左布局。网页正文直接复用对应 README 译文，结构调整时构建会提示维护者同步映射。

GitHub Pages 是当前主页的发布目标，部署配置由仓库工作流维护。

## 使用文档

中文使用手册位于 `/docs/`，通过 `src/pages/docs/[...slug].astro` 为每篇文章生成静态页面。正文源为根目录 `docs/guide/*.md`，元数据包含标题、摘要、分组、顺序与搜索关键词。新增或修改正文只需编辑这些 Markdown，分类目录、前后章和搜索索引随构建生成，不需额外同步一份网页文案。文章中的同目录 `.md` 链接在网站中转为带 `BASE_URL` 的章节链接，仓库内仍能直接阅读。

手册提供侧栏、手机内嵌目录、页内目录、全文搜索及无结果恢复；关闭 JavaScript 后仍可阅读和跳转。搜索只在当前浏览器中执行，不向服务器发送输入。主页所有语言入口可进入中文手册，手册未提供其他语言译文。

实际应用截图保存在 `public/docs/screenshots/`，原始像素、版本、平台、日期与来源说明集中在同目录的 `manifest.json`。正文通过带图注的 `figure` 引用，并链接原图；`src/lib/guide.ts` 将仓库相对图片路径转为站点基路径。新增截图必须核对实际构建版本及敏感内容，开发构建、模拟器预览和历史版本分别标注，不用当前版本补标旧图。原图保持截图工具输出，不能把模拟账号或额度当作真实运行结果。

完成构建后运行 `node scripts/check-site.mjs` 核对主页、文章、章节锚点和下载入口。交互验证使用 `node scripts/guide-smoke.mjs`，可用 `CAGE_PLAYWRIGHT_MODULE` 指定现有 Playwright；`CAGE_SITE_URL` 指向本地或线上站点，`CAGE_SITE_REPORT` 指定截图目录。

维护者明确授权 Windows 未签名公开预览后，可手动运行 `platform-preview.yml`，提供已经成功的三平台 CI run ID 并启用 `unsigned_windows`。流程从该次 CI 获取安装包，核对源码版本、原始摘要、配套源码和上传后的 GitHub digest，补充到已存在的 `v<版本>` 预发行条目，合并校验清单并记录各平台源码提交，随后调用官网验收与部署。流程不创建新 Release 或标签，日常推送不触发发布，现有 macOS 安装包不被覆盖。

`public/install.sh` 仍被上游 `release.sh` 与安装器测试使用，保留用于 agent-vm 兼容流程，不作为 Cage 安装入口。
