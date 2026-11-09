# 公证上传超时：原因与可用路径

供 Cage 发行维护者排查本次 `v0.1.4-alpha` 发布使用。查询与验证日期：2026-11-04。

## 结论

当前主机代理数据路径的上传吞吐过低，导致 `notarytool` 的 S3 请求在分片传完前超时。换用标准 S3 客户端后，经同一代理仍慢；仅让该客户端显式直连 S3，原始 App ZIP 在 22.18 秒上传成功，Apple 随后返回 Accepted，审核日志无问题。DMG 同样在 24.51 秒上传成功并获 Accepted。

这组对照支持定位到本次代理数据路径；尚未区分代理核心、节点、节点上游和本机到节点之间的具体瓶颈，不能据此认定 `notarytool` 分片实现存在缺陷，也不能推广为所有代理都不可用。

## 对照证据

所有实验保留原始安装包与签名，使用相同主机。未修改 Cage 环境出口规则。

| 对照 | 上传方式 | 结果 |
|---|---|---|
| GitHub，1 MiB 相同附件 | 当前主机代理 | 33.08 秒，服务端确认收到 |
| GitHub，1 MiB 相同附件 | 仅该命令直连 | 4.56 秒，服务端确认收到 |
| App ZIP，50,070,047 字节 | 官方 REST + boto3 单次 PUT，沿用代理 | 468.92 秒仍只读出约 10.35 MB；停止该对照 |
| 相同 App ZIP | 相同客户端、单次 PUT，仅 S3 `Config(proxies={})` | 22.18 秒，S3 HTTP 200，Content-MD5 校验通过，ETag 与 MD5 一致 |
| 签名 DMG，55,297,096 字节 | 官方 REST + 显式直连单次 PUT | 24.51 秒，S3 确认上传，Apple Accepted |

App ZIP 的 SHA-256：`df21bcac8abf9f6027fbaa7bb33723482d8e0a675a6e8652734144c884a7dfd3`。App 公证 ID：`7221e51e-8f1f-4b98-9e49-41a1609d1e09`；DMG 公证 ID：`7cfcb1a9-1791-4cc0-9b49-52b410e26256`。这些产物对应首个候选提交 `670b537`；用户随后要求纳入主分支的两项修复，最终发行产物须重新构建与公证。

本机过程证据在 `docs/test-reports/2026-11-04-release/` 与忽略目录 `_output/notary-debug/`；包含原始测量、服务端确认、公证状态和审核日志。后者不进入公开附件。

## 为什么总表现为分片超时

本机工具为 `notarytool 1.1.2 (41)`。详细日志显示，认证和创建提交返回 HTTP 200，随后工具使用 5 MB 分片，错误发生在 S3 上传阶段，历史错误中的 `completedParts` 为空。API 中的 In Progress 只表示已登记提交，不能证明文件已经完整上传。

命令帮助将 `--timeout` 定义为等待公证结果的期限，它不延长上传请求。工具二进制还包含独立配置 `nt-upload-connection-timeout`；受控实验设为 900 秒后，日志明确确认该值，但上传仍慢，因此停止实验并恢复原先不存在的配置。具体默认上传期限未核定。

标准客户端经代理能持续传出超过 5 MB，证明不存在已证实的“文件传到 5 MB 就被拒绝”的硬限制；在慢链路上，原生工具的单次请求期限更早暴露了问题。传出字节数也不能代替 S3 成功响应。

## 本次采用的替代流程

1. 使用已有本机 App Store Connect API 密钥，生成短期 JWT，仅向 Apple 认证。
2. 将待上传文件的 SHA-256 和名称提交给官方 Notary API，取得该次提交的临时 S3 凭据。
3. 以标准 boto3 单次 PUT 上传同一文件，显式限定本次 S3 客户端的网络路径，提供 Content-Length 与 Content-MD5。收到 S3 HTTP 200 后才记录上传完成。
4. 查询同一个提交 ID，等待 Accepted 并取得审核日志；不通过重复提交轮询状态。
5. 使用系统 `stapler` 附加票据，核对 Developer ID、票据、Gatekeeper 和包内版本，然后生成公开校验清单。

私钥只在本机读取；临时 AWS 凭据不落盘，不写入 GitHub，不放入公开日志。主机和 Cage 的代理开关、节点选择与出口策略保持原状。本次仅改变维护者发行上传客户端的传输路径。凭据、私有路径与签名上传链接不得加入研究正文或公开发行物。

## 一手依据

- [Apple：通过 Web 提交公证](https://developer.apple.com/documentation/notaryapi/submitting-software-for-notarization-over-the-web)：支持直接使用 REST API 与标准 S3 客户端，创建提交和上传文件是两个步骤，返回的 S3 凭据有时效。
- [Apple：Submit Software](https://developer.apple.com/documentation/notaryapi/submit-software)：提交名称与 SHA-256 对应待上传的精确文件。
- [Apple：自定义公证流程](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)：说明上传、查询、日志与默认 S3 加速端点。
- [AWS：boto3 配置](https://boto3.amazonaws.com/v1/documentation/api/latest/guide/configuration.html)：客户端 `Config.proxies` 优先于环境变量。
- [AWS：S3 分片边界](https://docs.aws.amazon.com/AmazonS3/latest/userguide/qfacts.html)：普通分片最低 5 MiB，最后一个分片例外；缩小到任意 KB 不能作为通用修复。
- 本机 `xcrun notarytool submit --help`、工具字符串、详细运行日志及实际上传回执：用于核验本机行为。隐藏配置仅用于受控诊断，不作为持久发行配置。

## 最终发行结果

用户要求将主分支后续两项修复一并纳入。最终版本来自 `11d44cabde8e3f560a364a818fa3d63bded658d3`，三平台 CI 全通过；新的 App 上传 21.98 秒、DMG 上传 18.89 秒，两者均获 Accepted，审核日志无问题。最终 App 公证 ID `3d9c5d7c-6b20-4fcc-8969-eee36711db32`，DMG 公证 ID `129453c5-04fd-446d-9bf8-17f753663d50`。

[公开 v0.1.4-alpha](https://github.com/tan396222-prog/Claude-AntiBan/releases/tag/v0.1.4-alpha) 已发布，[官网](https://github.com/tan396222-prog/Claude-AntiBan) 已更新。[最终公开验证与部署](https://github.com/tan396222-prog/Claude-AntiBan/actions/runs/41848921422) 完成三平台安装包的匿名下载、摘要校验，以及 macOS 签名、公证、票据与 Gatekeeper。Windows 使用明确授权的未签名预览包。主机代理选择和临时工具设置均已恢复。

GitHub 匿名元数据接口另发生额度耗尽；发布验证脚本现对指定版本使用公开 SHA256SUMS 与附件响应作为备用清单来源，拒绝任意路径、重复项、错误版本和缺失附件，并继续完整匿名下载验证。该维护改动为 `3ea7973`，不改变已发行应用的源码或字节。
