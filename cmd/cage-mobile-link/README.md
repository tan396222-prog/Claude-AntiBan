# 通过中继连接移动 CLI 会话

`cage-mobile-link` 把一个已信任设备连接到一台正在运行的 Cage 主机。客户端可以浏览主机授权的项目、创建 Claude Code 会话、发送消息、查看进度、回答工具审批和停止本轮执行。主机账户锁定时，这些操作返回 HTTP 423，需要在电脑上解锁。服务端继续负责项目授权、消息去重和执行结果。

使用官方中继时，按[手机使用说明](../../mobile/README.md)直接创建配对；主机助手默认读取桌面的中继设置。以下示例演示使用管理令牌的[自建中继](../../deploy/relay/README.md)。在电脑上启动包含移动 CLI 接口的 Cage，并构建主机连接助手。Android 和 iPhone 通过 [Cage 手机客户端](../../mobile/README.md)连接。若用另一台电脑的浏览器代替手机进行调试，该电脑也需构建本工具，并准备包含 `mobile.html` 和 `assets/` 的移动页面目录。

```sh
go build -o /tmp/cage-mobile-link ./cmd/cage-mobile-link
```

## 首次登记对端公钥

在电脑上执行一次初始化命令。为 `--state` 选择一个全新的专用目录，其父目录需要已存在。终端输出的 `sha256:…` 是电脑的公钥指纹，手机的公钥指纹在 App 首页。如果两端都使用命令行，则分别初始化。

```sh
/tmp/cage-mobile-link init --state /path/to/new-device-state
/tmp/cage-mobile-link fingerprint --state /path/to/new-device-state
```

在两台设备旁核对指纹，或使用已经验证身份的独立通信渠道。核对后，把对方的完整公开指纹写入自己设备的信任记录。

```sh
/tmp/cage-mobile-link trust \
  --state /path/to/new-device-state \
  --peer-pin 'sha256:对方实际的完整公开指纹'
```

每个 CLI 状态目录只登记一个对端。首次登记依赖操作者核对指纹，信任记录拒绝覆盖。中继提供的房间身份只用于此次连接。CLI 设备身份由私有文件保存，macOS/Linux 使用用户权限，Windows 使用用户 ACL。每次连接用同一设备密钥签发短期证书，公钥指纹保持稳定。

## 在手机上保存主机

主机已登记手机指纹后，在电脑运行以下命令。管理令牌、Cage 程序与目标主机配置都使用绝对路径，方便以后从不同工作目录启动。

```sh
/tmp/cage-mobile-link host \
  --state /path/to/host-device-state \
  --relay https://relay.example.com \
  --admin-token-file /private/path/relay-admin-token \
  --cage-bin /absolute/path/to/cage \
  --config /absolute/path/to/config.json \
  --connection-out /private/path/new-phone-connection.json
```

将导出的连接文件私下传到手机并导入，在手机上逐字核对电脑独立显示的指纹，再保存主机。连接文件包含中继地址、版本号、主机公开指纹和这台手机专用的查找令牌。持有令牌可向中继索取当前房间的客户端接入资格，因此应保护连接文件。进入工作台还需要手机自己的设备私钥，主机也会向手机证明自己的身份。

此主机命令在运行期间持续维护一个可加入的房间。手机断开、房间过期或中继重启后，主机以同一查找凭据登记新房间。手机选择已保存主机即可重新连接，再从主机读取原会话。发送消息、审批和停止执行各自保留独立的请求语义，连接恢复只重建通信。主机进程重新启动时可直接读取该设备的私有配置。

```sh
/tmp/cage-mobile-link host --state /path/to/host-device-state --serve-saved
```

要撤销这台手机，在电脑对其专用状态目录执行以下命令。命令移除对端指纹和已保存的查找令牌，正在运行的桥接进程会在约一秒内关闭现有连接。主机设备密钥保留。重新授权时先登记经过核对的手机指纹，再生成新的连接文件；新文件使用新的随机查找令牌。

```sh
/tmp/cage-mobile-link revoke --state /path/to/host-device-state
```

每个状态目录对应一台获授权的手机。`--connection-out` 选择新的输出文件名；同一配置的重复导出沿用现有配对。替换已保存的中继、主机配置或手机身份前需要撤销并重新登记。

