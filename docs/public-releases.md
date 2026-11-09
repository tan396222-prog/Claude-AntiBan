# 公开发行与主页部署

本页供维护者发布 Cage 安装包并维护项目主页。公开下载源为 [GitHub Releases](https://github.com/tan396222-prog/Claude-AntiBan/releases)，项目主页为 <https://github.com/tan396222-prog/Claude-AntiBan>。macOS 构建、签名与公证步骤见[发布 macOS 安装包](releasing.md)，Windows 安装器、签名及验收要求见 [Windows 指南](windows.md#维护者构建与验收)，Linux 系统包构建与验收见 [Linux 指南](linux.md#维护者构建与验收)。

## 自动发布入口

日常推送只检查并保存测试包，不升级版本号或公开发布。收到用户明确发版指令后，使用 [三平台 CI/CD](ci-cd.md) 的显式发布入口；它统一准备版本、验证三平台、上传完整草稿并公开发布。以下手工步骤用于维护和故障恢复，同样需要用户的发版授权。

## 发布安装包

`VERSION` 表示正在构建的应用版本；主页按平台展示可下载的公开附件。发布新版本时，先完成相关测试、提交源码并推送对应的 `v<版本>` 标签，再按平台发行文档生成已签名且通过公证的 macOS DMG、完成签名和实机验收的 Windows 安装器，以及通过发行验收的 Linux DEB。

三平台的安装包命名为：

- `Cage-<版本>-macos-arm64.dmg`。
- `Cage-<版本>-windows-amd64-setup.exe`。
- `Cage-<版本>-linux-amd64.deb`。

各平台随附与代理核心版本一致的 `mihomo-v<版本>-source.tar.gz`。Windows 还须附带该构建的 QEMU 主程序源码和包含随附库/固件的完整对应源码归档。三平台共用一份 `SHA256SUMS`，摘要在最终签名和公证票据附加后生成；不要用一个平台的清单覆盖另一个平台的清单。未签名开发包不得作为正式平台附件。

Linux 用户通过系统包管理器手动安装，应用不自动替换系统文件。发布说明必须区分云端容器测试与真实 Linux/KVM 桌面验收；未完成的项目不得列为通过。合并工具接受各平台目录，并去除相同内容的重复源码附件。

使用本地合并工具核对平台目录中的摘要并生成统一发布目录：

```sh
node scripts/prepare-release-assets.mjs \
  --output _output/publish-vVERSION \
  _output/releases/VERSION _output/releases/VERSION/windows \
  _output/releases/VERSION/linux
```

把 `VERSION` 替换为实际版本。输出目录必须不存在；工具拒绝摘要错误、不同内容的同名文件和开发安装器，相同源码附件只保留一份。该工具只准备文件，签名、公证与实机检查仍由对应平台流程完成。

先创建草稿并上传全部附件，再发布草稿。Alpha 版本标为预发行；所有 `gh` 命令显式指定 `--repo tan396222-prog/Claude-AntiBan`，避免 fork 的默认仓库指向上游。已发布的安装包不原地覆盖，修正二进制时使用新版本号。

人工公开发布 Release 会运行 `Website and public releases`：匿名下载附件，核对 SHA-256、应用版本、Developer ID 签名、公证票据与 Gatekeeper；通过后重建并部署主页。自动发布入口直接调用同一网站流程。草稿不会进入公开下载列表；编辑历史 Release 不自动重新部署网站。验收失败时，该轮主页部署停止，维护者应检查 Actions 日志并修正发行物。

GitHub 匿名 API 的额度耗尽时，指定版本的验证流程从该版本公开的 SHA256SUMS 和附件响应重建下载清单。此路径不使用维护者令牌；清单只能包含该版本安装包和允许的源码附件，所有文件仍须匿名下载并通过摘要、签名与公证检查。没有指定版本的自动列表检查继续报告 API 限流。

版本标签对应的源码须包含发布工作流。需要复验时，使用下文带 `release_tag` 的手动命令。

GitHub Release 一旦公开，应用内更新即可读到它，早于这项事后检查完成。因此，本地打包验收必须在发布草稿前通过，不能依赖 Actions 作为安装包公开前的审批门槛。CI 中若用默认 `GITHUB_TOKEN` 发布 Release，不会触发后续工作流，须另外显式调用网站工作流；手工网页发布或 `gh` 用户凭据发布会正常触发。

## 应用内更新

支持应用内更新的客户端直接读取公开 Release，经所选受管代理下载，没有主机直连回退。正式客户端忽略预发行；预发行客户端可接收更新的预发行或正式版本。客户端仍独立核对摘要、应用身份、平台签名：macOS 检查同一 Developer ID Team 和 Apple 验证结果，Windows 检查 Authenticode 信任链及相同发布者公钥。

首个内测版本为 `0.1.0-alpha.1`，macOS 首次安装使用 DMG，Windows 使用对应 EXE；后续版本可通过应用内入口升级。公开源校验与单元测试不能证明真实用户环境下的替换、重启和回滚已经验收；每次发布均须在 Release 中说明实际验证范围。

可在不安装、不启动 App 的情况下验证公开附件：

```sh
node www/scripts/sync-release.mjs --tag v0.1.0-alpha.1 \
  --output /tmp/cage-release-check/release.json \
  --archive-directory /tmp/cage-release-check/assets
bash scripts/verify-macos-release.sh /tmp/cage-release-check/assets
```

## 主页与措辞同步

GitHub Pages 的发布源设为 GitHub Actions。普通推送只做网站检查和构建；显式发版后由发布流程验证对应版本并部署。维护者明确需要重新部署时使用：

```sh
gh workflow run www.yml --repo tan396222-prog/Claude-AntiBan --ref main
```

需要同时重验某个公开安装包时，在命令后增加 `-f release_tag=v0.1.0-alpha.1`，替换为实际版本。

主页的“下载应用”打开 macOS、Windows、Linux 平台选择。默认版本来自具有完整 macOS 资产的公开 Release；同一版本的 Windows 正式安装器或 Linux DEB 具有有效摘要时，同时提供对应下载。缺少公开安装包的平台仍显示系统要求，并链接安装与构建说明。

三平台安装包统一归入 `v<版本>` Release，不单独建立平台预览标签。同一预发行条目可以附带 Windows 未签名安装器及其对应的 Mihomo、QEMU 主程序源码，全部列入共用的 `SHA256SUMS`。Windows 入口显示“预览版 · 未签名”，需手动安装和更新。该入口不改变应用内更新规则；签名限制、系统兼容性与验收范围须在发行说明中写明。

用户明确授权公开未签名预览后，维护者可手动触发 `platform-preview.yml`，提供成功的三平台 CI run ID 并启用 `unsigned_windows`。工作流要求对应 `v<版本>` 预发行条目已经存在，从该次 CI 取得原始产物、核对源码版本和摘要后补充缺少的平台附件，上传后核对 GitHub digest，最后更新共用校验清单。已有的同名安装包必须内容一致，不被覆盖；发行说明记录 Windows/Linux 的实际源码提交。随后调用官网工作流，匿名下载三平台安装包、核对摘要，并验证 macOS 签名与公证，成功后部署官网。普通推送不会触发发布。

主页构建从公开 Release 刷新下载链接，避免源码版本领先安装包时出现失效下载。图片、样式、404 返回链接与兼容重定向均使用 `/Claude-Cage/` 子路径。

产品正文由维护者确认的 README 驱动人工同步。README 修改完成后，再更新网页措辞及多语言版本，并分别核对功能范围、安装条件与验收限制。网站部署本身不会自动翻译 README。
