<div align="center">

<h1>Claude 防封</h1>

**把 Claude 大小姐关起来，为了更好地保护我们的相遇**

<img src="assets/claude-caged-logo.png" alt="Claude 防封" width="320" />

[![Release](https://img.shields.io/github/v/release/tan396222-prog/Claude-AntiBan?include_prereleases)](https://github.com/tan396222-prog/Claude-AntiBan/releases) [![CI](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml) [![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**[项目主页](https://github.com/tan396222-prog/Claude-AntiBan) · [下载应用](https://github.com/tan396222-prog/Claude-AntiBan/releases) · [使用文档](docs/README.md) · [常见问题](docs/troubleshooting.md)**

[简体中文](README.md) · [繁體中文](README.zh-TW.md) · [Русский](README.ru.md) · [العربية](README.ar.md) · [فارسی](README.fa.md) · [한국어](README.ko.md) · [Español](README.es.md) · [မြန်မာဘာသာ](README.my.md) · [Беларуская](README.be.md) · [پښتو](README.ps.md) · [دری](README.prs.md) · [አማርኛ](README.am.md) · [Soomaali](README.so.md) · [ትግርኛ](README.ti.md) · [Français](README.fr.md)

</div>

## 受够了 Anthropic

作为一个只是想和 Claude 对话与合作的正常用户，受够了折腾各种 VPS，指纹浏览器，环境检测，面对封号的担惊受怕和财产损失。我们提出 **Claude 防封** 项目：**一个开箱即用的桌面程序，配置好自己的网络代理，集中管理隔离环境、时区语言和环境检查。**

Claude 防封 让你在熟悉的系统里与 Claude 一起工作：Mac 上用 macOS 虚拟机，Windows 上用 Windows 虚拟机，也可以选择轻量的 Linux 环境。终端、浏览器、编辑器和官方 Claude Desktop 汇集在同一个工作台，项目按需共享，网络独立管理；还可以连接自己的服务器，或用手机接续电脑上的任务。支持以下几个重点功能：

- 和主机隔离的网络设置，Cage 自己接管和代理内部的所有网络流量
- 和主机隔离的指纹设置，包括时区、语言等，支持自动与代理 IP 同步
- 一键修改时区和语言：内置美国、加拿大、英国、欧洲、日本、韩国、新加坡、港台、澳大利亚等预设，自动推荐与代理出口匹配的组合，也可用命令 `cage region apply --preset us-west`
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
- **用熟悉的系统开发。** 选择 macOS、Windows 或 Linux 客机，使用对应平台的工具；也可自动匹配本机系统版本，下载官方镜像并创建独立环境。 [选择客机系统](docs/guide/guest-systems.md)
- **控制共享范围。** 只共享默认工作区和明确打开的项目文件夹；主机的 Claude 登录、浏览器配置和 SSH 密钥不会自动导入。
- **看清网络状态。** 内置代理管理支持导入订阅、选择节点和检查出口。关键检查未通过时，环境保持离线；代理不可用时没有直连回退。
- **下次继续使用。** 工具、文件和登录状态保存在虚拟磁盘里，关闭窗口可以保留后台任务。
- **管理多个 Claude Code 订阅。** 查看额度、为新会话分配账号，并在明确授权后切换账号接续历史。 [Claude Code](docs/guide/accounts.md)
- **从手机继续处理任务。** Android / iPhone 配对电脑，接续 Claude Code 对话与工具审批，或操作远程浏览器和 Claude Desktop。[手机连接](docs/guide/mobile.md)
- **把环境放到自己的服务器。** 通过 SSH 部署或连接 Linux VPS，工具、任务和数据留在服务器，无需 KVM。[云端部署与 SSH](docs/guide/cloud.md)
- **通过私网连接设备。** 在 Cage 内登录 Tailscale，查看网络中的设备，使用 SSH、Web 与其他 TCP 服务。 [Tailscale 私网](docs/guide/tailscale.md)

浏览器收藏、无痕会话、Cookie、扩展、密码保险箱，以及外观、资源、存储和工具更新，见[完整使用文档](docs/README.md)。

## 快速开始

把这句话发给你的 agent（codex，dsh，workbuddy，反正不会是 claude code😁）

```markdown
帮我安装 https://github.com/tan396222-prog/Claude-AntiBan ，并把 Cage 的代理配置为我本机 Clash 的 xxx 节点。
```

你唯一需要的，是一个靠谱的代理节点，本项目不做任何代理推广，可以自行搜索黑色错误网站解决。

## 下载与安装

Cage 支持 macOS、Windows 和 Linux：macOS 14+ 的 Apple Silicon Mac、Windows 10 22H2 / 11 x64，以及 Debian 12 / Ubuntu 22.04 / 24.04 x86_64。Windows 11 客机需要支持 Hyper-V 的 Windows 11 专业版、企业版或教育版主机；其他受支持的 Windows 主机可使用 Debian 或 Ubuntu 22.04。Intel Mac、Windows ARM64 和 Linux ARM64 暂不支持。 [macOS](docs/getting-started.md) · [Windows](docs/windows.md) · [Linux](docs/linux.md)。

1. 打开[项目主页](https://github.com/tan396222-prog/Claude-AntiBan/releases)，点击“下载应用”并选择平台。
2. macOS：打开安装包，将 **Cage** 拖入 **Applications（应用程序）**，再从“应用程序”启动。Windows：[安装说明](docs/windows.md#安装并开始工作) · Linux：[安装说明](docs/linux.md#安装并开始工作)。
3. 按应用内引导配置自己的代理，启动环境后打开项目。

安装包随附工作台和代理核心，无需先安装 Go、Node.js、Docker 或 Clash。首次创建环境时选择客机系统与存储位置，再准备对应的官方系统镜像和开发工具。macOS、Windows 系统按各自许可使用；Cage 不提供系统授权、代理服务或 Claude 账户。

源码与安装包公开，版本说明和校验文件见 [GitHub Releases](https://github.com/tan396222-prog/Claude-AntiBan/releases)。系统选择、镜像来源与数据保留方式见[客机系统指南](docs/guide/guest-systems.md)。

功能以所用构建为准，公开安装包的能力与验证范围以发行说明为准。

macOS 和已签名 Windows 版本支持通过所选代理更新；Windows 未签名预览版与 Linux 使用安装包手动更新。更新前保存并停止任务，个人数据继续保留。 [升级与卸载](docs/guide/maintenance.md)

## 三步开始

### 1. 选择代理

导入 Clash/Mihomo YAML 订阅或本地配置，选择节点并点击“应用代理”；需要中转时可配置链式代理。[网络与代理](docs/guide/network.md)

### 2. 启动环境

首次选择客机系统或“自动匹配本机系统版本”，再选择存储位置。点击“安装客户机”或“启动 Cage 环境”，按引导完成安装与授权；进度和错误可在“操作详情”查看。以后启动复用原环境。

### 3. 打开项目，运行 Claude

点击“打开项目”选择本机上的文件夹，再新建终端，输入 `claude`，在 Cage 浏览器中完成自己的账户授权。也可以先使用默认工作区，无需提前准备项目。完整步骤见[开始使用](docs/getting-started.md)。

## 使用前了解这些边界

- 共享项目中的修改和删除直接影响本机原文件，包括 Git 操作。请使用版本控制或备份，并核对提交结果。
- 同一份 Cage 配置中的项目共用所选客机系统、工具、账户与文件系统，项目之间没有独立隔离。更换客机系统需要独立安装，原环境的数据继续保留。
- 默认公网出站支持 IPv4 TCP，DNS 通过同一代理；公网 UDP、IPv6、ICMP 及主机和局域网直连受限。主动启用 Tailscale 后，可按私网权限连接设备。
- 网络检查展示当前配置和采样证据，无法保证账户不会受到平台限制，也不能代替完整的虚拟化安全审计。

详细机制见[网络与数据边界](docs/getting-started.md#网络与数据边界)。

## 文档与贡献

面向用户的[官网使用文档](docs/README.md)支持按功能浏览、搜索与章节直达。各项操作见[使用文档导航](docs/README.md)。

| 你要做什么 | 从这里开始 |
|---|---|
| 让自己的 Agent 下载、安装、配置和排查 Cage | [Agent 使用指南](docs/guide/agent.md) |
| 第一次安装、配置代理、打开项目 | [安装与首次启动](docs/guide/installation.md) |
| 选择 macOS、Windows、Debian 或 Ubuntu 22.04 客机，使用原生开发工具 | [选择客机系统](docs/guide/guest-systems.md) |
| 添加多个订阅、查看额度和分配终端 | [多账号与额度](docs/guide/accounts.md) |
| 手机扫码、工具审批、远程浏览器 | [手机连接与远程会话](docs/guide/mobile.md) |
| 在自己的 VPS 部署和连接环境 | [云端部署与 SSH](docs/guide/cloud.md) |
| 了解每一项设置及何时生效 | [设置参考](docs/guide/settings.md) |
| 维护存储、资源、更新与备份 | [维护、资源与数据](docs/guide/maintenance.md) |
| 处理下载、启动、联网和登录问题 | [故障排查](docs/troubleshooting.md) |
| 了解文件、按键和关闭与退出行为 | [项目、终端与文件](docs/guide/workbench.md) |
| 报告可复现问题 | [GitHub Issues](https://github.com/tan396222-prog/Claude-AntiBan/issues) |

## 来源与许可

Cage 基于 [agent-vm](https://github.com/sylvinus/agent-vm) 二次开发。

项目主许可为 [MIT](LICENSE)。随附组件保留各自许可，Mihomo 作为独立可执行文件按 GPL-3.0 分发；请同时参阅 [NOTICE](NOTICE) · [第三方许可](THIRD_PARTY_LICENSES) · [noVNC](ui/vendor/novnc/LICENSE.txt)。

Cage 是独立社区项目，与 Anthropic 无隶属关系。

## 参考与致谢

感谢以下项目为 Cage 提供基础能力或设计参考：

- [agent-vm](https://github.com/sylvinus/agent-vm)：Cage 的二次开发底座。
- [Lima](https://github.com/lima-vm/lima)：虚拟机管理与 macOS、Linux 客机的运行基础。
- [gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock)：虚拟机网络能力。
- [sshocker](https://github.com/lima-vm/sshocker) 和 [SFTP](https://github.com/pkg/sftp)：虚拟机与主机之间的文件共享能力。
- [noVNC](https://github.com/novnc/noVNC)：浏览器中的远程画面与交互。
- [Mihomo](https://github.com/MetaCubeX/mihomo)：内置代理核心。
- [code-server](https://github.com/coder/code-server)：浏览器中的代码编辑器。
- [AIO Sandbox](https://github.com/agent-infra/sandbox)：工作环境和交互体验参考。
- [claude-switcher](https://github.com/YOURSJEYO/claude-switcher) 和 [Claude-Guardian](https://github.com/DUNHKpcc/Claude-Guardian)：网络检查和安全诊断方法参考。

