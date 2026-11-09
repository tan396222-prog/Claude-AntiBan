# 三平台检查与发布

Cage 的日常推送会检查 macOS、Windows 和 Linux，并保存测试安装包。公开发布需要用户明确要求后单独触发；日常推送、测试重跑和测试包下载都不会升级版本号或创建 Release。

工作流需要与对应的三平台应用源码一并合入远端后才生效。本地保存配置不会改变 GitHub 上既有工作流的行为；首次接入应先完成源码整理与三平台检查，再启用显式发布入口。

## 日常开发

向工作分支或 main 推送代码、提交 Pull Request 后，`Three-platform checks and test packages` 会运行以下流程：

1. 检查版本一致性、发布逻辑测试和工作流语法。
2. 从同一个提交分别构建 Apple Silicon macOS、Windows x64 和 Linux x64。
3. 执行对应测试与安装检查，保存可下载的测试包和验收报告。

在仓库的 [Actions 页面](https://github.com/tan396222-prog/Claude-AntiBan/actions) 打开本次运行，可在 Artifacts 中下载测试包。测试包沿用 `VERSION` 中的版本号，每次运行以提交 SHA 区分；macOS 包保留完整 App 权限，Windows 包明确标为未签名，Linux 输出 `.deb`。测试包不会进入应用更新源。

Windows 的 NSIS 文件匹配按打包工具所在平台使用路径分隔符，Windows runner 和其他主机上的交叉打包共用一份安装脚本。Linux 原生验收在 Ubuntu 22.04 托管 runner 上的一次性 Debian 12 容器内以普通用户运行；Ubuntu 24.04 的宿主策略会额外限制容器用户的 UID 映射。容器放开 bubblewrap 所需的外层 AppArmor、seccomp 和挂载限制，并先验证命名空间可用，应用自身的 WebKit 沙箱保持开启。这些容器设置不进入发行包，也不改变用户主机的安全配置。

Windows 配套的 QEMU 源码归档使用固定提交、换行设置和 tar 权限，生成后核对固定摘要再写入缓存，避免开发者或 runner 的 Git 默认设置改变归档内容。该校验在安装器压缩前执行，摘要不符时立即停止。

官网文件的普通推送只做检查和构建。官网部署随显式发布流程完成，也可以由维护者明确运行网站部署工作流。

## 明确发版后的自动流程

用户要求发版后，维护者在 main 上运行 `Publish Cage after an explicit release request`。也可以在收到用户的发版指令后执行：

```sh
gh workflow run release.yml --repo tan396222-prog/Claude-AntiBan --ref main -f bump=prerelease
```

`prerelease` 用于继续当前 Alpha/Beta 序列；`patch` 发布修订版本（已有预发行时转为对应正式版）；`minor` 和 `major` 分别提升次版本、主版本。流程从源码版本、已存在的规范版本标签和 Release 中取最高版本作为起点，避免复用已占用的版本，包括草稿。

例如最高已用版本为 `0.2.0-alpha.1`，选择 `prerelease` 会生成 `0.2.0-alpha.2`，选择 `patch` 会生成 `0.2.0`。普通推送不会执行这些计算和修改。

发布流程依次执行：

1. 检查签名、公证与对应源码材料是否配置齐全。缺少任何必需材料就停止，尚不创建版本提交。
2. 在 `codex/release/<运行编号>` 候选分支同步 `VERSION` 与 UI、官网的 package/lock 版本，只修改五个版本文件。
3. 三个平台构建同一个候选提交，完成各自的检查。macOS 执行 Developer ID 签名与 Apple 公证，Windows 执行可信 Authenticode 签名，Linux 生成系统安装包。
4. 核对三端产物版本和源码 SHA，一起校验安装包、随附组件对应源码及统一 `SHA256SUMS`。
5. 确认 main 没有被其他提交推进，原子快进 main 并创建版本标签。创建草稿 Release，上传并核对全部附件，最后公开发布。
6. 重新验证本次公开版本，并部署指向该版本的官网。

任一平台失败时不会公开 Release。构建过程中 main 出现新提交，也会停止发布；流程不强推、不覆盖别人的提交。候选分支保留本次版本与提交，便于失败后继续。

发布使用普通版本与预发行通道的既有规则。硬件虚拟化、真实桌面操作和 Claude 账户使用仍属于实机验收，不能由托管 CI 编译成功替代；发行说明需要如实说明范围。

## 首次配置发布材料

在仓库 Settings → Environments 中使用 `cage-release` 环境，仅允许 main 部署。日常构建不进入这个环境，也不接触签名材料。配置操作不会自行触发发版。

以下内容放入该环境的 Secrets，不能提交到源码或粘贴到公开日志：

| Secret | 用途 |
|---|---|
| `CAGE_MACOS_P12_BASE64` | 含 Developer ID Application 私钥的 P12 文件，以 Base64 保存 |
| `CAGE_MACOS_P12_PASSWORD` | P12 导出密码 |
| `CAGE_MACOS_SIGNING_IDENTITY` | 完整的 Developer ID Application 身份名称 |
| `CAGE_MACOS_NOTARY_KEY_BASE64` | App Store Connect 团队 API 私钥，以 Base64 保存 |
| `CAGE_MACOS_NOTARY_KEY_ID` | API Key ID |
| `CAGE_MACOS_NOTARY_ISSUER_ID` | API Issuer ID |
| `CAGE_MACOS_PROVISIONING_PROFILE_BASE64` | 可选；原生能力需要时提供对应 Provisioning Profile |
| `CAGE_ESIGNER_USERNAME` | 使用 eSigner 时，SSL.com 登录用户名 |
| `CAGE_ESIGNER_PASSWORD` | 使用 eSigner 时，SSL.com 登录密码 |
| `CAGE_ESIGNER_CREDENTIAL_ID` | 使用 eSigner 时，已启用的签名凭据 ID |
| `CAGE_ESIGNER_TOTP_SECRET` | 使用 eSigner 无人值守签名时，验证器的 TOTP 密钥；不是每 30 秒变化的六位验证码 |
| `CAGE_WINDOWS_PFX_BASE64` | 仅证书存储方案需要：含可信代码签名私钥的 PFX 文件，以 Base64 保存 |
| `CAGE_WINDOWS_PFX_PASSWORD` | 仅证书存储方案需要：PFX 密码 |

Windows 签名方式和对应源码地址放入同一环境的 Variables：

| Variable | 用途 |
|---|---|
| `CAGE_WINDOWS_SIGNING_PROVIDER` | 云签名填 `esigner`；留空或填 `certificate-store` 使用证书存储方案 |
| `CAGE_ESIGNER_CERTIFICATE_SERIAL` | 使用 eSigner 时，预期发布者证书的十六进制序列号，不含空格 |
| `CAGE_WINDOWS_QEMU_SOURCE_URL` | 经核对的完整 QEMU 对应源码归档公开 HTTPS 地址，不含认证信息或临时签名参数，包含随附库与固件源码 |
| `CAGE_WINDOWS_QEMU_SOURCE_SHA256` | 该归档的 SHA-256 |

Windows 的普通开发包不需要证书。正式发布缺少可信证书时停止，不能用自签名证书或未签名包代替。eSigner 使用云端私钥，不要求 PFX；构建逐个签名应用、后端、Mihomo、卸载器和安装器，再由 Windows 验证信任链、时间戳及预期证书。任何一步失败就停止。首次签名的本机操作见 [Windows 维护者说明](windows.md#维护者构建与验收)。

macOS 使用临时钥匙串，Windows 证书存储方案使用临时证书导入。eSigner 使用固定摘要的官方 CodeSignTool 和 Java 21，通过临时参数文件传递密码，进程命令行只包含该文件路径；运行结束清理参数文件及工具日志。保留服务端恶意软件检查设置。发布产物只包含安装包、对应源码和摘要；中间公证凭据、私钥与配置文件不进入附件。

签名文件通常体积较小，按 GitHub Secret 的限制保存；首次使用前应在相应平台确认凭据可用。是否把账号密码和 TOTP 密钥交给 GitHub 必须由证书持有人决定；只做本机手动签名不需要保存这些 Secrets。材料存在不代表签名、公证或安装一定成功，正式构建仍会验证。

## 失败后继续

从原来的 Actions 运行选择重跑失败任务。相同运行编号复用同一个候选版本，不再次升级版本号。已经上传到本次私有草稿且摘要匹配的文件会复用；不匹配的本次草稿附件可重新上传。公开 Release 的附件不会被自动替换。

如果发布已经完成而官网部署失败，可以重跑网站任务；发布步骤重跑会确认公开附件仍与验证结果一致。不要新建另一轮发布来重试同一个版本，也不要手工编辑仍在执行中的候选分支或草稿附件。

若 main 已经有新提交，需要重新发起一次明确的发布请求，让新提交参与同一轮验证。Actions 测试产物默认保留 14 天；产物过期后需要重新构建对应候选提交。

## 工作流与验证边界

- `.github/workflows/ci.yml`：日常只读检查与测试包入口。
- `.github/workflows/release.yml`：显式发布入口及三端汇总。
- `macos-build.yml`、`windows-build.yml`、`linux-build.yml`：日常与发布共用的构建、检查契约。
- `.github/workflows/www.yml`：官网检查与显式部署，发布时使用明确的版本标签。
- `scripts/release-version.mjs`：版本选择、五文件同步内容与产物来源记录。
- `scripts/release-control.mjs`：候选提交、完整附件校验和发布重试。

GitHub 允许通过 `workflow_call` 复用构建任务；环境凭据在实际执行的任务上加载。[GitHub 文档](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows) 使用 `GITHUB_TOKEN` 创建的 push/Release 事件通常不会再触发其他工作流，因此发布入口直接调用网站工作流，不依赖隐式事件串联。[触发规则](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow)

根目录 `release.sh` 保留的是上游 agent-vm 的发行流程，不用于 Cage 三平台自动发布。
