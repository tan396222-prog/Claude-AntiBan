---
title: 让 Agent 配置 Cage
description: 给外部 Agent 的完整入口：下载校验、安装、配置代理与客机、任务跟踪、故障定位和恢复。
group: 开始使用
order: 5
keywords: Agent CLI JSON 自动化 配置 接口 安装 诊断 修复 capabilities doctor llms
---

你可以把下面这段话交给自己的 Agent，让它根据你的目标准备 Cage。你只需提供自己的代理来源、选择工作环境，并在操作系统要求时完成授权。Cage 账户密码、Claude 登录、许可证确认和项目共享范围由你决定。

> 请阅读 agent.md ，协助我安装、配置并验证 Cage。先检查本机安装和接口能力，再按实际状态操作；完成后告诉我环境是否就绪、网络是否通过检查，以及还需要我完成什么。保留所有现有账户、项目和虚拟机数据。

本页是当前源码与开发构建的接口说明。公开 Release 可能尚未包含 `cage agent`。安装后必须先运行能力探测；遇到未知命令时，使用该版本的界面和发行说明，或安装用户授权的较新构建。不要仅凭版本字符串推断接口已存在。

## Agent 从哪里开始

下文的 `cage` 代表 Cage 安装包内的后台 CLI。先找到实际文件；不要把桌面启动程序或 Linux 上同名的 Wayland 程序当作 CLI。

| 平台 | 默认 CLI 位置 | 调用示例 |
| --- | --- | --- |
| macOS | `/Applications/Cage.app/Contents/Resources/cage` | `"/Applications/Cage.app/Contents/Resources/cage" agent capabilities` |
| Windows | `%LOCALAPPDATA%\Programs\Cage\resources\cage.exe`，自定义安装目录以实际位置为准 | `& "$env:LOCALAPPDATA\Programs\Cage\resources\cage.exe" agent capabilities` |
| Linux | `/usr/bin/claude-cage` | `claude-cage agent capabilities` |

`agent capabilities` 输出本构建的协议版本、操作清单、输入字段和退出码，不启动后台或创建配置。首次配置与每次升级后都先读取一次。机器阅读入口为 `/agent.md`、`/llms.txt`、`/llms-full.txt`，使用 Cage 的外部 Agent 从本页开始。

所有 `agent` 命令自动输出 JSON，`--json` 可省略。示例把 `cage` 作为占位命令；执行时替换为上表路径。`--config /absolute/path/config.json` 明确选择配置；省略时使用 `CLAUDE_CAGE_CONFIG`，再省略则使用用户主目录下的 `.claude-cage/config.json`。每次输出中的 `config_path` 是实际目标。桌面配置的远端选择不会改变这个目标；管理服务器时，也可通过下文的 desktop / selected 范围管理桌面连接，或在授权的 SSH 通道内运行服务器接口。

## 下载、校验与安装

下载助手位于官网的 `/agent-bootstrap.mjs`，需要 Node.js 20 或更新版本，无额外依赖。可以先下载并检查脚本，再执行。它只发现 Release、下载和校验安装包；输出后续安装步骤与参数数组。Agent 按平台执行安装，系统权限提示由用户处理。

macOS/Linux 也可先获取 `/cage-install.sh` 再运行，参数与下载助手相同，需要 Node 和 curl。`/install.sh` 保留给上游 agent-vm 兼容流程，安装 Cage 时使用本节列出的专用入口。

```sh
curl --proto '=https' --proto-redir '=https' -fL \
  https://raw.githubusercontent.com/tan396222-prog/Claude-AntiBan/main/www/public/agent-bootstrap.mjs -o agent-bootstrap.mjs
node agent-bootstrap.mjs plan --platform darwin-arm64
node agent-bootstrap.mjs download --platform darwin-arm64 \
  --version v0.1.6-alpha-preview --destination /absolute/path/downloads
```

