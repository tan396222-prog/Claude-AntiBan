# 发布 macOS 安装包

本页供维护者构建和发布 Cage。用户安装说明见[开始使用](getting-started.md)。发行目标为 Apple Silicon、macOS 14 或更新版本；安装包包含应用、控制程序、代理核心和许可证，首次启动再下载 Linux 环境与开发工具。

## 构建前准备

需要 macOS、Xcode Command Line Tools、Go（版本见 `go.mod`）、Node.js 24、`uv`。`VERSION` 是 App、控制程序和安装包的版本来源。`CFBundleShortVersionString` 使用数字版本，关于窗口显示包含 Alpha 标记的完整版本。

每次发行都须对本次构建完成签名、公证和安装包验收。版本与通过范围以对应 Release 及其构建记录为准，其他版本的结果不能覆盖本次构建。

对外分发还需要 Apple Developer ID Application 证书与配套私钥，以及已保存到钥匙串的 `notarytool` 凭据。证书、私钥和公证凭据只保存在钥匙串或 CI Secret 中，不写入源码或发布附件。按 [Apple 公证说明](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow) 配置认证后，可先用 `xcrun notarytool history --keychain-profile <名称>` 验证。

## 构建并验证

先完成产品改动和相关测试，再运行发行脚本：

```sh
export CAGE_SIGNING_IDENTITY='Developer ID Application: 你的名称 (TEAMID)'
export CAGE_NOTARY_PROFILE='你的公证凭据名称'
unset CAGE_BUILD_OUTPUT
bash scripts/package-macos.sh
```

脚本从源码构建，逐一签名嵌套程序，给控制程序保留虚拟化权限，并启用 Hardened Runtime 与可信时间戳。App 和 DMG 分别提交 Apple 公证、附加票据并验证。公证未通过、签名错误或 Gatekeeper 拒绝时，脚本失败，不生成“可发布”的成功提示。

产物位于 `_output/releases/<版本>/`：

- `Cage-<版本>-macos-arm64.dmg`：供用户拖入应用程序文件夹安装。
- `mihomo-<核心版本>-source.tar.gz`：随包独立代理程序的对应上游源码，须与安装包一起上传。
- `SHA256SUMS`：上述两个附件的 SHA-256 校验值。
- `app-notarization.json`、`dmg-notarization.json`、`Cage-notarization.zip`：本地公证过程记录，不作为用户下载附件。

应用内 `Contents/Resources/licenses/` 包含主项目、Go 依赖、前端运行依赖、Mihomo 与 noVNC 的许可材料，以及实际随包使用的 noVNC 修改后源码。维护者应在依赖变化后运行 `scripts/licenses` 更新 Go 依赖许可集合。发行脚本会拒绝缺少许可证的前端运行依赖。

只做本机开发时可运行 `bash scripts/build-app.sh`，未设置签名身份会使用 ad-hoc 签名。`--install` 可安装到 `~/Applications/Cage.app`，会替换该位置的旧版本。此构建方式不完成公证，不能作为已经通过 Gatekeeper 的下载包宣传。

构建先在临时目录验证新 App，再替换目标；目标中的 App 或控制程序仍在运行时，脚本拒绝覆盖。默认目标为 `_output/Cage.app`，并清理构建目录中的其他 Cage App。独立验证可将 `CAGE_BUILD_OUTPUT` 设为绝对 `.app` 路径且不使用 `--install`，此时不清理其他副本。正式打包使用默认目标，应先取消这个自定义变量。

使用 `--install` 时，安装副本先验证再替换到 `~/Applications/Cage.app`，随后清理构建目录及两个 Applications 目录中的其他 Cage App。清理按应用标识核对，跳过符号链接、其他软件，以及 App 或控制程序仍在运行的副本，不删除配置、虚拟磁盘、项目或历史 DMG。目标本身在构建前和替换前均检查是否运行。安装前须保存文件、停止环境并退出。发行打包成功时也会清理构建目录中的 App 副本。

## Apple 返回等待或失败时

公证结果文件保存提交 ID。超时后先查询原提交，不重复上传同一包：

```sh
xcrun notarytool info <提交ID> --keychain-profile "$CAGE_NOTARY_PROFILE"
xcrun notarytool log <提交ID> --keychain-profile "$CAGE_NOTARY_PROFILE" _output/notarization-log.json
```

