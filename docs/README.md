# Cage 文档入口

首次使用 Cage，从安装与代理配置开始；已有环境遇到问题时，直接查故障恢复。公开安装包与版本验收范围见 [GitHub Releases](https://github.com/tan396222-prog/Claude-AntiBan/releases)，产品介绍见[项目主页](https://github.com/tan396222-prog/Claude-AntiBan)。

## 使用与反馈

面向日常使用的[官网使用文档](docs/README.md)提供分类目录、全文搜索和章节链接。内容源位于 [guide/](guide/index.md)，包括多账号、浏览器、手机、云端、设置与排障；下表保留安装、平台说明与维护资料的直接入口。

| 你要做什么 | 从这里开始 |
|---|---|
| 选择 macOS、Windows、Linux 安装包，配置代理并打开第一个项目 | [开始使用](getting-started.md) |
| 查看快捷键、文件保存、关闭与退出行为 | [日常使用](getting-started.md#日常使用) |
| 在 Windows 上安装、使用和更新 | [Windows 支持](windows.md) |
| 在 Linux 上安装、使用和更新 | [Linux 支持](linux.md) |
| 升级应用、更新环境工具或卸载 | [升级与卸载](getting-started.md#升级与卸载) |
| 查看首个内测版本的能力与验收范围 | [0.1.0-alpha.1 发行说明](releases/0.1.0-alpha.1.md) |
| 排查下载、启动、联网与授权问题 | [常见问题与恢复](troubleshooting.md) |
| 导出故障日志并了解自动清理 | [导出诊断包](troubleshooting.md#导出诊断包) |
| 报告隔离或凭据安全问题 | [安全报告](../SECURITY.md) |

## 开发与发布

| 你要做什么 | 从这里开始 |
|---|---|
| 从源码构建、修改和验证代码 | [参与开发](../CONTRIBUTING.md) |
| 理解工作台、浏览器与后台的职责 | [工作台设计](design/workbench.md) |
| 在同一个 Cage 中选择本机或远程，按需启用本机运行环境 | [桌面入口与资源设计](design/desktop-environments.md) |
| 将运行环境部署到无 KVM 的 Linux 云服务器，再从桌面连接 | [云端部署设计](design/cloud-deployment.md) · [部署文件](../deploy/cloud/) |
| 保存网站账号密码，了解本地加密与填充范围 | [使用密码保险箱](getting-started.md#保存网站账号密码) · [密码保险箱设计](design/password-vault.md) |
| 将多个订阅用于新建的 Claude Code 终端，了解调度、额度与 Desktop 的适用范围 | [账号面板](design/workbench.md#claude-code-账号面板) · [调研与集成取舍](research/2026-11-04-claude-account-pool.md) |
| 评估主机输入法、原位拼音与候选窗跟随 | [原生输入法研究](research/2026-11-04-native-ime.md) |
| 定位作业、终端、浏览器连接与退出流程的代码 | [控制服务职责](design/control-service.md) |
| 理解网络检查、暂停与恢复 | [网络检查设计](design/network-checks.md) |
| 理解内存回收、CPU 上限与压力后的联网恢复 | [计算资源](design/resources.md) |
| 理解日志脱敏、清理与诊断接口 | [日志与诊断](design/diagnostics.md) |
| 理解节点配置、代理核心与凭据归属 | [内置代理设计](design/managed-proxy.md) |
| 构建签名、公证的 macOS 安装包 | [发布 macOS 安装包](releasing.md) |
| 构建 Windows 安装包与执行平台验收 | [Windows 构建与验收](windows.md#维护者构建与验收) |
| 构建 Linux 系统包与执行平台验收 | [Linux 构建与验收](linux.md#维护者构建与验收) |
| 运行三平台 CI、配置签名和自动发布 | [CI/CD 指南](ci-cd.md) |
| 发布 Release、验证公开附件与部署主页 | [公开发行与主页部署](public-releases.md) |

## 上游与许可

Cage 基于 agent-vm 构建。[上游 README](agent-vm-upstream.md)、[上游版本历史](../CHANGELOG.md)、[上游发布流程](../RELEASING.md)和[上游待办](BACKLOG.md)保留其原有语境，不代表 Cage 的平台支持或产品路线。上游发布脚本仍引用版本历史，相关文件保留原路径。

依赖来源与许可分别见 [NOTICE](../NOTICE)、[第三方许可](../THIRD_PARTY_LICENSES)、[固定上游来源](../third_party/SOURCES)和[补丁说明](../patches/README.md)。