Windows 可用 PowerShell 下载脚本后执行 `node .\agent-bootstrap.mjs plan --platform win32-amd64`；Linux 的平台参数为 `linux-amd64`。省略平台时自动识别当前 Node 进程的平台和架构。指定 `--version` 会固定发行标签；省略时查询 GitHub 的 latest Release，它可能不包含预发行版，也可能缺少某个平台的附件。

助手从同一标签读取安装包与 `SHA256SUMS`，拒绝不完整附件、重复校验项、HTTP 降级和校验不符。下载使用独立临时文件，成功后才生成目标文件；目标已存在时保留它并报错。失败后可以重新下载到新的目标目录。Windows 只有未签名预览包时，返回 `unsigned_package_requires_consent`；用户明确选择该预览后才添加 `--allow-unsigned`。

下载使用主机网络，需能访问 GitHub API 与 Release 附件。Node 24 支持通过 `node --use-env-proxy agent-bootstrap.mjs ...` 使用用户明确配置的 `HTTPS_PROXY` / `HTTP_PROXY`；也可使用用户现有的主机 TUN。不要回显代理凭据。较旧 Node 的 fetch 不自动采用这些环境代理；连接失败时先排查主机下载网络，避免反复重建 Cage 配置。

| 平台 | 安装与验证 |
| --- | --- |
| macOS | 只读挂载 DMG，检查应用签名与 Gatekeeper 结果，按[安装指南](installation.md)放入应用程序目录。更新前保存任务并停止旧环境；保留可恢复的旧应用，安装失败时回滚应用文件。不要移除隔离属性或关闭系统校验来继续安装。 |
| Windows | 检查下载包的签名状态；在用户授权的安装上下文运行安装器。`/S` 支持静默安装；已有自定义安装目录以安装记录为准。系统安全提示、Hyper-V 启用与重启可能需要用户操作。 |
| Linux | 在用户授权的权限上下文执行 `sudo apt-get install -- /absolute/path/Cage-版本-linux-amd64.deb`，由包管理器检查依赖。公共命令名是 `claude-cage`。 |

安装、更新与卸载只处理应用文件。保留 `.claude-cage`、用户指定的数据目录、共享项目与持久虚拟机磁盘。已有账户重新出现创建界面时，首先核对 `config_path` 与原数据位置；不要创建替代账户或初始化旧数据目录。下载成功与应用已安装是两个状态；安装后验证真实 CLI 文件和 `agent capabilities`，再开始配置。

## 第一次配置的完整顺序

先明确用户希望使用哪类客机，以及哪个目录可以共享。Agent 可以探测主机条件、根据报告提出选择，不能替用户接受许可证、降低出口规则或授权未指定的主机目录。

```sh
cage agent capabilities
cage agent doctor
cage agent init
cage agent service.start
cage agent call account.status
cage agent host.inspect
```

`init` 仅创建缺失的默认配置；已有有效配置会保留，损坏配置会报错。`service.start` 只启动缺失的本机后台，复用同一构建的现有后台，拒绝替换不匹配的构建。它不启动虚拟机。`doctor` 只读取当前状态，不启动服务、不更改网络或应用修复。

账户存在且已解锁时继续下一步。否则让用户在 Cage 中创建或解锁；用户选择通过 Agent 提供密码时，可用下面的私有输入格式调用 `account.create` 或 `account.unlock`。命令行参数不接受密码。账户创建只适用于真正空白的 profile。

```json
{"username":"用户选择的名称","password":"用户提供的密码","confirm_password":"同一个密码"}
```

```sh
cage agent call account.create --input /private/path/account.json
cage agent call account.unlock --input -
```

`--input -` 读取一个完整 JSON 对象后等待 EOF。Agent 应通过子进程的标准输入传递凭据，或使用访问受限的临时文件并及时清理。不要把含密码的示例值、订阅链接或节点密钥写进共享日志、提交记录、聊天回显和 shell 历史。这个接口面向用户授权、在主机同一操作系统账户下运行的 Agent，权限与本机工作台一致；它不是给不受信任插件使用的受限远程接口。

