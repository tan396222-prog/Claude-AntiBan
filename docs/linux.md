# Linux 安装、使用与验收

Cage 的 Linux 桌面版面向 Debian 12、Ubuntu 22.04/24.04 的 x86_64 电脑，使用 GTK 3 与 WebKitGTK 显示工作台。项目、终端、浏览器、编辑器和内置代理沿用其他平台的操作方式。选择本机时，默认创建持久 Debian 13 虚拟机，也可通过“自动匹配本机系统版本”下载官方镜像，创建与主机同发行版的 Linux 客机，包括 Ubuntu 22.04。所有本机项目共用所选的持久环境与 Linux 工具链。首次创建时可以直接选择 Ubuntu 22.04，无需主机也运行该版本。主机与客机可以是不同的 Linux 发行版；macOS、Windows、Debian 与 Ubuntu 的组合见[选择客机系统](guide/guest-systems.md)。

同一个桌面应用也可以从侧栏 SSH 连接已部署 Cage 的服务器。使用远程工作台只需本机图形依赖，本机模式另需 KVM 和虚拟机工具。云服务器的无 KVM 容器部署见[云端使用指南](guide/cloud.md)和[部署说明](../deploy/cloud/README.md)。

Ubuntu 22.04 的桌面包采用兼容该系统的构建基线，已在隔离容器验证真实窗口、浏览器启动、重装与卸载数据保留。完整 KVM 客机安装、物理桌面输入法和 Claude 登录尚未实测，不能把容器验证视为这些操作通过。系统匹配、续装与各路线范围见[客机系统指南](guide/guest-systems.md)。

## 安装前准备

Ubuntu 22.04 需要启用 `universe`、`jammy-updates` 与 `jammy-security`，并安装更新后的 WebKitGTK 4.1（至少 2.40）。系统初始镜像中的旧 WebKit 无法运行当前工作台。

电脑需要图形桌面、可用的 KVM 硬件虚拟化、足够的磁盘空间和自己的代理节点。默认虚拟磁盘上限为 24 GiB。云主机需要服务商开放嵌套虚拟化；没有 `/dev/kvm` 的服务器不能按普通桌面流程启动 Cage 环境。

检查当前用户可读写 `/dev/kvm`：

```sh
test -r /dev/kvm && test -w /dev/kvm
```

设备不存在时，检查 BIOS/UEFI 虚拟化和内核 KVM 模块。设备存在但权限不足时，可由管理员将用户加入 `kvm` 组，再退出桌面并重新登录：

```sh
sudo usermod -aG kvm "$USER"
```

Cage 日常以普通用户运行。不要用 `sudo cage-desktop` 启动工作台。

## 安装并开始工作

在[项目主页](https://github.com/tan396222-prog/Claude-AntiBan/releases)点击“下载应用”并选择 Linux。从对应 Release 下载 `Cage-<版本>-linux-amd64.deb` 与 `SHA256SUMS`，核对该文件的 SHA-256 后安装。只有对应 Release 提供安装包时，才执行下面的安装命令：

```sh
sudo apt install ./Cage-<版本>-linux-amd64.deb
```

安装器随附 Cage 后台、工作台和 Mihomo 代理核心。APT 安装 WebKitGTK 等图形依赖。只连接 SSH 主机时可直接打开应用。要使用本机环境，请再运行 `sudo apt install qemu-system-x86 qemu-utils ovmf openssh-client iproute2 git` 并确认 KVM 可用。首次启动本机环境时会下载 Linux 镜像与客机工具。

本机 Git 必须为 **2.38 或更新版本**。Cage 首次启动本机环境或授权共享项目时，会要求明确启用 Git 安全保护；Git 版本不足时会阻止该操作并提示升级，避免宿主机误执行项目中的裸仓库配置。可以先运行 `git --version` 检查。Ubuntu 22.04 默认仓库中的 Git 版本不足，可按 [Git 官方 Linux 安装说明](https://git-scm.com/install/linux)使用维护者提供的 Ubuntu PPA：

```sh
sudo apt install software-properties-common
sudo add-apt-repository -y ppa:git-core/ppa
sudo apt update
sudo apt install git
git --version
```

升级后返回工作台，重新检查并按提示明确授权 Git 安全保护。此要求不会阻止应用安装和打开工作台；仅打开窗口不能证明本机环境已满足启动条件。

作为手机远程主机时，Cage 通过系统的 `systemd-inhibit` 和 logind 申请保持系统运行，屏幕仍可熄灭。完整安装包依赖 systemd；服务或用户权限不满足时，“设置 → 远程连接”会显示防休眠失败原因，不会自动改动系统电源策略。

从应用菜单打开 **Cage**，也可运行 `cage-desktop`。系统包名和后台命令为 `claude-cage`，避免与发行版自带的同名 Wayland 程序冲突。导入自己的代理、选择节点，再按[开始使用](getting-started.md#2-导入并选择代理)打开项目。项目路径例如 `/home/alice/Projects/demo`，只共享默认工作区与明确选择的目录；项目中的修改和删除会同步到主机。已授权项目中的 Git 元数据可读写，客机提交直接更新主机仓库；共享范围仍限于授权目录。

Linux 使用 Ctrl 快捷键：`Ctrl+O` 打开项目，`Ctrl+T` 新建网页，`Ctrl+L` 定位地址栏，`Ctrl+W` 关闭标签，`Ctrl+Shift+Enter` 新建终端。终端用 `Ctrl+Shift+C/V` 复制粘贴，`Ctrl+C` 中断命令。

关闭窗口保留后台环境与任务，再次打开 Cage 可重新连接。在应用内停止环境后退出，才能结束后台任务。Linux 本地账户使用密码解锁；设备快速解锁暂不提供。

浏览器上传使用本机文件选择窗口，下载可通过 GTK“另存为”选择保存位置。侧栏“下载”提供打开文件、打开所在文件夹和再次另存为；文件按当前桌面关联的应用打开。操作与限制见[浏览器上传与下载](getting-started.md#浏览器上传与下载)。

## 更新与卸载

Linux 版本通过系统安装包手动更新。应用内更新入口显示更新方式，不自动替换系统文件。先保存文件、停止环境并退出 Cage，再安装新版 `.deb`。安装器发现已安装的 Cage、控制服务或代理进程仍运行时，会拒绝覆盖并提示退出。

更新和卸载保留 `~/.claude-cage` 中的配置、代理与虚拟磁盘，默认工作区 `~/.claude-cage-workspace` 及自行打开的项目目录也保留。卸载命令为：

```sh
sudo apt remove claude-cage
```

需要删除用户数据时应先自行备份。只保存在虚拟磁盘中的文件和登录状态会随虚拟磁盘删除而丢失。

## 常见问题

- 工作台无法显示：确认在普通用户的图形桌面会话中启动，且系统 WebKitGTK 安装完整。不要关闭 WebKit 沙箱来绕过桌面配置问题。
- 提示 KVM 不可用：按安装前准备检查设备和权限。普通阿里云实例未必提供嵌套虚拟化。
- 提示找不到物理网卡：内置代理需要可识别的主机网络接口。Docker 容器的 veth、VPN 隧道和桥接接口不作为物理出口；桌面主机与容器测试的网络条件不同。
- 能打开工作台但无法联网：在 Cage 内确认已选择代理节点并查看网络检查。主机浏览器能联网不能证明 Cage 代理已可用。

反馈时提供发行版、CPU 架构、Cage 版本、桌面环境以及失败步骤和错误原文。避免附带代理密码、订阅链接、账户文件和带会话信息的地址。
