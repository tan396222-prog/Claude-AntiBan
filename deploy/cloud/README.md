# 云端运行环境的构建与运维

本目录面向部署 Cage 的维护者。云端运行控制服务和持久容器，本地客户端通过 SSH 使用工作台。容器文件系统、浏览器资料、开发工具及网络出口留在服务器；控制端口保持回环监听。运行边界见[云端部署设计](../../docs/design/cloud-deployment.md)。

这里提供开发构建材料，尚未发布云端镜像或安装包。`Dockerfile.guest` 构建 Debian 13 基础层，完整工具与 Cage 服务继续使用仓库中的共同安装脚本。基础层本身不能当作完整工作环境交付。本轮镜像与实机验证使用 Linux amd64；终端、浏览器、编辑器及 Claude Desktop 的 Chat/Code 安装路径可在无 KVM 容器内运行，依赖嵌套虚拟机的 Cowork 不在支持承诺内。Desktop 已验证安装、启动及欢迎页，未使用真实第三方账户验收聊天。

## 从 Cage 侧栏部署

用普通 SSH 用户连接下文列出的 Linux amd64 主机。首次部署使用空的配置目录，用户核验指纹并点击「部署并连接」后，桌面端传输与客户端同版本的 Linux 服务端、代理核心和安装脚本。服务器下载并校验容器引擎和固定摘要的 Debian 基础镜像，执行与本机环境共用的工具安装脚本。服务器在配置目录的 `cloud-host/` 下保存程序和私有引擎，在 systemd 用户目录注册以配置路径区分的服务。工作台就绪后即可创建或解锁服务器上的 Cage 账户。再次连接会沿用该实例，服务停止时会自动启动。部署中断后可用同一安装包重试。

桌面构建自动生成现场安装所需的材料。制作可选的私有离线包时，先准备 Linux amd64 的 Cage 后端、下文构建的完整镜像目录和两份校验过的 Docker 存档。设置 `CAGE_SERVER_BINARY`、`CAGE_GUEST_BUILD`、`CAGE_DOCKER_DOWNLOADS` 后运行 `bash deploy/cloud/pack-kit.sh`。用 `CAGE_CLOUD_KIT` 指定归档绝对路径。桌面程序按需从磁盘流式上传。

## 服务器前提

当前构建基线为 Linux x86_64、cgroup v2、systemd 用户服务及 rootless Docker 29.9.1。服务器无需 KVM。运行用户须有可用的多 UID/GID 用户命名空间、至少 65,536 个 subordinate UID/GID，以及可访问的 `/dev/fuse`、`/dev/net/tun`。`newuidmap`、`newgidmap` 可通过发行版的 setuid 或文件 capability 实现映射。

宿主需提供 OpenSSH、curl、tar、sha256sum、iproute2 与 iptables；Cage 服务端不依赖 GTK 或 WebKit。实际运行还会检查用户 cgroup 委派和父层资源上限。缺少前提时应修复服务器配置，不能通过 `--privileged`、宿主网络、宿主 PID、Docker socket 挂载或整个宿主 cgroup 的可写绑定启动工作环境。

每个用户的数据根默认为 `~/.claude-cage`。容器引擎的数据固定在 `~/.claude-cage/container-engine/data`，与版本号和可执行文件目录分开。显式使用其他 `CLAUDE_CAGE_CONFIG` 时，须同步修改两个用户服务中的绝对配置路径和引擎数据根；后续升级继续使用同一组路径。

## 准备独立的 rootless 引擎

[runtime-artifacts.json](runtime-artifacts.json) 固定官方静态发行物及其 SHA-256。Docker 主程序与 rootless extras 必须使用同一版本。以下是首次安装流程，以普通用户执行，安装在自己的配置目录，不修改已运行的系统 Docker。已有 Cage 引擎先按文末的停止与更新流程处理，不能在运行期间直接解包覆盖程序。