### 导入代理并确认出口

先读 `proxy.status`。已有可用来源时复用，避免重复导入。首次导入可用用户提供的订阅链接，也可用私有文件中的 Clash/Mihomo 配置或分享链接。

```json
{"name":"我的代理","url":"https://用户自己的订阅地址","interval_hours":0,"host_download":true}
```

```sh
cage agent call proxy.import --input /private/path/subscription.json --wait 2m
cage agent call proxy.status
```

`host_download:true` 明确允许主机联网下载这份订阅，仅用于订阅获取；客机不会因此获得直连回退。已有 Cage 代理时，可省略它让订阅经过现有代理。不要自动从本机浏览器、代理软件或其他 Agent 的凭据文件提取订阅。

从返回的来源和节点列表选择用户要求的出口；不要把订阅中的第一个节点当成用户已经同意的出口。

```json
{"node_id":"proxy.status 返回的节点 ID"}
```

```sh
cage agent call proxy.select --input /private/path/selection.json --wait 2m
cage agent call proxy.status
```

链式代理使用 `proxy.route`，输入 `{"node_ids":["入口ID","最终出口ID"]}`，顺序为入口到出口。更新订阅使用 `proxy.refresh` 和已有来源 `id`；`proxy.import` 每次创建来源，不能盲目重试。成功应用代理只表示代理已准备好，客机启动后的安全检查还需单独验证。

### 准备环境并打开项目

```sh
cage agent call guest.options
cage agent call guest.match
cage agent call storage.get
cage agent call resources.get
cage agent call settings.get
```

根据实际可用性选择系统。`guest.match` 给出匹配主机版本的干净镜像方案；将返回的 `key` 作为 `clone_key`，将 `plan.host.system` 作为 `guest_system` 传给 `environment.start`。镜像下载量、磁盘、主机权限和系统授权条件见[客机系统指南](guest-systems.md)。它不复制主机账户和个人文件。已有客机应继续原安装，保留原系统与介质信息。

首次需要换存储位置时，在安装前用 `storage.set`，输入 `directory`、`previous`、`migrate`；后两项分别来自 `storage.get` 的 `directory` 和 `has_environment`。已有环境的迁移会停止任务并保留备份，应先保存工作。资源配置用 `resources.set` 的 `allocation` 对象；修改保存后再以 `{}` 调用应用资源，会重启环境。容器资源由服务器部署管理。

只发送需要修改的设置，先预检再应用；browser_home_url 单独通过 browser.preferences.set 保存，环境设置分开提交；未指定字段会保留，包括出口规则对象中未指定的 IP、国家和城市基准。`null` 不用于清空字段。

```json
{"patch":{"auto_timezone":true,"auto_language":true},"dry_run":true}
```

```sh
cage agent call settings.patch --input /private/path/settings-check.json
# 应用时把 dry_run 改为 false，再提交同一 patch。
cage agent call settings.patch --input /private/path/settings-apply.json --wait 2m
cage agent call environment.start --input /private/path/guest-choice.json --wait 30m
```

已有环境的 `environment.start` 可以不带输入。首次选择的 JSON 字段为 `guest_system`、`clone_key` 或需要的 `media`、`sha256`；只在用户明确接受对应条款时传 `editor_terms:true`。可用系统以 `guest.options` 为准。安装成功且 `guest.options.activate:true` 时调用 `guest.activate`，随后重新调用 `service.start` 并读取状态。激活会让旧后台退出；请求中断时先核对安装状态再决定下一步。

系统首次设置、macOS 权限、Windows 登录或重启需要用户完成时，报告具体需要做的事和恢复查询命令。不要用无限重试掩盖等待用户操作。安装进度在作业的 `setup_progress` 中；`prepared`、安装完成、运行和联网分别检查。

```sh
cage agent call status
cage agent call initialization.status
cage agent call network.check --wait 5m
```

