# 密码保险箱

密码保险箱为 Cage 浏览器保存网站账号密码。用户提交登录表单后，在 Cage 的浏览器工具区确认保存；再次访问同一网站时，展开“密码”，选择账号填入表单，再自行提交登录。保存提示不代表网站已验证密码，用户应先确认登录成功。相同网站和用户名对应一条记录，密码变化时提示更新，同一网站可以保存多个账号。

## 使用与锁定

设置中的“密码保险箱”提供搜索、手动添加、编辑、删除及恢复网站保存提示。虚拟机未启动时仍可管理密码。编辑已有记录时，密码留空表示保留原密码；查看和复制明文均需再次输入 Cage 账户密码。明文显示在 30 秒后、窗口失焦或页面隐藏时关闭。复制使用现有三平台剪贴板桥，复制后的内容由系统剪贴板管理。

保险箱与 Cage 共用解锁状态，包括已启用的记住登录和指纹解锁。手动锁定后，主机拒绝密码库接口，并取消已授权的在途请求。退出账户和控制服务关闭沿用账户锁定流程。每次解锁产生新的浏览器授权标识，上一轮的候选记录与填充请求不能跨解锁继续使用。

浏览器的账号列表在页面内展开并占据布局空间。键盘 Tab 可以移动到账号，Enter 填充，Escape 收起列表并把焦点移回触发按钮。保存提示支持保存、暂不保存和此网站不再提示；拒绝提示的站点可以在设置中恢复。

## 数据归属与保护

主机控制服务拥有密码库，文件为当前配置目录中的 `password-vault.json`，默认位于 `~/.claude-cage`。所有记录及拒绝提示的网站整体使用 AES-256-GCM 加密，随机 nonce，每次保存原子替换文件。使用账户内部密钥通过带用途标识的 HMAC-SHA256 派生保险箱密钥；用户修改 Cage 密码时仍包装同一个账户内部密钥，已有密码库无需重新加密。文件访问权限沿用主机私有文件实现，Windows 使用相应 ACL。

主机按请求解密，事务与账户锁定共用互斥锁，完成后不保留密码库缓存。列表只返回网站、用户名、记录标识与更新时间。查看密码时再次验证不会改变账户解锁周期，也不会重新启用自动登录。解密、格式或权限检查失败时拒绝修改原文件。

密码库文件与 `account.json` 必须一起备份，并保留可用的 Cage 账户密码；单独复制密码库不能恢复账号。安装、更新和卸载保留配置目录，本功能不迁移或清理个人数据。密码库没有云同步、在线找回或独立主密码。

客机浏览器只短期持有用户提交的待保存密码以及本次选择填入的密码。主机不会将密码库文件、完整账号列表或保险箱派生密钥发送给客机。账户内部密钥的既有 Desktop 密钥环用途见[工作台账户设计](workbench.md#本地账户与锁定)。保险箱依赖现有主机与客机的访问边界；它不防止已取得主机账户权限的程序读取运行中数据，也不阻止当前网页读取已填入它的密码。

## 浏览器接入

macOS、Windows、Linux 使用同一份 Go 接口、React 界面及客机 Chromium 模块。原生窗口继续使用已有账户和剪贴板能力，没有新增平台专属密钥库。客机设置 Chromium 的 `PasswordManagerEnabled=false` 策略，关闭原生密码管理器的新增保存；按 Chromium 策略语义，原有密码记录保留且可能继续填充，不自动迁移或删除。客机模块进入现有环境组件指纹，旧环境需要完成组件更新后使用网页保存与填充。

网页表单识别和填充在 Chromium 独立脚本环境中运行，通过私有 CDP 管道连接浏览器控制器。网页主脚本无法直接调用保存绑定。控制器只接受所跟踪顶层框架的绑定事件，且只从活动标签接收可信提交、登录按钮点击或输入框中的 Enter。表单提交前读取本次值，以支持提交后立即跳转的网站。浏览器不会判断服务端登录是否成功。

仅支持 HTTPS 顶层页面。匹配使用完整 origin，包含协议、主机与非默认端口，不跨子域自动共享。填充绑定到所选账号、活动标签和当前文档随机标识；在实际写入输入框前再次检查 origin 与文档标识。导航后旧请求失效，不自动提交。常见的先填邮箱、再填密码页面可以在同 origin 内短期关联用户名；HTTP、iframe、封闭 Shadow DOM、验证码和 Passkey 不在自动识别范围。Claude 账号面板的临时授权上下文沿用其独立生命周期，不参与密码捕获。

主机在已解锁的工作台中每两秒请求浏览器状态，为捕获授予 6.5 秒有效期。断连、关闭工作台或停止刷新后授权到期，客机清除候选；显式锁定同时尝试立即撤销。每个标签最多一条候选，总量最多八条，最长保留两分钟，关闭标签也清除。普通标签列表、事件流和诊断日志不携带密码。密码候选只通过专用认证接口进入主机，给前端的保存提示已移除密码。

## 接口与实现入口

| 接口 | 行为 |
|---|---|
| `GET /api/vault` | 已解锁账户的记录摘要及拒绝提示列表 |
| `POST /api/vault` | `save`、`delete`、`unblock`；`reveal` 额外验证 `account_password` |
| `POST /api/vault/browser` | `status`、`save`、`never`、`dismiss`、`fill` |
| 客机 `POST /vault` | 现有 Bearer 认证下的短期捕获与定向填充，不向网页开放 |

主机实现见 `internal/workbench/vault_store.go`、`vault_http.go`。网页识别与文档校验见 `internal/cage/browser-vault.mjs`。界面见 `ui/src/password-vault.jsx`。原生保存策略依据为 Chromium 的 [PasswordManagerEnabled 定义](https://raw.githubusercontent.com/chromium/chromium/main/components/policy/resources/templates/policy_definitions/PasswordManager/PasswordManagerEnabled.yaml)。协议依据为 Chromium 的 [Page 独立脚本环境](https://chromedevtools.github.io/devtools-protocol/tot/Page/)与 [Runtime 绑定](https://chromedevtools.github.io/devtools-protocol/tot/Runtime/)。

## 验证入口

- `go test ./internal/workbench -run TestVault`：加密、锁定、账户改密、重开、损坏保护、网站规范化、再次验证和接口脱敏。
- `node scripts/browser-vault-smoke.mjs`：真实 Chromium 中的识别、填充、跨站与导航检查。
- `node scripts/vault-controller-smoke.mjs`：隔离 HTTPS 站点通过生产控制器和私有 CDP 管道验证捕获、填充、接口认证及导航与锁定。通过 `CAGE_TEST_BROWSER_PATH` 指定测试 Chromium，仅测试适配器忽略临时站点证书。
- `node ui/scripts/password-vault-smoke.mjs`：设置、保存提示和键盘布局交互。
- `node scripts/vault-service-smoke.mjs`：真实界面连接隔离 Go 服务，验证加密落盘、后台重启及锁定；先构建 `_output/vault-qa/cage`。

浏览器脚本使用 `CAGE_PLAYWRIGHT_MODULE` 指定现有 Playwright 安装。所有测试只使用临时配置和虚构网站，不连接用户的邮箱或个人密码库。平台编译结果与原生系统实测范围分别记录在本地验收报告中。
