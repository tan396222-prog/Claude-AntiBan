# Windows Privacy Guard 与 Cage 的比较

本文供 Cage 维护者判断产品差异与可借鉴的能力。核查日期：2026-11-03。外部项目固定到提交 `cf7e6ed14736b8f9762a10bb3e94ee6f40cac797`；Cage 依据当前工作区，HEAD 为 `7090ecf5a3464b46d6b05442e456d929839889fe`，包含尚未提交的在途修改。本次阅读文档和关键源码，没有部署外部项目、执行其脚本或进行两产品的运行性能与泄露对照测试。

## 阅读版：用户会遇到什么差别

Cage 的主要优势是把浏览器、终端和开发工具放进统一的隔离环境，并在环境外控制网络。Windows Privacy Guard 的主要优势是保留 Windows 原生使用方式，提供定向登录清理、应用联网保护和独立浏览器入口。两者都有失败关闭和证据过期机制；虚拟机隔离与浏览器抗追踪需要分别评价。

例如，让 Claude 调用 Python 下载依赖时，Cage 内新进程继续经过相同的虚拟机出口。Windows 应用保护要检查这个 Python 进程是否属于防火墙登记范围，不能从 Claude 主程序通过验收推导整个工具链都已受保护。此判断来自两边的约束对象：应用身份与虚拟机网络，见下文源码证据。

| 用户关心的方面 | Windows Privacy Guard | Cage 当前实现与边界 |
|---|---|---|
| 日常工作方式 | Windows 上的 Claude 桌面应用、独立 Edge／Brave 配置；通过启动器、后台服务和面板协作 | 同一窗口管理 Linux 终端、浏览器、编辑器及 Linux 桌面应用；需要虚拟化和额外磁盘、内存，具体开销未对测 |
| 本机文件与登录资料 | 定向清理 Claude 登录残留；独立浏览器配置不复制普通浏览器登录。项目未建立通用的宿主文件隔离边界 | 主机目录按授权共享，宿主登录与密钥不自动导入。共享文件仍可被修改、删除；所有项目共用一个环境 |
| 网络保护范围 | 防火墙按登记的可执行文件／包身份限制出口；手动浏览器另有代理参数、偏好和进程检查，不承诺按浏览器配置文件实现内核隔离 | 在虚拟机外约束客机业务流量，不依赖应用是否遵循代理环境变量；宿主应用不属于这个范围 |
| DNS、UDP、IPv6 | 有作用域 DNS、WebRTC 偏好、Windows 内核规则。某入口的策略通过不能替代其他入口的实包验证 | 客机业务 DNS 经同一代理；公网 UDP、IPv6、ICMP 拒绝。代理自身启动解析与业务 DNS 分别说明 |
| 时区与语言 | 当前手动浏览器快照要求 Windows 系统为丹佛时区、浏览器英语；准备工具不会自动修改整机时区 | 改虚拟机内时区、语言和浏览器偏好，宿主可保持原设置；可按出口建议配置 |
| 浏览器抗追踪 | Brave 入口采用浏览器原生隐私功能；日常手动入口不连接调试协议 | Chromium 通过私有调试管道管理标签。已隔离宿主环境，但未建立与 Brave 原生指纹随机化等价的能力 |
| 部署与维护 | 可构建源码、Skill 和面板；依赖用户准备多个组件，缺少通用的完整安装事务 | 应用集成工作台与代理管理。macOS、Windows、Linux 的实测范围不同，不能概括为三平台全面验收 |

外部能力与限制依据：[后端说明](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/backend/README.md)、[手动浏览器说明](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/references/manual-browsers.md)、[定向清理与项目范围](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/README.md)。Cage 依据：[工作台设计](../design/workbench.md)、[网络检查](../design/network-checks.md)、[内置代理](../design/managed-proxy.md)。

### 截图能够支持的结论

外部项目的截图记录了维护者某次出口、DNS、WebRTC、时区和语言的结果。项目明确说明这些图片来自不同阶段与浏览器。面板的 36 条 WFP 规则核验属于内核策略身份与条件检查，没有宣称同时完成 UDP／IPv6 正负实包对照。语言评分 57→23 和 Brave 的 B 级均来自第三方检测，不能作为两产品整体安全性或账号风险的排名。[截图与限定条件](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/docs/before-after.md)

