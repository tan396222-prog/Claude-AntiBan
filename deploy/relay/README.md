# 自建 Cage 中继

此目录面向维护自有服务器的开发者，用于部署两个已授权设备之间的连接转发服务。电脑和客户端分别主动连接服务器，服务器将同一房间内的两条 WebSocket 连接互相转发。

部署中继后，使用主机连接助手登记手机的公钥指纹。Android/iOS 客户端通过设备通道操作 Claude Code CLI 会话。设备信任、保存连接与撤销由端点负责。[移动客户端](../../mobile/README.md) · [主机连接助手](../../cmd/cage-mobile-link/README.md)

普通用户可直接使用[官方中继](../../mobile/README.md)。本文的自建部署默认要求管理令牌。如果运营者需要允许任何电脑创建房间，可显式启用 `--public-rooms`。应在公网入口按源 IP 限制创建频率和并发连接数，同时保留中继的全局房间上限。公开创建房间时仍需在手机和电脑上互信指纹才能操作工作台。

## 连接与密钥的归属

中继管理令牌仅用于创建短期连接房间。每个房间返回独立的主机令牌、客户端令牌和过期时间；两端各使用自己的令牌连接一次。两端之间的业务数据须由 `internal/remotelink` 的 TLS 1.3 双向证书验证通道保护，端点校验预先信任的对端身份后才能发送业务数据。中继房间令牌只代表一次连接资格，不能替代设备身份或 Cage 的账户授权。

HTTPS/WSS 保护设备到中继的外层连接，端到端 TLS 保护中继内转发的业务内容。中继实现本身只转发不透明二进制帧，无法强迫任意自编客户端加密；客户端必须使用端到端通道。部署者仍能观察连接地址、时间与流量规模。

房间只存在于内存中。未在配对期限内到齐的连接会被关闭；一端断开后需要创建新房间。服务重启也会中断所有房间，恢复业务状态由设备侧负责。默认最多 128 个房间，配对期限 2 分钟，空闲期限 5 分钟；单个 WebSocket 消息不得超过 64 KiB。

本服务不接收 SSH 客户端、普通 HTTP 代理、任意浏览器 WebSocket 或 VNC 客户端。浏览器发起的带 `Origin` 请求会被拒绝，原生客户端负责建立设备通道。现有 Cage 的 SSH 远程主机连接使用另一套启动与认证流程，不能把 SSH 主机地址直接替换成此中继地址。

## 在 Linux 服务器部署

准备一台安装 Docker Engine 和 Compose 的 Linux 服务器，以及指向该服务器的域名。仅开放 TCP 80、443；Caddy 使用域名取得可信证书，转发到容器内部的中继服务。部署配置不会将中继的 8787 端口映射到主机。

在仓库根目录执行以下命令。密钥保存在仓库外，`openssl` 不会将内容输出到终端；使用当前部署用户运行后续 Compose 命令。

```sh
install -d -m 700 "$HOME/.config/cage-relay"
(
  umask 077
  set -C
  openssl rand -hex 32 > "$HOME/.config/cage-relay/admin-token"
)
chmod 600 "$HOME/.config/cage-relay/admin-token"
export RELAY_TOKEN_FILE="$HOME/.config/cage-relay/admin-token"
export RELAY_UID="$(id -u)"
export RELAY_GID="$(id -g)"
export RELAY_DOMAIN="relay.example.com"
docker compose -f deploy/relay/compose.yaml config --quiet
docker compose -f deploy/relay/compose.yaml up -d --build
```

将示例域名改为自己的域名。密钥生成命令拒绝覆盖已有文件；已有部署直接复用原文件。Compose 缺少域名、密钥路径或文件所有者标识时会直接报错。Go 构建镜像固定为与项目声明一致的 `1.26.10`，Caddy 固定为 `2.11.8-alpine`；镜像升级需修改并验证配置，不使用 `latest`。