以 `status.ready`、`vm_running` 以及 `safety` 中当前配置对应的网络检查与授权共同判断可用性；`proxy.status.network_open` 可辅助检查外网是否已放行。网络暂停时保持用户的暂停意图，只有用户要求恢复联网才运行 `network.check`。完成首次引导使用 `initialization.set` 的 `{"action":"complete"}`，后台会再次核对环境与网络状态。

打开项目使用 `project.open`：`{"workspace":"用户授权的绝对路径","add":true,"activate":true}`。它会让客机访问这个目录，改动会影响主机文件。返回 `approval` 表示等待系统授权；Agent 可以调用相同的 `/api/macos/project-approval` 查看、操作该窗口。Claude 的服务登录仍在 Cage 内的官方工具中完成；本接口不会导入主机 Claude 凭据。

移除已添加项目使用 `project.remove`，传入 `{"workspace":"项目列表中的路径"}`。关闭该目录的终端、运行中会话和编辑器后可解除共享，原文件与会话历史保留。默认工作区保留，移除当前项目后切回默认工作区。

## JSON 结果和异步任务

除流式 `socket` 外，每次命令的 stdout 只有一个 JSON 对象；`socket` 的 NDJSON 格式见下文。格式示例：

```json
{"protocol":1,"version":"构建版本","config_path":"绝对配置路径","status":"accepted","data":{"id":"012345abcdef","kind":"start","running":true,"message":"正在准备…"}}
```

| 字段或退出码 | 含义 |
| --- | --- |
| `status: completed`，退出 0 | 本次读取或同步操作完成；异步作业已确认成功。仍需根据数据核对环境和网络是否达到目标。 |
| `status: accepted`，退出 0 | 作业被接受，仍在运行；保存 `data.id` 并查询。 |
| `status: error`，退出 1 | 操作失败，查看稳定的 `error.code`、`message`、`next_action` 和 `retryable`。 |
| 退出 2 | 命令或输入错误，修正后再提交。 |
| 退出 3 | 需要用户解锁或完成授权。 |
| 退出 4，`wait_timeout` | 本次等待结束，后台任务可能继续；查询原任务，避免重复提交。 |

```sh
cage agent job 012345abcdef
cage agent job 012345abcdef --wait 30m
```

`--wait` 为有限等待，范围为 0 到 24 小时；不指定时立即返回。等待或客户端断开不会自动取消作业。后台只保留最近一个作业，后续作业会替换查询记录，重启也可能失去记录。`job_not_found`、`outcome_unknown` 或连接中断后，先读 `status` 和相应配置，核对副作用；不要把其他任务成功当成原任务成功。`job_canceled` 单独表示取消，不能视为完成。

命名 call 的输入必须是单个 JSON 对象，最大 12 MiB；request 支持原始二进制文件流。未知命令和字段会被拒绝。能力清单的 `input_schema` 描述允许字段；当前平台、系统状态和字段组合仍由后台验证。错误说明经过脱敏，正常业务结果保持接口原值。用户可以查看的密码、Cookie、配对码等，在同样的再次验证和权限检查通过后也可由 Agent 读取，应通过私有管道处理，避免默认记录或回显。

## 定位问题与恢复

先运行 `doctor`，核对配置路径、账户和后台，再用 `status`、`proxy.status`、`environment.versions` 找到失败阶段。修复后重复对应查询，只有真实状态达到目标才报告恢复成功。

