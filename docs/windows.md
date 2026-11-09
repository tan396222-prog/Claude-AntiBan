# Windows 安装、更新与发布

Cage 的 Windows 版本面向 Windows 10 22H2（内部版本 19045）和 Windows 11 的 x64 电脑。安装后从开始菜单打开 Cage，导入自己的代理，再启动 Linux 环境。工作台、项目、终端和浏览器沿用 macOS 版的操作方式。

Windows 实现和安装器处于开发验收阶段。源码构建成功不代表所有电脑均已通过验收；下载时应查看对应发行说明。未签名开发包用于测试，不能通过应用内更新自动安装。正式发行还需要可信代码签名、完整的随附软件源码材料和 Win10/11 实机验收。

当前开发包已在 Windows 11 Pro 24H2（26100）的 Intel x64 实体机验证安装、覆盖安装、原生工作台、QEMU/WHPX 启动、十一项关键安全检查、中文空格路径共享、终端输入与缩放，以及浏览器和编辑器操作。暂停外网后客机 HTTPS 请求被阻断，重新检测后恢复；配置和项目文件在覆盖安装后保留。卸载保留数据及拒绝未签名更新也已实测。Windows 10 的实机兼容性和可信签名版本之间的完整升级仍待验证。

Windows 11 x64（内部版本 26200）另已在普通账户的原生工作台验证首次选 D 盘、显示真实下载字节、断点续传、已有环境从 C 盘迁到 D 盘并启动，以及迁移中断恢复。迁移后核对了虚拟机内文件、内置编辑器登录会话和本机测试账户。

## 安装前准备

| 项目 | 要求 |
|---|---|
| 系统 | x64 Windows 10 22H2 或 Windows 11；不包含 Windows ARM64 与 32 位系统 |
| 虚拟化 | BIOS/UEFI 已启用处理器虚拟化，Windows 已启用“Windows 虚拟机监控程序平台” |
| 存储 | 使用本地磁盘，保留安装包、Linux 镜像、虚拟磁盘和更新备份所需空间；初始虚拟磁盘上限为 24 GiB |
| 代理 | 自己的 Clash/Mihomo YAML 来源或 HTTP/SOCKS5 节点；Cage 不提供代理服务 |

在“启用或关闭 Windows 功能”中启用“Windows 虚拟机监控程序平台”，按系统要求重启。也可在管理员终端执行：

```powershell
DISM /Online /Enable-Feature /FeatureName:HypervisorPlatform /All
```

这个系统功能需要一次管理员授权；Cage 日常运行和应用安装使用普通用户权限。企业电脑若禁止虚拟化，应由设备管理员启用。Cage 使用独立的 QEMU 虚拟机，不依赖 WSL 或 Docker。

## 安装并开始工作