```sh
cage_engine_root="$HOME/.claude-cage/container-engine"
install -d -m 700 "$cage_engine_root/tools" "$cage_engine_root/downloads" \
  "$cage_engine_root/data" "$cage_engine_root/client"
curl --fail --location --proto '=https' --tlsv1.2 \
  https://download.docker.com/linux/static/stable/x86_64/docker-29.9.1.tgz \
  -o "$cage_engine_root/downloads/docker-29.9.1.tgz"
curl --fail --location --proto '=https' --tlsv1.2 \
  https://download.docker.com/linux/static/stable/x86_64/docker-rootless-extras-29.9.1.tgz \
  -o "$cage_engine_root/downloads/docker-rootless-extras-29.9.1.tgz"
printf '%s  %s\n' \
  c158ecc55adb3afb01bbd3539ed3b997d20c26dbdc418080506cc1bf5243ff57 \
  "$cage_engine_root/downloads/docker-29.9.1.tgz" \
  c87668896a3fced2e715324e9fb6a0634aba3e5d8408a471f6bc23dc476662af \
  "$cage_engine_root/downloads/docker-rootless-extras-29.9.1.tgz" | sha256sum -c -
tar -xzf "$cage_engine_root/downloads/docker-29.9.1.tgz" --strip-components=1 -C "$cage_engine_root/tools"
tar -xzf "$cage_engine_root/downloads/docker-rootless-extras-29.9.1.tgz" --strip-components=1 -C "$cage_engine_root/tools"
if [ ! -e "$cage_engine_root/daemon.json" ]; then
  (umask 077; printf '{}\n' > "$cage_engine_root/daemon.json")
fi
install -d -m 700 "$HOME/.config/systemd/user"
if [ ! -e "$HOME/.config/systemd/user/cage-container-engine.service" ]; then
  install -m 644 deploy/cloud/cage-container-engine.service "$HOME/.config/systemd/user/"
else
  printf '保留已有 cage-container-engine.service，请核对其中的路径。\n'
fi
systemctl --user daemon-reload
systemctl --user enable --now cage-container-engine.service
```

连接该引擎时显式指定私有 socket 和独立的客户端配置，避免操作系统 Docker：

```sh
export DOCKER_HOST="unix:///run/user/$(id -u)/cage-container-engine/docker.sock"
export DOCKER_CONFIG="$HOME/.claude-cage/container-engine/client"
cage_engine="$HOME/.claude-cage/container-engine/tools/docker"
"$cage_engine" info --format '{{json .SecurityOptions}} {{.CgroupDriver}} {{.CgroupVersion}}'
```

