<div align="center">

<h1>Claude 防封</h1>

**把 Claude 大小姐关起来，为了更好地保护我们的相遇**

<img src="assets/claude-cage-character.png" alt="Claude 防封" width="320" />

[![Release](https://img.shields.io/github/v/release/tan396222-prog/Claude-AntiBan?include_prereleases)](https://github.com/tan396222-prog/Claude-AntiBan/releases) [![CI](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml) [![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**[项目主页](https://github.com/tan396222-prog/Claude-AntiBan) · [Français](https://github.com/tan396222-prog/Claude-AntiBan) · [下载应用](https://github.com/tan396222-prog/Claude-AntiBan/releases) · [使用文档](docs/README.md) · [常见问题](docs/troubleshooting.md) · [参与开发](CONTRIBUTING.md)**

[简体中文](README.md) · [繁體中文](README.zh-TW.md) · [Русский](README.ru.md) · [العربية](README.ar.md) · [فارسی](README.fa.md) · [한국어](README.ko.md) · [Español](README.es.md) · [မြန်မာဘာသာ](README.my.md) · [Беларуская](README.be.md) · [پښتو](README.ps.md) · [دری](README.prs.md) · [አማርኛ](README.am.md) · [Soomaali](README.so.md) · [ትግርኛ](README.ti.md) · [Français](README.fr.md)

</div>

## 受够了 Anthropic

作为一个只是想和 Claude 对话与合作的正常用户，受够了折腾各种 VPS，指纹浏览器，环境检测，面对封号的担惊受怕和财产损失。我们提出 **Claude 防封** 项目：**一个开箱即用的桌面程序，配置好自己的网络代理，集中管理隔离环境、时区语言和环境检查。**

Claude 防封 把 Linux 终端、浏览器、文件编辑器和按需安装的 Claude Desktop 放进同一个桌面工作台。可以在本机运行轻量虚拟机，也可以通过 SSH 将工作环境部署到自己的 Linux 服务器；手机开发客户端可连接电脑，处理 Claude Code 会话与工具审批。支持以下几个重点功能：

- 和主机隔离的网络设置，Cage 自己接管和代理内部的所有网络流量
- 和主机隔离的指纹设置，包括时区、语言等，支持自动与代理 IP 同步
- 针对 Claude 使用过程中的全链路适配（浏览器登录，授权，claude code 进程）
- 内置网络与隔离检查，关键检查失败或证据不完整时暂停外网；社区和第三方检测提供补充参考

**使用 Cage 的环境检测结果：**

| 网络与隔离检查 | 浏览器环境检测（社区参考） |
| :---: | :---: |
| <a href="assets/network-checks.png"><img src="assets/network-checks.png" alt="网络与隔离检查" width="400" /></a> | <a href="assets/community-browser-check.png"><img src="assets/community-browser-check-cropped.png" alt="浏览器环境检测（社区参考）" width="400" /></a> |

**Cage 运行主界面：**

![Cage](www/public/workbench-2026-11.jpg)

## 你可以用它做什么

- **在一个窗口里完成工作。** 用终端运行 Claude Code，用编辑器查看改动，用 Cage 浏览器预览服务和完成登录。
- **控制共享范围。** 只共享默认工作区和明确打开的项目文件夹；主机的 Claude 登录、浏览器配置和 SSH 密钥不会自动导入。
- **看清网络状态。** 内置代理管理支持导入订阅、选择节点和检查出口。关键检查未通过时，环境保持离线；代理不可用时没有直连回退。
- **下次继续使用。** 工具、文件和登录状态保存在虚拟磁盘里，关闭窗口可以保留后台任务。
- **管理多个 Claude Code 订阅。** 独立登录多个账号，查看五小时和七天额度，为新建 Claude 终端分配账号；正在运行的终端保留原账号。[多账号与额度](docs/guide/accounts.md)
- **从手机继续处理任务。** Android / iPhone 开发客户端通过二维码或配对码连接电脑，创建和继续结构化 Claude Code 会话、处理工具审批，以及操作远程浏览器和 Claude Desktop。需要包含移动功能的构建，普通终端会话仍在原终端操作。[手机连接](docs/guide/mobile.md)
- **把环境放到自己的服务器。** 从侧栏添加 SSH 主机，核验指纹后部署或连接，在符合条件的 Linux x86_64 VPS 运行环境，无需 KVM。项目、账户和网络配置保存在服务器，断开客户端可保留任务。[云端部署与 SSH](docs/guide/cloud.md)

浏览器还提供收藏夹、无痕会话、Cookie 管理、扩展导入和密码保险箱；外观、资源上限、环境存储位置与工具更新都可以在工作台管理。各项入口、使用步骤和错误处理见[完整使用文档](docs/README.md)。功能以所用构建为准，手机端目前需要开发构建，公开安装包范围以对应 Release 说明为准。

## 快速开始

把这句话发给你的 agent（codex，dsh，workbuddy，反正不会是 claude code😁）

```markdown
帮我安装 https://github.com/tan396222-prog/Claude-AntiBan ，并把 Cage 的代理配置为我本机 Clash 的 xxx 节点。
```

你唯一需要的，是一个靠谱的代理节点，本项目不做任何代理推广，可以自行搜索黑色错误网站解决。

## 下载与安装

Cage 支持 macOS、Windows 和 Linux：macOS 14+ 的 Apple Silicon Mac、Windows 10 22H2 / 11 x64，以及 Debian 12 / Ubuntu 24.04 x86_64。Windows 和 Linux 仍处于开发验收阶段；安装包是否可用、签名与实测范围以各平台的发行说明为准。Intel Mac、Windows ARM64 和 Linux ARM64 尚不受支持。 [macOS](docs/getting-started.md) · [Windows](docs/windows.md) · [Linux](docs/linux.md)。

1. 打开[项目主页](https://github.com/tan396222-prog/Claude-AntiBan/releases)，点击“下载应用”并选择平台。
2. macOS：打开安装包，将 **Cage** 拖入 **Applications（应用程序）**，再从“应用程序”启动。Windows：[安装说明](docs/windows.md#安装并开始工作) · Linux：[安装说明](docs/linux.md#安装并开始工作)。
3. 按应用内引导配置自己的代理，启动环境后打开项目。

安装包随附工作台和代理核心，日常使用无需安装 Go、Node.js、Docker 或 Clash。首次启动仍需联网下载 Linux 镜像与开发工具；Cage 不提供代理服务或 Claude 账户。

源码、主页与发行物公开；可下载版本、功能、签名、公证和平台验收范围以[公开 Release](https://github.com/tan396222-prog/Claude-AntiBan/releases)说明为准。源码中的新增能力可能尚未进入公开包，手机开发客户端的安装与连接方式见[移动端文档](docs/guide/mobile.md)。

支持应用内更新的 macOS 和 Windows 版本通过所选代理下载，无直连回退；安装前请保存并停止任务。macOS 要求同一发布者的 Developer ID 签名并通过 Apple 验证，Windows 要求可信 Authenticode 签名和相同发布者公钥。Windows 未签名预览版和 Linux 系统包需手动更新。详见[升级与卸载](docs/getting-started.md#升级与卸载)。

## 三步开始

### 1. 选择代理

点击首页“配置网络与代理”，再点击“添加来源”，导入 Clash/Mihomo YAML 订阅或本地 YAML 文件，在“节点”中选择最终出口并点击“应用代理”。需要通过中转连接住宅代理时，选择“链式代理”，按顺序选择中转与最终出口。没有可用代理时，可以明确勾选“通过主机网络下载这份订阅”，或直接导入本地文件。后续可从“设置 → 网络与代理”管理节点。

### 2. 启动环境

返回工作台，点击“启动 Cage 环境”。如果本机 Git 需要安全保护，应用先说明影响，由你点击“启用保护并继续”后设置；普通项目的提交与拉取不受影响。随后下载镜像、安装工具并执行网络检查，可以在“操作详情”查看进度。看到环境就绪、关键检查通过后，即可联网工作；失败时按页面提示修复并重试。

### 3. 打开项目，运行 Claude

点击“打开项目”选择本机上的文件夹，再新建终端，输入 `claude`，在 Cage 浏览器中完成自己的账户授权。也可以先使用默认工作区，无需提前准备项目。完整步骤见[开始使用](docs/getting-started.md)。

## 使用前了解这些边界

- 共享项目中的修改和删除会直接影响本机上的文件。请使用版本控制或备份；`.git` 等元数据默认只读，Git 提交在主机上完成。
- 所有项目共用一个 Cage 环境，也共用其中的工具、账户与 Linux 文件系统。项目之间没有独立隔离。
- 当前允许客机通过代理访问公网 IPv4 TCP，DNS 通过同一代理发送；公网 UDP、IPv6、ICMP，以及客机主动访问主机和局域网的连接受到阻断。依赖这些能力的工具可能无法使用。
- 网络检查展示当前配置和采样证据，无法保证账户不会受到平台限制，也不能代替完整的虚拟化安全审计。

详细机制见[网络与数据边界](docs/getting-started.md#网络与数据边界)、[网络检查设计](docs/design/network-checks.md)和[内置代理设计](docs/design/managed-proxy.md)。

## 文档与贡献

面向用户的[官网使用文档](docs/README.md)支持按功能浏览、搜索与章节直达。源码与设计资料见[仓库文档导航](docs/README.md)。

| 你要做什么 | 从这里开始 |
|---|---|
| 第一次安装、配置代理、打开项目 | [安装与首次启动](docs/guide/installation.md) |
| 添加多个订阅、查看额度和分配终端 | [多账号与额度](docs/guide/accounts.md) |
| 手机扫码、工具审批、远程浏览器 | [手机连接与远程会话](docs/guide/mobile.md) |
| 在自己的 VPS 部署和连接环境 | [云端部署与 SSH](docs/guide/cloud.md) |
| 了解每一项设置及何时生效 | [设置参考](docs/guide/settings.md) |
| 维护存储、资源、更新与备份 | [维护、资源与数据](docs/guide/maintenance.md) |
| 处理下载、启动、联网和登录问题 | [故障排查](docs/troubleshooting.md) |
| 了解文件、按键和关闭与退出行为 | [项目、终端与文件](docs/guide/workbench.md) |
| 从源码构建或提交改进 | [贡献指南](CONTRIBUTING.md) |
| 了解模块与网络约束 | [工作台设计](docs/design/workbench.md) |
| 报告可复现问题 | [GitHub Issues](https://github.com/tan396222-prog/Claude-AntiBan/issues) |

## 来源与许可

Cage 基于 [agent-vm](https://github.com/sylvinus/agent-vm) 二次开发，上游基线为 `652688aae92c35ce536b9be52262e1cc5ec6e03a`。

项目主许可为 [MIT](LICENSE)。随附组件保留各自许可，Mihomo 作为独立可执行文件按 GPL-3.0 分发；请同时参阅 [NOTICE](NOTICE)、[第三方许可](THIRD_PARTY_LICENSES)、[第三方来源](third_party/SOURCES)及 [noVNC 来源说明](ui/vendor/novnc/README.cage.md)。上游项目的说明保存在[上游 README](docs/agent-vm-upstream.md)。

Cage 是独立社区项目，与 Anthropic 无隶属关系。

## 参考与致谢

感谢以下项目为 Cage 提供基础能力或设计参考：

- [agent-vm](https://github.com/sylvinus/agent-vm)：Cage 的二次开发底座。
- [Lima](https://github.com/lima-vm/lima)：Linux 虚拟机管理。
- [gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock)：虚拟机网络能力。
- [sshocker](https://github.com/lima-vm/sshocker) 和 [SFTP](https://github.com/pkg/sftp)：虚拟机与主机之间的文件共享能力。
- [noVNC](https://github.com/novnc/noVNC)：浏览器中的远程画面与交互。
- [Mihomo](https://github.com/MetaCubeX/mihomo)：内置代理核心。
- [code-server](https://github.com/coder/code-server)：浏览器中的代码编辑器。
- [AIO Sandbox](https://github.com/agent-infra/sandbox)：工作环境和交互体验参考。
- [claude-switcher](https://github.com/YOURSJEYO/claude-switcher) 和 [Claude-Guardian](https://github.com/DUNHKpcc/Claude-Guardian)：网络检查和安全诊断方法参考。