## 单次连接邀请

主机需要中继的管理令牌文件、可信 Cage 程序的绝对路径，以及目标主机配置的绝对路径。在主机执行以下命令，并保持进程运行。

```sh
/tmp/cage-mobile-link host \
  --state /path/to/host-device-state \
  --relay https://relay.example.com \
  --admin-token-file /private/path/relay-admin-token \
  --cage-bin /absolute/path/to/cage \
  --config /absolute/path/to/config.json \
  --invitation-out /private/path/new-client-invitation.json
```

将新生成的邀请文件通过私有渠道传给客户端，保留仅本人可读的权限。文件包含短期房间编号、客户端的一次性接入令牌和到期时间。中继域名与主机公钥指纹分别来自设备本地配置。主机通过本机 `server-entry --json` 取得票据并就地换取会话 Cookie，这两项凭据只在主机侧使用。

## 在客户端打开页面

在客户端设备启动本机桥接服务。将 `--web-dir` 指向可信的移动页面构建目录，为 `--launch-file` 选择一个新的私有 HTML 文件名。

```sh
/tmp/cage-mobile-link client \
  --state /path/to/client-device-state \
  --relay https://relay.example.com \
  --invitation-file /private/path/client-invitation.json \
  --web-dir /absolute/path/to/mobile-build \
  --launch-file /private/path/new-cage-entry.html
```

在同一设备的浏览器中打开启动文件。入口在两分钟内可用一次，随即换成本机 HttpOnly 会话 Cookie。页面从本机回环地址加载，设备私钥、房间令牌和主机 Cookie 均由桥接进程管理。CLI 输出只提示下一步操作，避免将敏感入口打印到终端。

使用单次邀请时，断开后重新运行 `host` 创建新的短期邀请，再运行 `client`。使用已保存主机时，将 `--invitation-file` 换为 `--connection-file` 并传入保存的连接文件；中继地址和已核对的主机指纹必须与文件一致。两条命令的私有输出文件都拒绝覆盖，请使用新文件名。

## 原生 App 接入

[nativeclient](../../internal/mobilelink/nativeclient/client.go) 提供可绑定到 Swift 与 Android 的接口。平台保存 P-256 密钥，并向 Go 层提供公钥及 SHA-256 摘要签名能力。签名结果采用 X9.62 DER 编码。原生层可展示 `Fingerprint` 供主机核对，把经独立核对的主机指纹保存在设备信任记录中。

取得短期邀请后调用 `Open`，传入独立配置的中继地址、房间编号、客户端令牌、已信任的主机指纹和设备签名器。连接成功后，`StartUI` 接受应用自带资源的绝对目录，返回本机一次性启动 URL。原生 WebView 应只允许这次入口的精确源，并直接加载该 URL。原生界面也可调用 `Request`，接收 HTTP 状态、内容类型和响应体。退出该主机时调用 `Close`，释放本机页面服务、连接及运行中的读取。

保存主机时，原生层将查找令牌存入系统安全存储，同时单独保存已经核对的主机身份和中继地址。后续调用 `OpenSaved` 即可查找新房间并建立连接，参数依次为中继地址、查找令牌、已核对的主机指纹和设备签名器。该接口仅在读取目录时对短暂的房间交接等待进行有限重试。首次授权仍需独立核对指纹。手机操作见[移动客户端使用说明](../../mobile/README.md)。

## 验证真实工作台连接

下面的测试会新建临时 Cage 配置、本机账户和双方设备身份。它通过真实中继、端到端 TLS、主机票据交换与移动 API 验证项目读取、会话创建、按版本恢复、锁定和越权拒绝，还会验证保存主机后的房间重建、中继重启、主机桥重启以及撤销设备。测试不启动虚拟机或调用模型。

```sh
go build -o /tmp/cage-mobile-e2e-backend ./cmd/cage
CAGE_MOBILE_E2E_BINARY=/tmp/cage-mobile-e2e-backend \
  go test -race -run TestIsolatedWorkbench ./cmd/cage-mobile-link
```

如需验证人工传递邀请的等待时间，同时设置 `CAGE_MOBILE_E2E_DELAY_JOIN=1`。测试让客户端延后 16 秒进入房间，覆盖了用户加入用时超过单次 TLS 握手期限的场景。