输出必须包含 `name=rootless`，cgroup 驱动为 `systemd`、版本为 `2`。用户未启用 linger 时，最后一个登录会话结束后用户服务可能停止。无人登录时常驻需要服务器管理员为该用户启用 linger；测试环境不自动修改此设置。[Docker rootless 前提与用户服务说明](https://docs.docker.com/engine/security/rootless/)

## 构建完整客体镜像

先从同一份源码构建 Linux Cage 后端：`make cage GOOS=linux GOARCH=amd64 CGO_ENABLED=0`。构建会生成工作台资产及 `_output/bin/cage`；不运行发布或版本修改流程。

通过 `Dockerfile.guest` 构建基础层，再创建独立的镜像构建容器。构建容器不挂载项目、个人主目录、代理配置或账户；安装下载使用单独的构建网络。默认 Debian 镜像已经固定 digest，APT 继续验证发行版签名。若部署网络需要镜像站，只调整构建阶段的 Debian 软件源，保留签名验证；基础层和完整镜像提交前均恢复 `deb.debian.org` 官方源，以符合运行环境的维护下载来源限制。

使用[完整镜像构建脚本](build-guest.sh)建立本次构建独占的父 slice、网络和容器。脚本检查实际 rootless 引擎和父层上限，再执行共同安装脚本；命名带唯一后缀，避免覆盖已有环境。

```sh
CAGE_SERVER_BINARY="$PWD/_output/bin/cage" \
CAGE_CONTAINER_ENGINE="$cage_engine" \
  bash deploy/cloud/build-guest.sh
```

完整镜像的安装顺序与本地 Cage 一致：

```sh
# CAGE_BUILD_CONTAINER 是本次新建的镜像构建容器，须由操作者明确指定。
_output/bin/cage container-install-script tools |
  "$cage_engine" exec --interactive --user cage --workdir /home/cage "$CAGE_BUILD_CONTAINER" bash -l
_output/bin/cage container-install-script services |
  "$cage_engine" exec --interactive --user cage --workdir /home/cage "$CAGE_BUILD_CONTAINER" zsh -l
```

第一步仅选择 Python、Node、Chromium、Claude Code 和 code-server，复用 `ToolInstallPrelude` 与 `agent-vm.setup.sh`；第二步复用 `GuestSetup`。服务安装需要容器内 systemd 正在运行，不能把第二步直接塞进普通 `Dockerfile RUN`。容器模式跳过 zram；Chromium 沙箱保持开启。基础层将容器内 systemd 的 `DefaultTasksMax` 设为 `infinity`，单个服务不再受默认百分比派生的小限额约束。运行工作环境由宿主父 slice 实施 1024 个进程／线程的硬上限；独立镜像构建仍使用 2 CPU、4 GiB 内存、512 个进程／线程的预算。初始化浏览器资料的 `StartScript` 留到工作环境首次启动，不能在基础镜像内预建浏览器密码或控制凭据。

在 rootless Docker 中，systemd 的启动入口为：

```sh
umount /sys/fs/cgroup
mount -t cgroup2 none /sys/fs/cgroup
exec /sbin/init
```

这些命令只能在已经配置为私有用户、PID、IPC、网络和 cgroup 命名空间的容器中执行。容器采用 `/run`、`/tmp` 临时文件系统，只额外提供 `/dev/fuse`、`/dev/net/tun` 以及用户命名空间内的 `SYS_ADMIN`、`NET_ADMIN`。工作环境由 Cage 创建，使用 `network=none` 和专门的宿主父 slice 限额；镜像构建容器的联网与生命周期由构建步骤单独管理。引擎将容器主机名固定为 `cage`，并添加 `cage:127.0.1.1` 别名，保证无默认网络时也能解析自身主机名。容器可管理自己的 cgroup 子层，CPU、内存和 PID 硬上限须放在它看不到的宿主父层。

提交完整镜像前，仅清理本次新建构建容器：停止 SSH 和 Cage 服务，移除生成的 SSH host keys、`cage_authorized_keys`、journal、APT 缓存和临时下载，将 `/etc/machine-id` 清空。确认其中没有账户文件、浏览器控制凭据、浏览器密码、Claude 登录态、代理凭据或用户项目，再停止并提交镜像，记录 SHA-256 image ID。不得对已有工作环境执行这一清理；已有容器的整个可写文件系统都属于持久数据。

脚本默认生成本地 `cage-guest:development` 镜像，并在 `_output/cloud-images/build.*` 保存 image ID、导出的 `cage-guest.tar`、`SHA256SUMS`、安装脚本和日志。成功后只回收当轮创建的构建容器与网络；失败时保留它们供检查。没有推送镜像、创建 Git 标签或发布版本的步骤。

离线准备了相同 Debian 基底时，可用 `CAGE_GUEST_BASE_IMAGE` 指定已经校验的本地 image ID；它必须固定为 SHA-256。需要其他 Debian 镜像站时，`CAGE_DEBIAN_MIRROR` 接受 HTTP(S) 源站地址，例如 `http://mirrors.aliyun.com`，APT 签名验证保持开启。[软件源恢复脚本](restore-apt-sources.sh)在提交镜像前校验并恢复 Debian 主仓库和安全仓库的官方 URI，保留发行版、组件与签名密钥配置，不改 NodeSource 等其他来源。

## 安装并连接控制服务

将同一次构建的 Cage 后端与校验过的 Linux Mihomo 放到 `~/.local/lib/cage/`，分别命名为 `cage`、`mihomo`。Mihomo 的固定下载来源和校验沿用 `scripts/fetch-linux-proxy-core.sh`。

下面的示例从 `build-guest.sh` 输出的 `Artifacts` 目录读取 `image-id`。如果在另一台机器构建，先把该目录中的 `image-id`、`cage-guest.tar`、`SHA256SUMS` 复制到服务器；示例会校验归档并加载到私有引擎。

默认使用 `~/.claude-cage/config.json`，已经显式设置的 `CLAUDE_CAGE_CONFIG` 会保留。`init` 只在配置不存在时执行，其文件创建也使用排他模式；已有配置不覆盖。`container-configure` 通过标准输入接收 JSON，已有容器配置仅接受完全相同的参数，换镜像或资源配置需要按迁移流程处理。已有 VM 数据的配置目录会被拒绝，不能用此流程覆盖本地环境。

```sh
(
  set -eu
  printf '粘贴 build-guest.sh 输出的 Artifacts 绝对目录：'
  IFS= read -r cage_build_output
  case "$cage_build_output" in /*) ;; *) echo '需要绝对目录。' >&2; exit 1 ;; esac

  cage_binary="$HOME/.local/lib/cage/cage"
  cage_engine="$HOME/.claude-cage/container-engine/tools/docker"
  cage_socket="unix:///run/user/$(id -u)/cage-container-engine/docker.sock"
  export DOCKER_HOST="$cage_socket"
  export DOCKER_CONFIG="$HOME/.claude-cage/container-engine/client"
  unset DOCKER_CONTEXT
  export CLAUDE_CAGE_CONFIG="${CLAUDE_CAGE_CONFIG:-$HOME/.claude-cage/config.json}"
  case "$CLAUDE_CAGE_CONFIG" in /*) ;; *) echo '配置路径必须是绝对路径。' >&2; exit 1 ;; esac

  (cd "$cage_build_output" && sha256sum -c SHA256SUMS)
  cage_image_id=$(cat "$cage_build_output/image-id")
  printf '%s\n' "$cage_image_id" | grep -Eq '^sha256:[a-f0-9]{64}$'
  "$cage_engine" load --input "$cage_build_output/cage-guest.tar"
  "$cage_engine" image inspect "$cage_image_id" >/dev/null
  if [ ! -e "$CLAUDE_CAGE_CONFIG" ]; then
    "$cage_binary" init
  else
    printf '保留已有配置：%s\n' "$CLAUDE_CAGE_CONFIG"
  fi

  "$cage_binary" container-configure --json <<JSON
{
  "engine": "docker",
  "executable": "$cage_engine",
  "socket": "$cage_socket",
  "image": "$cage_image_id",
  "cpus": 2,
  "memory_bytes": 4294967296
}
JSON
  "$cage_binary" setup
)
```

最后一步准备离线的持久容器，不放行业务网络。若使用了自定义配置或引擎目录，先把下列用户服务中的路径同步为同一组绝对路径，再启动控制服务。

```sh
if [ ! -e "$HOME/.config/systemd/user/cage-server.service" ]; then
  install -m 644 deploy/cloud/cage-server.service "$HOME/.config/systemd/user/"
else
  printf '保留已有 cage-server.service，请核对其中的配置路径。\n'
fi
systemctl --user daemon-reload
systemctl --user enable --now cage-server.service
```

打开同一个 Cage 应用，在「SSH」侧栏点击加号，保存服务器地址、SSH 用户和认证方式。选中主机后连接并核验服务器主机密钥。如果本机仍在运行，确认卡片会展示停止本机和保留本机的选项。工作台继续使用服务器自己的账户；远程项目选择的是服务器目录。无需将控制服务、VNC、编辑器或容器端口发布到公网。

## 停止、更新与恢复

关闭本地窗口或断开 SSH 连接保留远端环境。需要停止任务时，先在 Cage 中停止环境，再停止 `cage-server.service`；确认不再需要运行容器后才能停止 `cage-container-engine.service`。

更新前保存任务并停止环境，保留配置目录、引擎 `data` 目录和服务器项目。替换应用文件后使用相同配置路径恢复服务。镜像升级需要显式迁移已有完整文件系统；不能用删除容器、替换 data-root 或重新创建账户完成升级。禁止对保存工作环境的引擎执行 `docker system prune` 或批量 `docker rm`。

诊断优先检查用户服务状态、当前配置下的容器网络日志及实际 rootless/cgroup 状态。无登录常驻、宿主重启恢复和各客户端的原生交互分别验收，不能由单次容器启动结果代替。

## 实机验证范围

普通无 KVM VPS 上的 rootless Docker 已完成 systemd、FUSE/SSHFS、受控网络、资源父层、持久化与工作台真实操作验证。Podman 尚未实测，其他发行版和 ARM64 也不在本轮结论内。

构建脚本已检查 shell 语法，并实际运行到独立父 slice、网络、固定 Debian 基底构建、systemd 与共同工具安装入口；完整工具安装、身份清理、镜像检查和导出分别完成了真实验证。整段自动脚本未从头到尾重复执行完毕。测试用户未开启 linger，也未执行服务器重启，因此无人登录常驻与重启恢复仍需单独验收。