| 稳定错误码或可见状态 | Agent 的下一步 |
| --- | --- |
| `config_missing` | 核对用户数据位置；真正首次使用才运行 `init`。 |
| `config_invalid`、`private_file_permissions` | 保留配置与磁盘，导出诊断，检查原文件和权限；不要覆盖整个 profile。 |
| `service_unavailable` | 核对实际 CLI 与路径，执行 `service.start`；失败时导出离线诊断。 |
| `controller_mismatch` | 使用与运行后台同一应用内的 CLI；更新需要保存并停止已有任务，Agent 不会自动接管。 |
| `config_path_mismatch` | 同一目录的后台使用了另一个配置文件名；改用原后台的绝对配置路径，保留所有现有文件。 |
| `account_locked` | 用户在 Cage 解锁，或通过私有输入调用 `account.unlock`。 |
| `request_rejected`、`invalid_input` | 读取本构建能力与错误说明，修正字段或节点/来源 ID；不要重复原请求。 |
| `state_conflict`、`service_busy` | 读取当前任务和系统状态；有在途任务时等待原任务完成。 |
| `unsupported_operation` | 检查当前平台和客机的能力，使用对应版本指南。 |
| `job_failed` | 保留 `data.error` 与脱敏的任务日志，检查当前配置、来源可用性、空间与主机条件。 |
| `outcome_unknown`、`job_not_found` | 读取实际副作用；代理导入、安装和迁移都不能盲目重新提交。 |
| 外网未放行或检查失败 | 检查所选代理、链路、出口规则、时间和位置；保持失败时关闭外网。用户授权后可切换节点或重检，不自动改为直连或降低规则。 |
| `user_action_required`、项目 `approval`、首次系统设置未完成 | 使用同一原生桥或客机授权窗口接口继续；系统要求生物认证、管理员确认时，完成验证后查询原状态。 |

```sh
cage agent diagnostics --output /absolute/path/new-cage-diagnostics.zip
```

诊断导出在后台不可用时也能收集主机日志；可连接时尝试补充客机信息。使用新的输出文件名，已有文件会保留。诊断包经过现有脱敏流程，分享前仍由用户决定。不要把原始配置、订阅地址、密钥、账户文件或虚拟机磁盘作为诊断附件自动上传。

更新组件用 `environment.update`，更新工具用 `environment.tools`，均可通过作业接口跟踪。`environment.stop` 会终止环境内活动任务，保留磁盘和登录资料。应用升级、跨版本回滚及卸载仍遵循[维护与数据指南](maintenance.md)，以安装器和系统包管理器为入口。

`status.can_restore:true` 时可用 `settings.restore` 恢复已保存的上一份设置并重启环境；这会中断现有任务，先保存工作。配置文件本身已经损坏、路径选错或账户无法读取时，保留原文件并先核对备份，不能用恢复命令代替数据定位。

## 交付给用户的结果

完成后用几句话说明：实际安装的版本和路径、使用的配置与客机、代理是否应用、环境是否运行、网络检查是否通过、项目是否连接。列出需要用户完成的授权和登录，以及尚未验证的范围。仅完成下载、模拟请求或单平台编译时，分别报告，不写成完整安装或三平台实测成功。

## 用户与 Agent 的操作空间

命名操作是常用配置的快捷方式。通用 `request` 访问与界面相同的 HTTP API，`socket` 连接同一 WebSocket，原生操作交给实际桌面窗口的现有桥接执行。两种入口使用同一账户权限、密码重认证、项目授权、网络门槛和平台能力。用户授权 Agent 完成配置后，Cage 不因为调用者是 Agent 增加一轮产品确认。系统自身要求的 Touch ID、管理员认证等仍然有效。

### 先选择目标

| 范围 | 目标 |
| --- | --- |
| `local`，完整应用默认 | 明确 config_path 对应的本机工作负载，固定本机，不跟随 SSH 切换。 |
| `desktop` | 完整应用的桌面连接配置、SSH 主机、本机信任和原生窗口队列。需要桌面连接服务运行。 |
| `selected` | 完整应用桌面当前选择的环境，必须带 `--connection`，值取 client.context.connection_id。切换后旧代次被拒绝。 |
| `client`，轻量客户端默认 | 轻量客户端的连接服务；业务请求必须带当前 `--connection`，连接管理请求可不带。 |

完整应用的 `--config` 仍指原主配置；desktop 范围会定位对应的 desktop-entry 服务。轻量客户端使用自己安装包中的 CLI，默认配置为 `~/.claude-cage/client/config.json`。需要桌面窗口时，Agent 可以用正常系统启动方式打开 Cage，再继续接口操作；没有要求用户必须亲手打开窗口。