密钥作为只读 Compose secret 挂载，中继容器以密钥文件所有者的 UID/GID 运行。Docker Compose 对文件来源 secret 使用绑定挂载，不能依赖 secret 的 `uid`、`gid` 或 `mode` 设置替代主机文件权限。[Docker secrets 文档](https://docs.docker.com/compose/how-tos/use-secrets/)

中继容器使用只读文件系统、移除所有 Linux capabilities，并且只接入内部网络。Caddy 同时连接外网与内部网络，证书保存在 `caddy_data` 卷中。配置没有启用访问日志，默认结构化日志会移除请求路径和请求头，避免记录房间地址及认证信息。[Caddy 反向代理文档](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)

使用不含密钥的健康检查确认 HTTPS 和服务监听正常：

```sh
curl --fail --silent --show-error "https://${RELAY_DOMAIN}/healthz"
docker compose -f deploy/relay/compose.yaml ps
```

健康检查返回 `ok` 仅表示中继可响应；还需要使用两个持有对端信任信息的测试端点验证加密握手、转发、断线和身份拒绝。它不能证明移动工作台已经可用。

## 本机开发与直接运行

在仓库根目录构建并启动：

```sh
go build -o /tmp/cage-relay ./cmd/cage-relay
/tmp/cage-relay --token-file "$HOME/.config/cage-relay/admin-token"
```

程序默认监听 `127.0.0.1:8787`，不主动开放公网。修改监听地址必须显式传入 `--listen`；公网部署应通过可信 TLS 入口，不能直接暴露明文 HTTP。启动日志只记录监听地址，不输出令牌、邀请内容或房间 URL。

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `--listen` | `127.0.0.1:8787` | HTTP 监听地址 |
| `--token-file` | 必填 | 中继管理令牌文件 |
| `--max-rooms` | `128` | 等待连接和已连接房间的总上限 |
| `--pair-timeout` | `2m` | 等待两端到齐的期限 |
| `--idle-timeout` | `5m` | 连接空闲期限 |

令牌需包含 32–4096 字节的无空白可见 ASCII 字符，允许末尾一个 LF 或 CRLF。macOS/Linux 拒绝组或其他用户具有权限的文件，建议使用 `0600`。Windows 文件权限采用 ACL，程序不通过 Unix 权限位推断访问范围；运行前应关闭继承，并仅允许服务账户及必要的系统管理员访问。Windows 和 macOS 可运行同一 Go 入口；容器部署示例以 Linux 服务器为目标。

## 协议接入

集成方通过 `POST /v1/rooms` 创建房间，将管理令牌放在 `Authorization: Bearer …` 请求头。返回值包含 `id`、`host_token`、`client_token`、`expires_at`，不得写入日志或公开页面。

主机与客户端分别访问 `GET /v1/rooms/{id}/host` 和 `GET /v1/rooms/{id}/client`，将各自房间令牌放在同一请求头中并升级到 WebSocket。接口不接受查询参数凭据。连接配对完成后进行端到端身份验证，再交换业务数据；房间地址本身也应视为敏感连接信息。

需要保存设备连接时，主机用 `lookup_hash` 登记设备专属查询凭据的摘要。手机把原查询凭据放在 Bearer 请求头中访问 `GET /v1/invitation`，取得主机已加入且尚未使用的房间邀请。断线后主机登记新房间，原查询凭据可以继续使用。中继重启后由在线主机重新登记。房间令牌和查询凭据分别管理，每次会话仍校验设备公钥。

中继不会转发 HTTP 路径到电脑上的管理服务。只有设备侧明确允许的应用操作才能进入 Cage。创建房间成功、WebSocket 成功和端到端握手成功分别代表不同阶段，调用方应保留对应错误反馈。

## 停止与更新

```sh
docker compose -f deploy/relay/compose.yaml down
docker compose -f deploy/relay/compose.yaml up -d --build
```

停止服务会先关闭所有中继房间，再结束 HTTP 监听；两端需要重新连接。更新保留仓库外的令牌和 Caddy 证书卷。中继不访问电脑的 Cage 个人目录或虚拟机磁盘。

轮换管理令牌后需要重启中继；重启会清除全部房间。只修改管理令牌不能撤销设备间已经保存的信任关系，设备撤销必须由主机端授权层处理。执行更新时保留令牌文件权限，避免通过放宽文件权限解决读取失败。
