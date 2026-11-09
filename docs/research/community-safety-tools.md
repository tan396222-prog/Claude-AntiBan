# 社区检测工具与 Cage 的接入边界

本文供 Cage 的维护者选择检测来源和实现方式。核查日期为 2026-10-31；网页服务随时可能改变，仓库结论绑定到下列源码树快照（Git tree）。

## 接入结论

Cage 的联网准入采用分级规则：隔离、出站约束和配置生效属于必需检查；IP 信誉与地区语言建议属于提示。社区工具可以补充检查项目和解释，但第三方评分不能证明本机隔离有效，也不能预测账号是否被限制。

| 来源 | 已核查的用途 | Cage 的接入方式 |
|---|---|---|
| [ip.net.coffee/claude](https://ip.net.coffee/claude/) | 出口、ASN、IP 属性与信誉评分，DNS、WebRTC 和浏览器信息检查 | 提供在 Cage 浏览器中打开的入口；结果注明第三方来源。自动取数暂不依赖其网页内部接口 |
| [fuck-claude.com](https://fuck-claude.com/) | 浏览器时区、语言、字体、Intl 等信号的加权估计；页面明确区分估计与 Claude 的实际判断 | 作为自愿使用的浏览器诊断入口。中文语言或字体本身不构成 Cage 隔离失败 |
| [claude-switcher](https://github.com/YOURSJEYO/claude-switcher/tree/525c261a3f162a149100ed8eb36dfc82767e841f) | Mac 代理、环境检查与进程守护 | 参考 STUN 结果解释、出口一致性和故障提示；不引入其整机配置修改与账号切换流程 |
| [Claude-Guardian](https://github.com/DUNHKpcc/Claude-Guardian/tree/06fe998d753f697c47ae0b67be5fe91451532a1f) | 面向 Agent 的准备、诊断和修复工作流；包含网络检查与证据判定脚本 | 参考证据完整性、故障演练和生命周期检查方法；不安装或运行其宿主修改脚本 |

两个网页的前端均包含外部统计脚本。打开时，站点会收到访问所需的网络信息，并可读取其页面内可访问的浏览器信息。因此入口只在用户点击后打开，位置必须是 Cage 内的浏览器。不能从 Mac 工作台页面执行它们的浏览器检测，再把结果当成 Cage 的结果。源码依据：[IP 检测页脚本](https://ip.net.coffee/claude/claude-page.js?v=20260916a)、[浏览器检测页脚本](https://fuck-claude.com/_astro/Detector.astro_astro_type_script_index_0_lang.BjyUZeoD.js)。

IP 检测页调用了 `/api/geoip/`、`/api/iprisk/` 等接口。这只能确认网页的实现方式；本次尚未核实供第三方集成的稳定接口约定、使用条件和限额。自动接入应先补齐这些信息，并明确超时、服务停用和格式变化时显示“未知”。不抓取分数后伪装为 Cage 自有检测，也不把私人配置、凭据或完整报告提交给检测站。

## 值得采用的检查方法

Claude-Guardian 的出口判定器区分已建立证据、已观察到违规和证据不完整；它处理调用者提供的观察数据，本身不采集网络流量。只有所需层次齐全才形成肯定结论，重启、休眠唤醒和网络变化的检查另外记录。这适合 Cage 的报告设计：每项都要说明采样位置、方法、时间和适用配置。来源：[egress_verifier.py](https://github.com/DUNHKpcc/Claude-Guardian/blob/06fe998d753f697c47ae0b67be5fe91451532a1f/skills/claude-guardian/scripts/egress_verifier.py)。

claude-switcher 的 STUN 检查会区分返回了哪个出口、探测失败，以及没有 UDP 响应。它的安全说明也明确进程守护与防火墙的能力差异。Cage 应保留这种限定：超时不能单独证明拦截有效，需要与 VM 外网络层实际拒绝请求的证据结合。来源：[STUN 检查与调用方](https://github.com/YOURSJEYO/claude-switcher/blob/525c261a3f162a149100ed8eb36dfc82767e841f/core/claude-switch.sh#L1256)、[安全边界](https://github.com/YOURSJEYO/claude-switcher/blob/525c261a3f162a149100ed8eb36dfc82767e841f/SECURITY.md)。

对 Cage 的具体要求如下：

- 网络约束在 VM 外执行。停止浏览器进程、隐藏按钮或 guest 内的守护程序不能替代出站控制。
- 报告绑定配置和本次 VM 运行实例。重启、配置变化或关键监测失败后，旧报告不能继续授权联网。
- 关键项包含失败、未知和未检测状态。缺失证据不能通过空列表、默认值或第三方高分补齐。
- 明确区分代码策略检查、真实 guest 探针和外部观测。检查一个 HTTP 出口不能概括 DNS、IPv6、UDP 或浏览器的全部路径。
- 故障演练在启动检查或用户主动重检时执行。运行中的监测不擅自停止代理或中断用户任务来制造故障。
- 发生阻断后，报告应解释原因，并保留本地终端、文件和修复入口。

## 许可与使用范围

claude-switcher 的仓库许可证为 [MIT](https://github.com/YOURSJEYO/claude-switcher/blob/525c261a3f162a149100ed8eb36dfc82767e841f/LICENSE)；Claude-Guardian 的项目说明标明 [GPL-3.0-only](https://github.com/DUNHKpcc/Claude-Guardian/blob/06fe998d753f697c47ae0b67be5fe91451532a1f/README.md#反馈与许可)。本次仅阅读资料和源码，没有复制第三方实现到 Cage。直接复用、打包分发或翻译大段资料前，需要核对相应文件的许可及归属。

社区关于封禁原因、评分权重和使用节奏的说法，保留为有来源的经验，不升级为平台官方规则。Anthropic 的[位置数据说明](https://privacy.claude.com/en/articles/11186740-does-claude-use-my-location)提及 IP 和其他信号；它没有公开一套可由这些社区分数替代的账号安全判定。