1. 在[项目主页](https://github.com/tan396222-prog/Claude-AntiBan/releases)点击“下载应用”并选择 Windows，核对对应发行条目的 `SHA256SUMS`。签名安装包名为 `Cage-<版本>-windows-amd64-setup.exe`；当前公开预览包名为 `Cage-<版本>-windows-amd64-setup-unsigned.exe`，主页标明“预览版 · 未签名”。未签名包可能触发未知发布者提示，需手动安装和更新。
2. 运行安装程序，选择安装位置。安装器随附 .NET 桌面运行组件、WebView2 安装程序、QEMU、OpenSSH 客户端和代理核心，无需另装 Go、Node.js 或 Clash。
3. 从开始菜单启动 Cage。如果 C 盘空间有限，在工作台点击“选择环境存储位置”，选择其他磁盘的专用空文件夹，再导入代理并启动环境。首次下载的 Linux 镜像、后续安装的工具和虚拟磁盘可以一起放在所选盘。已有环境也可以从设置迁移。见[选盘与迁移步骤](getting-started.md#选择环境存储位置)和[首次下载清单](getting-started.md#首次启动下载什么)。

安装器不设置系统代理，不安装 SSH 服务，不把随附工具加入系统 PATH。WebView2 是 Microsoft 提供的系统浏览器运行时，其安装与维护由 Microsoft 安装程序负责。

“打开项目”接受 Windows 文件夹。例如 `C:\Users\姓名\Projects\demo` 在 Linux 内对应 `/c/Users/姓名/Projects/demo`。项目文件仍保存在原来的 Windows 文件夹中，编辑和删除会同步；`.git` 等元数据保持只读，提交在主机 Git 中完成。只共享默认工作区与明确打开的目录。

Windows 快捷键以 `Ctrl` 为主：`Ctrl+O` 打开项目、`Ctrl+T` 新建网页、`Ctrl+L` 定位地址栏、`Ctrl+W` 关闭标签，`Ctrl+Shift+Enter` 新建终端。终端使用 `Ctrl+Shift+C/V` 复制粘贴，`Ctrl+C` 中断命令。网页和编辑器采用 Linux 的 Ctrl 组合键。

关闭窗口会保留环境与后台任务，可从托盘图标重新打开；停止环境或选择停止后退出会结束终端。重新打开应用可连接仍运行的会话。

浏览器上传通过本机文件选择窗口选取文件；下载完成可用 Windows“另存为”选择位置。侧栏“下载”提供打开文件、在资源管理器中显示与再次另存为，取消保存窗口时保留默认 Downloads 中的文件。操作与限制见[浏览器上传与下载](getting-started.md#浏览器上传与下载)。

## 更新与卸载

应用内更新从固定的公开 GitHub Releases 来源选择 Windows x64 安装器，并沿用预发行与正式版通道规则。检查与下载经过已经应用并运行的 Cage 代理，不回退主机直连。下载安装器后校验 SHA-256、版本、产品名称和 Authenticode 签名；当前应用与新安装器必须具有相同的发布者公钥，未签名开发包不能自动安装更新。

没有新版时隐藏更新按钮；有新版时显示金色圆形按钮。用户先保存文件并结束运行中的任务，再点击一次，按钮显示环形下载进度和百分比。校验通过后自动安装，不再二次确认。更新器先备份应用，再通过工作台统一入口停止环境、代理和控制服务，等待当前窗口退出后执行静默安装。新应用完成工作台启动确认后才认定成功；安装或启动失败时尝试恢复旧程序，保留失败原因和必要备份。磁盘空间不足、文件被其他软件占用等情况可能阻止自动恢复，应按错误信息处理。

手动更新前也应保存文件、停止环境并退出 Cage，再运行新版安装器。旧后台仍运行时安装器会拒绝覆盖。更新应用不等于更新 Linux 内所有工具；工具升级在“设置 → 环境与恢复”中单独执行。

卸载前停止环境并退出，在 Windows“设置 → 应用”中卸载 Cage。卸载保留用户数据：默认配置、代理和虚拟磁盘位于 `%USERPROFILE%\.claude-cage`，默认工作区通常位于 `%USERPROFILE%\.claude-cage-workspace`，自行打开的项目仍在原目录。WebView2 可能由其他程序共用，不随 Cage 自动卸载。需要另行删除数据时先备份，删除虚拟磁盘会丢失只保存在 Linux 环境中的文件与登录状态。

## 常见启动问题

本地开发包已适配含中文的安装目录。启动时优先读取随附 QEMU 的 UEFI 固件，无需在用户目录另存副本。在默认虚拟 CPU 配置下，Cage 会向 Windows 虚拟机监控程序平台查询可用指令；Claude Code 需要客机提供 SSE4.2。自行指定了虚拟 CPU 型号的环境仍沿用该设置。这两项修复尚未进入公开的 `v0.1.5-alpha.2` 安装包。

- 提示虚拟化不可用：检查 BIOS/UEFI 的虚拟化开关及 Windows 虚拟机监控程序平台，重启后再试。远程或嵌套虚拟机还需要外层宿主允许嵌套虚拟化。
- 提示缺少 WebView2：重新运行完整安装程序。不要从第三方站点下载同名 DLL。
- 工作台能打开，环境不能联网：检查 Cage 内是否已选择节点及网络检查结果。Windows 浏览器能联网不表示 Cage 节点已配置。
- 提示权限或路径不可用：使用当前用户有权限访问的本地目录，不把 Cage 安装目录、私有配置或磁盘根目录作为共享项目。更新器拒绝含符号链接或目录联接的安装路径。

反馈问题时提供 Cage 版本、Windows 版本与内部版本号、CPU 架构、失败步骤和错误原文。不要附带代理密码、订阅链接、会话地址或未脱敏的配置文件。

## 维护者构建与验收

构建使用 PowerShell 7、Go（遵循 `go.mod`）、Node.js、.NET 10 SDK、7-Zip 和 NSIS。依赖版本、下载地址及哈希固定在 `packaging/windows/dependencies.json`；构建时逐个校验。源码默认版本来自根目录 `VERSION`。

```powershell
pwsh -File scripts/build-windows.ps1
pwsh -File scripts/package-windows.ps1 -SkipBuild
```

应用目录输出到 `_output/windows-amd64/app`，安装器与校验文件输出到 `_output/releases/<版本>/windows`。正式签名构建使用构建脚本的 `-Release` 模式，支持 SSL.com eSigner 或当前用户证书存储中的代码签名证书；发布脚本还要求与 QEMU 构建相对应的源码归档。参数以脚本帮助为准。签名完成后再生成发行物摘要，不能对签名后的文件重新修改。两个平台同批发布时，使用[公开发行流程](public-releases.md#发布安装包)生成共用校验清单；官网仅在公开 Release 的 Windows 附件和摘要有效时显示下载按钮。

首次验证 eSigner 时，在自己的终端运行下面的命令。需要 Java 21 和 Node.js；macOS、Windows 和 Linux 都可以调用签名工具。工具固定版本与摘要保存在 `packaging/windows/signing.json`，下载内容变化时会停止，须审核后更新。输入 SSL.com 用户名、密码、凭据 ID，等工具提示后再输入验证器当时显示的六位验证码。密码和验证码不显示、不保存；签名后的副本写入新的输出目录，原文件保留。

```sh
node scripts/esigner.mjs install
node scripts/esigner.mjs sign /absolute/path/to/Cage.exe --output-dir /absolute/path/to/fresh-signed-output
```

签名单个文件不会发版。完整 Windows 构建选择 `CAGE_WINDOWS_SIGNING_PROVIDER=esigner`，并设置 `CAGE_ESIGNER_USERNAME`、`CAGE_ESIGNER_PASSWORD`、`CAGE_ESIGNER_CREDENTIAL_ID`、`CAGE_ESIGNER_CERTIFICATE_SERIAL` 和 `CAGE_ESIGNER_TOTP_SECRET`；其中 TOTP 密钥供无人值守流程生成验证码，不能用六位验证码代替。NSIS 的卸载器签名子进程不提供交互终端，因此完整打包需要这些环境配置。密码和密钥不要写入 shell 命令历史或源码。GitHub 环境配置见 [CI/CD 说明](ci-cd.md#首次配置发布材料)。

Windows 正式构建会检查可信 Authenticode 签名、时间戳与指定证书；只通过这些检查的副本才会替换待打包文件。其他平台上的签名测试不能替代 Windows 信任与安装验收。自动更新继续要求旧应用和新安装器使用相同发布者公钥，换用新密钥前需要单独安排更新迁移。

工具参数与验证器配置以 SSL.com 的 [CodeSignTool 指南](https://www.ssl.com/guide/esigner-codesigntool-command-guide/)和 [签名凭据指南](https://www.ssl.com/guide/esigner-signing-credential-guide/)为准。

CI 负责源码、原生窗口、安装器和 Windows 可执行测试；干净 Windows 10/11 上的虚拟化仍需要设备验收。每次正式发行至少记录：首次安装、WebView2 首次启动、无虚拟化时的提示、代理导入、VM 首次下载与启动、网络隔离检查、中文和空格路径共享、终端输入与缩放、退出恢复、从旧签名版升级、失败恢复、卸载后数据保留。只有真实执行的项目才能标记通过。

### 窗口上栏的外观

切换「设置 → 外观」后，菜单栏及其下拉菜单会采用所选主题的侧栏配色。Windows 11 的标题栏使用相同配色，Windows 10 的标题栏随浅色或深色模式切换。开启 Windows 对比度主题时，原生菜单和标题栏采用系统配色。