```sh
cage agent call client.context --scope desktop
cage agent routes --scope desktop
cage agent call settings.get --scope selected --connection 当前连接代次
```

`capabilities` 离线返回命名操作与实际输入类型。`routes` 在线读取当前监听器真正注册的路由，新增界面 API 会自动进入清单；平台与环境状态仍决定某路由当前是否可用。带 `{id}` / `{action}` 的模式需要替换为实际值。

### 配置能力对照

| 用户可操作内容 | Agent 的相同入口 |
| --- | --- |
| 名称、头像、密码、锁定、退出 | account.profile：username/avatar；account.password：old_password/password/confirm_password；account.lock/logout/suspend。 |
| 本机信任、记住登录、设置密码保护 | desktop 范围 client.trusted-login：action 为 create/resume/enable/disable/protect，按同一规则提供 password、connection_id。 |
| Touch ID 与快捷登录 | native.submit 提交 accountDevice，operation 为 status/enable/disable/unlock，kind 为 automatic/biometric；设备密钥由原生安全存储管理。Windows/Linux 不支持的操作与用户界面返回相同限制。 |
| 首次启动的 Git 保护 | host.git 读取，host.git.enable 输入 confirm:true 启用；会修改主机全局 Git 保护配置，与界面动作相同。 |
| 外观、字体、配色、自定义主题 | appearance.get/set 读写完整对象，appearance.codex 读取本机 Codex 外观导入；读取后保留未修改字段。 |
| 出口位置检测、测速、连接和日志 | settings.probe 输入完整设置草稿；proxy.delay 输入 node_id；proxy.connections/logs。dry_run 仅静态校验。 |
| 尚未开始安装时重选系统 | guest.reselect 输入 guest.options.pending.id，仍由原安装状态约束。 |
| Claude 账号、额度、分组、启停、调度与排序 | claude-accounts.get/change，动作与字段类型由 capabilities 给出；业务 action 与界面相同。 |
| 密码保险箱 | vault.get 列元数据；vault.change 的 action 为 save/delete/unblock/reveal；reveal 仍需 account_password，再次验证后返回真实密码；vault.browser 提供页面填充和保存。 |
| 手机配对 | mobile.get/pair/remove；自建中继输入 admin/relay，配对结果是私密邀请码。 |
| Tailscale 与 SSH 公钥 | tailscale.get，以及 install/connect/login/disconnect/logout/add-key/remove-key；公钥字段为 key。 |
| 浏览器首页 | browser.preferences.get/set；set 立即更新浏览器，失败保留旧值，长度限制与界面一致。settings.patch 单独修改首页时复用同一保存流程。 |
| Cookie、权限、扩展、收藏 | browser.cookies/settings/extensions；favorites.get/change。Cookie 的 mode、scope、id、cookieKey 与界面相同。 |
| 客机首次设置、登录与授权窗口 | request 访问 /api/macos/first-setup、/api/macos/login、当前构建提供的 /api/macos/maintenance、/api/macos/project-approval；会话、截图和键鼠动作与界面一致。 |
| SSH 主机与部署、指纹核对、连接与切换 | desktop 范围 client.hosts、client.hosts.create、client.candidate/commit/cancel/disconnect/local-stop；更新/删除主机使用 PUT/DELETE /api/client/hosts/主机ID。保留同一连接代次、候选、指纹和本机任务去留规则。 |
| 已归档会话、名称、固定、账户切换 | request 访问 /api/cli/sessions 和 /api/cli/sessions/会话ID/{metadata,account,archive,restore,purge,open}。 |
| 文件选择、主题导入导出、App 更新 | native.submit 调现有 chooseFolder、chooseGuestMedia、importTheme、exportTheme、appUpdate 等原生 action。系统选择器和签名检查照常执行。 |

### 通用请求与文件

