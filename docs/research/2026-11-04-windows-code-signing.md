# Cage Windows 代码签名选择

本文供 Cage 维护者选择签名服务、估算费用并定位接入工作。核查日期为 2026-11-04；源码依据当前工作区，HEAD 为 `aebefa5`，包含其他尚未提交的修改。本文只记录调研结果，未购买证书、提交申请、改变签名实现或发布应用。

## 阅读版：安装者会看到什么

公开可信代码签名使 Windows 能验证发布者身份以及文件是否被修改。签名后的新软件仍可能出现 SmartScreen 提示，需要逐步积累信誉；微软明确说明 EV 证书也不再自动获得跳过提示的待遇。自签证书适用于受控测试环境，不能直接解决公众下载时的信任问题。[微软签名选项](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options)、[SmartScreen 说明](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation)

申请主体决定安装界面显示的发布者：个人证书显示经过验证的个人姓名，组织证书显示组织名称。SSL.com 提供无需注册公司的 IV 个人代码签名证书；具体地区的身份材料能否受理，须由签发方确认。[SSL.com IV 产品](https://www.ssl.com/products/software-integrity/code-signing/iv/)

### 可选择的服务

| 方案 | 申请条件与费用 | Cage 的选择条件 |
|---|---|---|
| 微软 Artifact Signing（原 Trusted Signing） | 个人目前限美国、加拿大；组织支持地区包含美国、加拿大、欧盟、英国、澳大利亚、新西兰、日本、韩国、新加坡、瑞士、挪威、以色列，未列中国大陆。基础套餐 $9.99/月，含 5,000 次签名 | 有符合条件的主体时评估；更换 Azure 部署区域不能代替身份资格 |
| SSL.com IV + eSigner | IV 单年证书标价 $129；云签名最低 $20/月，含 20 次，或年付 $180 含 240 次。证书费另计 | 个人希望使用自己的姓名、通过 CI 签名时的候选；需先确认大陆身份受理、最终报价及续签规则 |
| SignPath Foundation 免费开源签名 | 要求 OSI 开源许可证、持续维护、已有发布、满足签名政策、构建来源和团队角色要求；证书签发给 SignPath Foundation | Cage 的 MIT 许可证满足其中一项；完整依赖、随附运行时和构建流程仍须审核，不能据此认定已符合全部条件 |

表中信息分别来自[微软资格要求](https://learn.microsoft.com/en-us/azure/artifact-signing/quickstart)、[微软价格](https://azure.microsoft.com/en-in/products/artifact-signing)、[SSL.com 证书价格](https://www.ssl.com/products/software-integrity/code-signing/iv/)、[eSigner 价格](https://www.ssl.com/guide/esigner-pricing-for-code-signing/)、[SignPath 条款](https://signpath.org/terms.html)。价格以实际结算为准，不含可能的税费和汇率变化。以 IV 单年证书加 eSigner 年付最低套餐估算，挂牌合计为 $309/年；这不是已确认可受理的报价。

### 从申请到可用的步骤

1. 确定个人或组织申请，以及用户安装时应看到的发布者身份。
2. 确认签发方接受该主体所在地区的材料。个人准备政府签发的身份证明，其他身份、地址及联系方式材料按签发方要求提交；组织按其验证清单准备材料。身份证明应在签发方的验证入口提交。[SSL.com 验证要求](https://www.ssl.com/faqs/ssl-ov-validation-requirements/)
3. 选择云签名或兼容的硬件令牌。公开可信 OV/IV 代码签名私钥受硬件保护，不能默认会拿到可导出的 PFX 私钥文件。云签名更适合 Cage 现有的 GitHub 托管构建流程。[SSL.com 私钥存储要求](https://www.ssl.com/guide/esigner-pricing-for-code-signing/)
4. 接入签名服务，完成应用、后端、卸载器和安装器签名及时间戳；签名后再计算下载摘要。
5. 在 Windows 验证签名信任、首次安装、保留数据的覆盖安装，以及两个签名版本之间的应用内更新。现有未签名版本仍需手动安装首个签名版本，见 [Windows 更新规则](../windows.md#更新与卸载)。

SignPath 还要求不能以项目证书直接给其他项目的独立二进制签名，但允许在签名安装包中包含未签名的上游开源二进制。Cage 当前会对随附的 Mihomo 可执行文件加签，申请时须明确处理这一差异，并核对 WebView2 等随附组件是否符合其 System Libraries 例外。[SignPath 签名范围与许可条件](https://signpath.org/terms.html)

## 证据版：Cage 接入范围

| 当前行为 | 实现位置 | 接入时须处理的事项 |
|---|---|---|
| Windows CI 从 Secret 还原 PFX、导入当前用户证书库 | `scripts/ci-windows-build.ps1:21`；`.github/workflows/windows-build.yml:104` | 按所选服务改用云签名认证或硬件提供程序；PFX 上传方式不能作为新证书的默认接入假设 |
| 使用证书指纹调用 SignTool，并添加 SHA-256 时间戳及验证签名 | `scripts/windows-common.ps1:94` | 保留签名验证，按服务要求配置调用方式和时间戳端点 |
| 对窗口程序、后端、Mihomo 加签 | `scripts/build-windows.ps1:127` | 核对签名范围与服务条款；普通构建不消耗生产签名配额 |
| NSIS 构建时对卸载器加签；打包后对安装器加签 | `packaging/windows/Cage.nsi:44`；`scripts/package-windows.ps1:68` | 接入必须覆盖卸载器签名命令，不能只替换安装器最后一步 |
| 更新器验证 Windows 信任，并比较当前程序与更新包的发布者公钥摘要 | `desktop/windows/Authenticode.cs:25`、`:32`、`:37` | 服务切换、换钥、证书续签时验证升级连续性；同名发布者不足以通过该检查 |

按当前流程，每个完整签名构建包含 5 个签名对象，最低 20 次/月套餐理论上覆盖 4 次完整构建，重试和额外测试另计。此计数来自脚本，不代表签名服务对失败、重试或批次请求的最终计费规则。

微软 Artifact Signing 使用短期证书并自动管理证书与密钥生命周期。若选择该服务，须核对不同签名日期的公钥是否相同，并处理 Cage 当前公钥固定规则与轮换之间的关系；不能仅凭一次安装成功认定后续更新可用。[微软证书管理](https://learn.microsoft.com/en-us/azure/artifact-signing/concept-certificate-management)

本次没有修改打包流程、接口或数据格式，因此 macOS 和 Linux 无实现变更。Windows 云签名、硬件签名及签名版本升级均未实测。后续实现需同步检查共用发行就绪检查、三平台工作流及说明，继续保留明确发版触发点。