App 提交已 Accepted 时，运行 `xcrun stapler staple _output/Cage.app`，再执行 `bash scripts/package-macos.sh --from-notarized-app` 接续 DMG 制作。此选项要求 App 版本与 `VERSION` 一致且已有有效票据；源码有新改动时必须重新完整构建。

DMG 提交已 Accepted 时，直接对原 DMG 执行 `xcrun stapler staple`、`xcrun stapler validate` 和 `spctl --assess --type open --context context:primary-signature --verbose=2`。附加票据会改变文件内容，必须重新生成校验值。公证被拒时根据 Apple 日志修复并重建；不能用移除隔离属性或关闭 Gatekeeper 代替验收。

## 发布与安装验收

发行物通过公开的 GitHub Releases 分发。推送前检查提交内容不包含个人配置、节点凭据、登录链接和未脱敏截图；先推送源码提交及对应版本标签，再创建草稿、上传 DMG、Mihomo 源码与校验文件，附件完整后发布。公开源验证与主页自动部署见[公开发行与主页部署](public-releases.md)。

从实际 DMG 复制 App 后验证签名、票据与首次入口。用独立临时配置验证空状态，避免覆盖维护者正在使用的环境。每次发布说明分别列出已验证项和仍需真实用户操作的项目：干净 Mac 安装、首次镜像下载、代理配置、Claude 授权及模型请求、重启恢复、升级和卸载。单独通过公证不能替代这些产品验证。

应用内更新读取公开 Release 中与版本对应的 `Cage-<版本>-macos-arm64.dmg` 和 `SHA256SUMS`，通过所选受管代理获取，无直连回退。发布包及其中的控制程序、代理核心和更新安装器须由与当前 App 相同的 Developer ID Team 签名，并通过公证与 Gatekeeper 验证；ad-hoc 开发构建会被自动安装流程拒绝。公开下载源无需 GitHub 凭据；发布者仍须按每个版本分别验证下载、安装与重启，不能将附件公开可读等同于升级验收通过。

客户端自动检查新版，仅在有可安装更新时显示圆形按钮。用户保存工作后点击一次，下载校验完成即停止环境和控制服务、替换 App 并重启，不再二次确认。版本接管与启动失败恢复见[工作台设计](design/workbench.md#更新与后台接管)。配置、代理和虚拟磁盘位于用户数据目录，替换或删除 App 不会自动删除这些数据；Linux 组件与工具另行升级。

## 客机工具版本与来源

`internal/cage/tool-versions.env` 保存 Cage 首装和受管升级使用的最低版本及平台摘要，`guest-tools.sh` 是共用安装器。code-server 固定为 [4.145.0 官方发行版](https://github.com/coder/code-server/releases/tag/v4.145.0)，mise 固定为 [2026.10.25 官方发行版](https://github.com/jdx/mise/releases/tag/v2026.10.25)；arm64 与 amd64 资产的 SHA-256 来自各项目 GitHub Release API 的资产 `digest` 字段。code-server 首选中科大固定版本的 .deb，缺包时自动回到 GitHub。mise 首选 npmmirror 上的上游平台包，各架构的 npm 归档和 GitHub 发布归档分别固定 SHA-256。调整 mise 版本时，从 npm 官方注册表核对完整性，并比对两个归档内可执行文件的字节。校验失败的文件不能安装。Claude Code 使用 npm 的固定版本 `2.1.314`，npm 校验注册表的包完整性。Chromium 采用 Debian 签名软件源选定的版本。对 Debian 官方安全库中的 chromium 和 chromium-common，先从南大、中科大取同一 .deb，使用官方索引中的 SHA-256 验证，再交给原 APT 操作安装。自定义安全仓库或缺少 python3-apt 的精简环境继续使用原 APT 配置。

升级这些最低版本时，需要同时核对官方资产、更新摘要并验证现有持久环境，不能只改显示版本。受管升级保留较新的已安装版本，不清理用户配置，不重建 Linux 磁盘。组件脚本与工具包分别维护：组件摘要正确只证明 Cage 脚本匹配，不表示所有工具已升级或已完成漏洞审计。
