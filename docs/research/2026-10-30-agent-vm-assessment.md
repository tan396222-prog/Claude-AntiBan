# 基于 agent-vm 开发 Claude 防封 的选型建议

查询日期：2026-10-30。状态：已核对固定版本源码，尚未构建、启动 VM 或验证真实登录与网络行为。本文是二次开发选型建议，不表示用户已批准具体底座及全部实现方案。

**建议优先以 [sylvinus/agent-vm](https://github.com/sylvinus/agent-vm) 为二次开发底座。** 它已具备本地 Linux VM、项目目录共享、Claude Code、浏览器版 VS Code 和 Chromium，能够复用的部分比单独采用 Lima 或 code-server 更多。AIO Sandbox 保留为交互形态参考。

本次检查基于提交 `652688aae92c35ce536b9be52262e1cc5ec6e03a`。源码从 GitHub 固定提交压缩包读取，没有运行安装脚本、访问本机 Claude 凭据或创建外部 fork。下文链接均指向该提交，便于复查。

## 需求与现成能力

目标是在本机打开一个隔离工作区，使用文件、CLI 和浏览器完成 Claude 编码全流程。主机指定项目目录双向共享；登录、授权、模型请求及工具运行留在 cage 内；时区和语言独立配置；全部对外通信必须经指定 HTTP/SOCKS5 代理，失败时断网。

| 用户可见的行为 | 上游已有能力 | 需要二次开发的部分 |
|---|---|---|
| 在本机启动隔离工作区 | 每项目 Linux VM，内嵌 Lima，提供创建、复用、停止及诊断入口 | cage 专用配置与启动检查 |
| 在网页中看文件和运行 Claude | 已有 code-server 入口及 Claude 安装选项 | 保留必要组件，统一工作区入口 |
| 操作浏览器完成登录 | 已有 Chromium、xvfb、浏览器自动化配置 | 增加可交互浏览器画面、独立持久用户目录和 cage 内授权回调 |
| 单独设置时区和语言 | Linux guest 可以承载这些设置 | 配置系统时区、locale、浏览器语言；清除不必要的宿主继承并检查实际生效值 |
| 指定代理不可绕过 | 主机侧网络栈已提供访问策略检查 | 从公网可达策略改为强制代理，关闭直连、宿主 DNS 和启动阶段的旁路 |
| 与主机共享项目文件 | 已有目录共享，主机侧保护部分文件 | 挂载白名单、专用凭据存储、实际项目 IO 验证；Git 写权限单独决定 |

上游能力来源：[README](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/README.md)、[安装配置](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/agent-vm.setup.sh)、[网页编辑器入口](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/internal/cli/code.go)。适配判断与改造项为本次分析。

## 为什么适合二次开发

项目主许可为 MIT，仓库中包含 CLI、VM 管理、主机侧网络与文件保护、安装配置、构建入口，以及修改过的第三方源码和补丁。至少在本次源码检查范围内，关键入口可定位，不必依赖一个没有构建定义的预制容器。是否可在本机完整构建仍须实际验证。[LICENSE](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/LICENSE)、[Makefile](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/Makefile)、[第三方源码来源](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/third_party/SOURCES)

其网络策略在主机侧执行。VM 内即使拥有管理员权限，也不能直接修改主机进程中的策略。上游还拒绝给 VM 增加另一张绕开该网络栈的网卡。这是实现“代理故障即断网”的有用基础，但目前策略和出站行为仍不符合本项目要求。[策略接入](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/internal/limaembed/limaembed.go#L45)、[额外网络检查](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/internal/vm/networkconfined.go#L15)

选择它也有维护代价：它内嵌并修改了 Lima、文件共享服务和网络栈，需要跟踪第三方升级与补丁；本次不能承诺这是一个已经充分验证的成熟安全产品。初期应尽量沿已有扩展点改造，避免同时更换共享文件机制、虚拟机后端和用户界面。

## 已确认的关键缺口

**网络默认仍可直连公网。** `Policy.Allows` 在没有域名限制时允许公共地址；TCP 和 UDP 转发在通过策略后直接调用主机网络连接。因此，设置 HTTP_PROXY 或增加一个域名白名单都不能独立实现本项目要求。[访问策略](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/internal/netguard/netguard.go#L183)、[TCP 转发](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/third_party/gvisor-tap-vsock/pkg/services/forwarder/tcp.go#L36)、[UDP 转发](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/third_party/gvisor-tap-vsock/pkg/services/forwarder/udp.go#L32)

建议保留 TUN 接管思路，并把主机侧策略收紧为只允许指定代理端点及明确列出的内部服务。TUN 被关闭或篡改后，外层仍拒绝其他目的地。DNS 必须一起改造：上游给宿主 DNS 服务开有内部例外，解析代码还会使用主机默认解析器，仅限制 TCP/UDP 的通常转发路径会漏掉它。初次设置、镜像下载、软件安装与常态运行的出站也应分别列清楚，避免首次启动为了安装依赖临时开放直连。[网络补丁](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/patches/lima/0005-network-isolation-hooks.patch)、[DNS 解析](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/third_party/lima/pkg/hostagent/dns/dns.go#L192)

**浏览器默认主要服务于无界面自动化。** 上游 Chrome DevTools MCP 使用 `--headless=true --isolated=true`。需要增加可供用户操作的浏览器窗口传输，并单独持久化登录所用的浏览器配置。code-server 的登录链接处理不能继续默认打开主机中的授权网站；需要把链接交给 cage 内浏览器。[浏览器配置](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/agent-vm.setup.sh#L605)、[编辑器链接参数](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/internal/cli/code.go#L225)

**独立地区设置需要明确成为 cage 配置。** 在检查的应用配置与安装入口中，没有找到同时管理系统时区、系统 locale 和浏览器语言的完整配置契约。建议统一配置并分别验证终端与浏览器的实际值。上游会继承宿主代理环境；cage 应显式控制代理、排除列表等设置，防止本机全局配置改变 cage 的行为。[代理继承处理](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/agent-vm.setup.sh#L76)

**共享文件有明确的 Git 使用限制。** 上游默认将 `.git` 等路径设为只读，因而 Claude 不能直接在共享仓库中提交。这个限制保护主机侧 Git 配置，也影响编码流程。初期可以先验证源码编辑与测试，保留默认保护；是否允许 cage 写共享 `.git`，应在要求完整 Git 工作流时单独决定。它当前使用受限的 reverse-sshfs/SFTP 路径，不能未经评估直接换成此前泛用 Lima 方案中的 virtiofs，否则会失去上游已有的文件保护机制。[共享限制](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/internal/vm/lima.go#L136)、[上游安全说明](https://github.com/sylvinus/agent-vm/blob/652688aae92c35ce536b9be52262e1cc5ec6e03a/README.md#security)

## 实现边界与最小验证顺序

建议第一轮只处理本机 macOS、单个 cage、单个指定 HTTP/SOCKS5 代理，以及一个明确共享的项目目录。入口包括 code-server 和 cage 内浏览器，不增加完整桌面、多用户平台或云端调度。

1. 在独立构建目录验证固定版本的构建与现有 VM 生命周期；运行未加固的上游前，必须明确它的直接公网出口，不能拿真实 Claude 登录态先行试用。
2. 补主机侧默认拒绝、指定代理通道和受控 DNS。先用无凭据探针验证正常请求、错误代理、断开代理、关闭 TUN、重启、IPv6 与 UDP 阻断。
3. 设置并验证系统时区、locale、浏览器语言和独立持久目录；增加最小交互式浏览器入口。
4. 实际完成一次 cage 内登录、授权回调、模型请求、项目修改与测试，最后核对主机共享目录中的结果。

第一轮通过的含义是：在约定权限范围内，指定代理是唯一外部出口；网络故障不会回退直连；用户的主机浏览器资料、家目录及凭据没有被自动导入；共享仅发生在指定项目路径。时区与语言独立配置和可见值得到验证。“完全安全”不作为无边界承诺，主机管理员、虚拟化底座与明确共享的文件始终在信任边界内。

## 其他候选的结论

- AIO Sandbox：功能接近，但上一份调研中确认的公开构建资料缺口仍未解除，因此不优先作为需要自行掌握关键实现的底座。参见[综合调研](2026-10-30-cage-options.md)。
- [dbort/claudevm](https://github.com/dbort/claudevm)：项目更小，但 README 明确说明防火墙在 guest 内、默认用户可用 sudo 关闭它，且登录令牌通过主机浏览器获取；核心缺口比 agent-vm 更多。
- [Isolade](https://github.com/isolade/isolade)：面向多 agent、多仓库和凭据代理的完整工作台，与本次精简文件、CLI、浏览器的目标相比，需要先消化更多产品机制，暂不优先。

这些是本次目标下的适配判断，不是对项目整体质量的排名。最终采用 agent-vm 前仍需构建和真实 VM 验证；本次完成了选型所需的关键源码核查。