Cage 保留中文字体与 Emoji 字体以支持正常使用，因此也不应把消除这些字体信号作为安全验收目标。虚拟机使网页接触客机环境，但客机自身的浏览器、字体、渲染和持久登录仍可形成关联信号。Brave 对部分可指纹化值进行随机化的机制，与运行在虚拟机中的 Chromium 是不同能力；具体效果仍需同条件测试。[Brave 机制说明](https://brave.com/privacy-updates/3-fingerprint-randomization/)

### 出口监测的实际取舍

外部网关将出口许可设为 4 秒，并有高频本地检查和出口采样；Cage 的放行租约为 90 秒，运行监测约每 30 秒检查一次。外部实现更强调证据新鲜度，代价可能是探测成本和网络抖动时的暂停，这属于代码推断，尚未做体验对照。它还实现了无新结果时保留连接但暂停转发的限时分支，不能把 4 秒理解为所有连接一律立即关闭。[外部网关源码](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/backend/source/guard.py#L47-L64)

两边都无法仅凭周期探测保证代理服务商每条连接使用相同出口。Cage 的 90 秒租约也不表示代理断线后会直连；实际转发没有直连回退。两种失败需要分开：代理不可用，以及代理仍可用但出口在两次采样间改变。[Cage 网络约束](../design/network-checks.md)

### 建议

1. 保留 Cage 的统一虚拟机边界。它覆盖任意客机工具的方式更适合开发工作台，且可保留宿主原有时区、浏览器和登录状态。
2. 借鉴外部项目的证据展示：分别显示配置读回、运行时约束、实际流量测试及其时间与范围。Cage 已有对应分层设计，可进一步检查界面是否足够清楚。
3. 将浏览器抗追踪作为独立评估项。如评估 Brave，应在相同代理、相同站点和相同使用任务下比较兼容性、关联信号与资源开销；不能仅凭一次评分决定替换浏览器。
4. 复核出口异常发现时间与误暂停之间的平衡，先测故障行为和探测成本，再决定是否调整周期，不直接照搬 4 秒参数。

这些是后续建议，本次没有改变产品设计、配置或运行中的环境。Cage 的平台文档记录了 Windows 11 单台实体机验收，以及 Linux 云端／软件模拟验证；Windows 10、Linux KVM 与部分真实业务路径仍有未测项。[Windows 范围](../windows.md)、[Linux 范围](../linux.md)

## 证据版：关键实现位置

| 结论 | 外部项目源码 | Cage 当前工作区 |
|---|---|---|
| 按应用身份限制与按 VM 限制的区别 | [ClaudeWfpGuard.cs:195](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/backend/source/wfp/ClaudeWfpGuard.cs#L195)：身份条件与四类拒绝规则 | `internal/cage/egress.go:24`、`:49`；`internal/vm/networkconfined.go:18`：拒绝不经受控网络的驱动与额外网卡 |
| 业务 DNS 不走宿主解析回退 | [install-scoped-dns.ps1](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/backend/source/dns/install-scoped-dns.ps1)：指定域的 NRPT 定向 | `internal/cage/egress.go:131`：经同一代理发送 DNS；代理启动解析边界另见设计文档 |
| 短期放行证据 | [guard.py:47](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/backend/source/guard.py#L47)：4 秒许可及无结果暂停 | `internal/cage/gate.go:21`：90 秒；`internal/workbench/safety.go:553`：约 30 秒监测间隔 |
| 浏览器原生偏好与调试管道 | [brave_browser_policy.py:17](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/backend/source/brave_browser_policy.py#L17)：读回 WebRTC 偏好；同文件 `arguments` 定义启动参数 | `internal/cage/browser-agent.mjs:37`：Chromium 私有调试管道、关闭 AutomationControlled 标记与 QUIC；这些选项不能证明没有可检测特征 |
| 系统时区归属 | [brave_browser_policy.py:13](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/backend/source/brave_browser_policy.py#L13) 与手动浏览器说明：固定 Denver/Windows 时区要求 | `internal/cage/guest-env.sh:3`：设置客机时区；`:9`：客机进程环境 |
| 中文字体保留 | [before-after.md](https://github.com/SWL-1229/claude-windows-privacy-guard/blob/cf7e6ed14736b8f9762a10bb3e94ee6f40cac797/docs/before-after.md)：说明仍存在中文字体与 Emoji 信号 | `internal/cage/guest-setup.sh:3`：安装 Noto CJK 与 Emoji 字体 |
| 项目之间共用环境 | 外部项目没有提供通用项目 VM 隔离机制 | `docs/design/workbench.md:44`、`:46`：单持久 VM 与已授权项目共享；不能宣传为项目之间隔离 |

本次核对用于比较机制与交付范围，不构成完整安全审计。源码存在、维护者截图、离线测试、当前机器真实运行验证是不同证据等级。
