# Linux 安装、使用与验收

Cage 的 Linux 桌面版面向 Debian 12、Ubuntu 24.04 的 x86_64 电脑，使用 GTK 3 与 WebKitGTK 显示工作台。项目、终端、浏览器、编辑器和内置代理沿用其他平台的操作方式。选择本机时，所有本机项目共用一个持久 Linux 虚拟机。

同一个桌面应用也可以从侧栏 SSH 连接已部署 Cage 的服务器。使用远程工作台只需本机图形依赖，本机模式另需 KVM 和虚拟机工具。云服务器的无 KVM 容器部署见[云端部署设计](design/cloud-deployment.md)和[部署说明](../deploy/cloud/README.md)。

Linux 支持处于开发验收阶段。安装包及真实测试范围以对应发行说明为准；云服务器中的窗口测试不能代表所有桌面环境、显卡和输入法均已通过验收。ARM64、RPM 发行版及 Wayland 的完整交互尚未验收。

开发包已在 Debian 12 云端隔离环境验证原生工作台、窗口重开后的界面状态、代理生命周期、系统包安装与卸载保留数据。QEMU 软件模拟验证了客体启动、共享文件保护和迁移重启。KVM 硬件加速、Ubuntu 24.04 实机以及真实 Claude 业务网络仍需验收。

## 安装前准备

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

在[项目主页](https://github.com/tan396222-prog/Claude-AntiBan/releases)点击“下载应用”并选择 Linux。从对应 Release 下载 `Cage-<版本>-linux-amd64.deb` 与 `SHA256SUMS`，核对该文件的 SHA-256 后安装。尚无公开安装包时，平台入口会指向本页，可按下文[维护者构建与验收](#维护者构建与验收)构建：

```sh
sudo apt install ./Cage-<版本>-linux-amd64.deb
```

安装器随附 Cage 后台、工作台和 Mihomo 代理核心。APT 安装 WebKitGTK 等图形依赖。只连接 SSH 主机时可直接打开应用。要使用本机环境，请再运行 `sudo apt install qemu-system-x86 qemu-utils ovmf openssh-client iproute2 git` 并确认 KVM 可用。首次启动本机环境时会下载 Linux 镜像与客机工具。

从应用菜单打开 **Cage**，也可运行 `cage-desktop`。系统包名和后台命令为 `claude-cage`，避免与发行版自带的同名 Wayland 程序冲突。导入自己的代理、选择节点，再按[开始使用](getting-started.md#2-导入并选择代理)打开项目。项目路径例如 `/home/alice/Projects/demo`，只共享默认工作区与明确选择的目录；项目中的修改和删除会同步到主机。Git 元数据保护沿用现有约束，提交在主机 Git 中完成。

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

## 维护者构建与验收

正式构建基线为 Debian 12 x86_64；从更新发行版构建的原生程序可能需要更高版本 glibc，不能标为兼容 Debian 12。GTK/WebKitGTK 依赖来自发行版软件源（[Debian 依赖](https://packages.debian.org/bookworm/libwebkit2gtk-4.1-0)、[Ubuntu 依赖](https://packages.ubuntu.com/noble/libwebkit2gtk-4.1-0)），动态库最低版本由打包脚本自动提取。

构建需要遵循 `go.mod` 的 Go、Node.js 24、C 编译器和以下系统包：

```sh
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev libjson-glib-dev dpkg-dev curl ca-certificates
make cage-linux
bash scripts/package-linux.sh --skip-build
```

应用目录为 `_output/linux-amd64/app`；安装包、校验清单和 Mihomo 对应源码位于 `_output/releases/<版本>/linux`。`packaging/linux/dependencies.json` 固定代理核心及源码的下载地址与哈希。Cage 不随包复制发行版 QEMU 或 WebKit 库，系统包管理器负责这些组件的维护。

`make check-linux` 可在其他主机检查 Linux Go 编译与静态诊断。原生桌面、Go 测试及安装检查须在 Linux 执行；CI 使用非 root 用户、独立配置和虚拟显示。真实发行还需验证 KVM 启动、代理出口及失败阻断、中文空格路径共享、终端输入与缩放、网页和编辑器、关闭恢复、升级和卸载后的数据保留，以及常见 X11/Wayland 桌面和输入法。