```sh
cage agent request GET /api/settings
cage agent request POST /api/browser/extensions/upload \
  --input /private/path/extension.zip --content-type application/octet-stream
cage agent request GET /api/diagnostics/export --output /absolute/path/new-report.zip
cage agent request DELETE /api/client/hosts/主机ID --scope desktop
```

JSON 请求通过私有文件或 `--input -` 传递；二进制请求使用文件流和正确的 Content-Type。输入大小由同一生产处理器约束。输出给出 http_status、content_type、必要的 recovery，以及 body 或保存文件的 path，合法的 202 会保留。输出文件排他创建，已有文件保留。同步请求期限为两分钟，长安装通过异步任务跟踪。

request 返回的是实际业务响应；Agent 仍需检查错误字段、任务状态和可见副作用。接口清单中的集合路径可带相同查询参数。新功能没有命名快捷方式时，直接用 request；这种差异不限制用户或 Agent 的操作权限。

WebSocket 用 `cage agent socket /api/terminals/终端ID/stream`。输入输出为 NDJSON，输入每行是 `{"type":"text","data":"原协议JSON字符串"}` 或 `{"type":"binary","data":"base64"}`。输出先发送 `{"protocol":1,"status":"connected"}`，随后为相同的帧对象，结束时输出标准命令结果。握手使用同一账户、Origin 和连接代次，可承载终端输入、尺寸调整和画面等已有消息。Ctrl+C 结束本地查看，断开不停止原终端；会话凭据由 CLI 持有。

### 原生窗口与客户端偏好

先从 client.context 取得实际连接代次，提交同一窗口的原生 action：

```json
{"connection_id":"当前代次","request":{"action":"accountDevice","operation":"status"}}
```

```sh
cage agent call native.submit --scope desktop --input /private/path/native.json
cage agent request GET /api/agent/native/返回的ID --scope desktop
```

原生队列属于当前窗口所在的监听器。认领与回执需要原生启动桥换取的窗口证明，普通网页会话无法抢占；证明只留在窗口闭包，不返回给 Agent。窗口只认领一次；pending/running 需要继续查询原 ID，completed 表示收到真实回执，failed 返回原生失败。连接切换、窗口关闭或丢失回执后不自动重放动作；两分钟内未被窗口认领的请求失败。记录过期或被后续记录替换后，先核对副作用。只有依赖原生窗口的操作需要打开窗口，普通配置可直接调用后台。

客户端的布局、音量与缩放使用 WebView 的原有存储。提交 `request.action:"clientPreferences"`，operation 为 get/set；set 的 values 使用下表原始字符串，null 恢复默认。写入成功回执后刷新当前窗口，使实际界面应用；远端偏好按同一实例后缀隔离。

| 逻辑键 | 原始字符串编码 |
| --- | --- |
| cage-sidebar-collapsed | `true` 或 `false` |
| cage-browser-tab-layout | JSON 字符串，例如 `"vertical"`，在外层 JSON 中需要转义引号 |
| cage-keyboard | `mac` / `native`，仅远端客户端；本机使用 settings.keyboard |
| cage.audio | JSON 对象，例如 `{"muted":false,"volume":0.5}` |
| cage-desktop-zoom | 0.75、0.9、1、1.1、1.25、1.5 |
| cage-active | 当前标签 ID |
| cage-tab-layout | get 返回的按项目保存名称、固定和顺序的对象 JSON |
| cage-recent-environment | local 或主机 ID |

外观走 appearance 持久接口，WebView 中的外观只是缓存；保存后可以通过原生 reconnect 刷新。原生动作继续交给三平台已有接收者，平台不支持时返回同样的不可用结果。已授权 Agent 可以操作产品内的授权窗口；系统要求的生物认证或管理员验证仍由系统执行。

原生设备凭据和启动票据属于内部运输。通用 request 不直接导出 device-enroll/device-unlock 等包装密钥；这些操作通过 native.submit 的 accountDevice 调用原有安全存储与生物认证，保留用户的同一操作能力。
