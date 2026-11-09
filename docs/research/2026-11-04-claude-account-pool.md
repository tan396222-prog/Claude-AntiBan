# Claude Code 多账号与订阅额度

核对日期为 2026 年 11 月 5 日。Cage 的账号面板管理用户本人可以使用的订阅。用户可以同时打开两个项目终端，查看各自使用的账号，也可以暂停一个账号接收新终端。账号的登录、模型请求和续期均由客机里的官方 Claude Code 处理。

## 两个现有工具的取舍

| 工具及模式 | 用户能做什么 | Cage 的选择 |
| --- | --- | --- |
| CC Switch v4.0.25，隔离终端 | 在独立配置目录中并行使用多个账号。另有更换全局登录的模式，据项目说明，进行中的会话也可能改用新账号。订阅账号不参加它的代理故障转移。 | 借鉴独立目录。Cage 在创建终端时完成分配，暂停账号后原终端仍属于它。[项目说明](https://github.com/farion1231/cc-switch/blob/v4.0.25/docs/guides/claude-official-accounts-guide-zh.md) |
| CCS v8.10.0，原生账号 | 为工作和个人账号分开登录，配置目录和默认会话历史各自独立。 | 每个账号保留官方 CLI 的独立配置和历史。Cage 共用同一 VM、项目文件和网络出口。[原生账号说明](https://docs.ccs.kaitran.ca/providers/concepts/claude-accounts) |
| CCS，代理号池 | 另行配置代理凭据，按请求路由。提供会话粘性、账号冷却和流开始前的故障转移。 | 订阅路径在官方 CLI 创建会话时分配，不接管请求流，工具执行途中不会由 Cage 自动重放。[号池说明](https://docs.ccs.kaitran.ca/features/pool) · [路由源码](https://github.com/kaitranntt/ccs/blob/v8.10.0/src/cliproxy/routing/routing-strategy.ts) |

CC Switch 和 CCS 的额度功能中都能找到读取订阅凭据、调用 OAuth 用量端点的代码。这个端点不是本方案面向 Cage 的公开额度接口。Anthropic 允许用户在原版 Claude Code 中自己登录，也明确限制第三方收集或中转订阅令牌、代表用户用订阅凭据路由请求。产品内运行官方 CLI 还须遵守有关商业条款、保留原版认证方式和由用户直接结算的条件。[CC Switch 用量源码](https://github.com/farion1231/cc-switch/blob/v4.0.25/src-tauri/src/services/subscription.rs) · [CCS 用量源码](https://github.com/kaitranntt/ccs/blob/v8.10.0/src/web-server/usage/native-quota-collector.ts) · [Anthropic 对产品集成与认证的说明](https://code.claude.com/docs/en/legal-and-compliance)

## 登录与存储

一个 Cage 本地账户解锁整份工作台，可以在一个 Linux 客机中管理多个 CLI 登录。主机保存名称、订阅身份摘要、是否参与调度和最近用量。面板中的新登录各有一个固定的配置目录。普通终端的默认登录则引用原目录，Cage 单独核对邮箱和组织，发现身份后需用户选择加入调度。CLI 自己把登录状态写入该目录，用户在临时浏览器中完成官方授权。认证页面的 Cookie 与日常浏览器隔离，授权参数不出现在工作台标签元数据或最近网址中。操作系统用户相同的客机程序仍处于同一信任边界，不能把配置目录当作不同租户之间的安全隔离。[官方多账号认证](https://code.claude.com/docs/en/authentication#log-in-with-multiple-accounts) · [凭据存储](https://code.claude.com/docs/en/authentication#credential-management)

桌面应用有自己的账号登录。官方文档明确区分 CLI 和 Desktop 的认证配置。网页或桌面端的账号菜单可以在同邮箱绑定的个人和团队组织之间切换。未查到让外部程序给 Desktop 的每段对话指定个人 Pro 登录的受支持接口。Cage 当前也只管理一个 Desktop 服务与密钥环。首版把调度范围限定在工作台新建的 Claude Code 终端。普通 shell、独立命令行、Desktop 和网页继续使用各自的登录。[Claude Code 认证](https://code.claude.com/docs/en/authentication#credential-management) · [官方账号切换说明](https://support.claude.com/en/articles/13189465-log-in-to-your-claude-account) · [Cage 工作台](../design/workbench.md)

## 调度和缓存

终端 A 已经在改文件时，即使用户停用其账号，或服务端报告额度用完，Cage 都保留 A 的进程、项目和登录目录。已有连接重连原终端。只有新建终端才在可用账号间分配。这也避免了把已执行的文件编辑、测试或 shell 命令再运行一次。终端关闭后，Claude 的历史仍在该账号的目录。关闭终端或停止 VM 和暂时断开查看有不同结果。

配置目录隔离本地历史，服务端自己管理提示缓存。同一账号、相同模型和稳定的提示前缀有利于缓存复用，跨账号继续一段对话不能承诺缓存命中。新项目开新会话，也不该因为选到同一账号就展示“缓存已命中”。需要验证时可以观察官方状态行提供的缓存读取量，以及支持版本的主会话命中率。订阅的额度无须通过主动发送模型请求来探测。[Claude Code 缓存字段](https://code.claude.com/docs/en/statusline#prompt-cache-fields) · [Claude API 的缓存边界](https://platform.claude.com/docs/en/build-with-claude/prompt-caching#cache-storage-and-sharing)

## 额度能看到什么

Pro 账号的五小时和七天窗口分别显示用量。账号面板通过客机里的官方 Claude Code 主动查询 `/usage` 的结构化数据，会话状态行作为补充。控制请求 `get_usage` 支持只取订阅额度，跳过本地历史分析。该结构在官方 SDK 中仍标为实验性，字段缺失时应保留旧读数并说明结果。CLI 确认登录和能否取回用量是两个独立结果；有百分比但无重置时间时，只能显示百分比。其他设备也使用同一订阅，查到的数值可能来自 CLI 的近期缓存。[Claude Code 用量查询](https://code.claude.com/docs/en/costs#using-the-usage-command) · [官方 SDK 0.3.314 类型定义](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.314/sdk.d.ts) · [状态行字段](https://code.claude.com/docs/en/statusline#available-data)

调度先排除已暂停、身份不可用和仍在耗尽窗口内的账号。可分配的账号中优先选占用终端较少的，再比较有时效的额度，负载相同、余量接近时，优先沿用这个项目最近五分钟分配到的账号。其余平手情形按轮次分配。五分钟是本地调度偏好，不代表服务端缓存的寿命。已登录但暂时查不到额度的账号仍可分配终端。没有任何可用账号时向用户说明原因。不回退到隐藏账号、付费 API 或其他出口。零个账号记录的已有安装继续走原终端入口，原有个人数据不需搬迁。独立新登录经官方身份确认后才计入账号池。旧版留下的未绑定草稿仍可以撤销。
