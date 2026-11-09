---
title: 安装与首次启动
description: 安装 macOS、Windows 或 Linux 客户端，选择数据盘，完成第一次环境启动。
group: 开始使用
order: 1
keywords: 下载 安装 Apple Silicon WHPX KVM 账户 密码 镜像 继续安装
---
## 选择安装包

从[官网下载区](https://github.com/tan396222-prog/Claude-AntiBan/releases)选择平台。公开附件、签名和校验文件以 [GitHub Releases](https://github.com/tan396222-prog/Claude-AntiBan/releases)为准。源码压缩包用于开发，不能直接安装。

| 平台 | 要求与安装方式 |
| --- | --- |
| macOS | Apple Silicon、macOS 14+。打开 `.dmg`，将 Cage 拖入“应用程序”，再从那里启动。Intel Mac 暂不支持。 |
| Windows | Windows 10 22H2 / 11、x64。启用“Windows 虚拟机监控程序平台”并按系统提示重启，再运行 `.exe` 安装器。未签名预览包会明确标注，使用前阅读该版的身份与验收说明。 |
| Linux | Debian 12 / Ubuntu 24.04、x86_64。需要 KVM 与当前用户的访问权限。执行 `sudo apt install ./Cage-<版本>-linux-amd64.deb` 补齐应用依赖，再从应用菜单打开。 |

Windows ARM64 和 Linux ARM64 暂不支持。平台依赖详情见 [Windows 指南](https://github.com/tan396222-prog/Claude-AntiBan/blob/main/docs/windows.md)与 [Linux 指南](https://github.com/tan396222-prog/Claude-AntiBan/blob/main/docs/linux.md)。

<figure class="guide-shot">
  <a href="../../www/public/docs/screenshots/account-setup.jpg" target="_blank" rel="noopener" aria-label="查看原图：首次创建 Cage 账户"><img src="../../www/public/docs/screenshots/account-setup.jpg" alt="首次创建 Cage 账户" width="2560" height="1764" loading="lazy" decoding="async" /></a>
  <figcaption>首次创建 Cage 账户。截图版本：0.1.6-alpha-preview（开发构建） · macOS 原生应用 · 2026-11-07。<a href="../../www/public/docs/screenshots/account-setup.jpg" target="_blank" rel="noopener">查看原图（2560 × 1764） ↗</a></figcaption>
</figure>

## 创建 Cage 账户

选择用户名和密码登录时，设置用户名和至少 8 个字符的密码。这个账户用于解锁当前 Cage，Claude 订阅需要另外在环境里登录。当前没有在线密码找回，请保存好密码。

包含本机信任登录的较新桌面构建还可选择“信任此电脑并进入”，也可选“使用用户名和密码”进入下图所示的流程。已有密码账户保持原来的登录方式；本机信任的适用范围见[受信任的本机登录](https://github.com/tan396222-prog/Claude-AntiBan/blob/main/docs/getting-started.md#受信任的本机登录)。

macOS 可在支持的启动方式中开启“记住登录”和 Touch ID；手动锁定后仍需再次验证。在“设置 → 账户”修改头像、用户名、密码及解锁选项。账户密码不会加密整个项目目录或虚拟磁盘。

若以前已有账户，现在再次出现创建页面，先停止操作并查[数据目录问题](troubleshooting.md#原账户与数据看不见了)。

## 选择环境存储位置

首次启动前可以点击“选择环境存储位置”，选择本地磁盘上的专用空文件夹，再点击“使用此位置”。Windows 安装器里的程序目录与环境数据目录分开；例如应用装在 C 盘，环境可放在 `D:\CageData`。

选择支持权限管理与稀疏文件的磁盘：Windows 可用 NTFS，Linux 可用 ext4 / XFS / Btrfs，macOS 使用已启用所有权的 APFS。不要选择磁盘根目录、网络盘、已有项目或应用安装目录。已有环境换盘请按[存储迁移](maintenance.md#迁移环境到另一块磁盘)操作。

<figure class="guide-shot">
  <a href="../../www/public/docs/screenshots/first-start.jpg" target="_blank" rel="noopener" aria-label="查看原图：首次启动的工作台与三步引导"><img src="../../www/public/docs/screenshots/first-start.jpg" alt="首次启动的工作台与三步引导" width="2560" height="1764" loading="lazy" decoding="async" /></a>
  <figcaption>首次启动的工作台与三步引导。截图版本：0.1.6-alpha-preview（开发构建） · macOS 原生应用 · 2026-11-07。<a href="../../www/public/docs/screenshots/first-start.jpg" target="_blank" rel="noopener">查看原图（2560 × 1764） ↗</a></figcaption>
</figure>

## 导入代理并启动

1. 点击“配置网络与代理”，添加 Clash/Mihomo YAML 订阅、本地配置或手动 HTTP/SOCKS5 节点。详细步骤见[网络与代理](network.md)。
2. 选择最终出口，点击“应用代理”。Cage 使用独立核心，不会自动采用本机 Clash 当前所选节点。
3. 返回工作台，点击“启动 Cage 环境”。需要启用 Git 保护时，阅读影响后点击“启用保护并继续”。
4. 在“操作详情”查看下载、安装与检查进度。出现环境就绪且关键检查通过的绿色盾牌后，再尝试联网工作。

Git 保护会修改当前主机用户的全局 `safe.bareRepository=explicit`，影响本机其他 Git 工具；普通 `.git` 项目操作不变，裸仓库需明确指定路径。只有你确认后才写入设置。

## 第一次启动需要等待什么

首次会下载约 320–325 MiB 的 Debian 基础镜像，再安装 Chromium、Node.js、Python、Claude Code 和网页编辑器等工具。Cage 会按固定摘要校验下载文件，优先尝试已配置的公共镜像，失败时回到官方来源。下载速度取决于所选代理和来源。

初始虚拟磁盘容量上限为 24 GiB，实际占用会随安装和使用增加。Claude Desktop 按需单独下载。随后启动会复用工具、文件与登录资料。

下载中断时查看错误，修复网络或磁盘空间后点“继续安装”。已保留的文件会尝试续传；摘要不符时必须重新下载。不要通过清空 `.claude-cage` 或重建账户解决安装失败。

<figure class="guide-shot">
  <a href="../../www/public/docs/screenshots/windows-native-download.png" target="_blank" rel="noopener" aria-label="查看原图：Windows 原生应用首次下载镜像的进度"><img src="../../www/public/docs/screenshots/windows-native-download.png" alt="Windows 原生应用首次下载镜像的进度" width="750" height="320" loading="lazy" decoding="async" /></a>
  <figcaption>Windows 原生应用首次下载镜像的进度。截图版本：0.1.5-alpha.2（测试构建） · Windows 11 x64 原生应用 · 2026-11-06。<a href="../../www/public/docs/screenshots/windows-native-download.png" target="_blank" rel="noopener">查看原图（750 × 320） ↗</a></figcaption>
</figure>

## 完成第一项工作

点项目旁加号打开自己的目录，用“新建 Claude 终端”完成环境内的授权。先让 Claude 读取一个文件或完成小修改，在主机检查结果后再投入正式任务。文件、终端和上传操作见[项目、终端与文件](workbench.md)。
