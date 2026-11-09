# 参与开发

Cage 的桌面开发目标为 Apple Silicon macOS 14+、Windows 10 22H2/11 x64 和 Debian/Ubuntu Linux x86_64。平台构建工具、安装器和验收范围见 [Windows 指南](docs/windows.md#维护者构建与验收)与 [Linux 指南](docs/linux.md#维护者构建与验收)。用户安装预构建 App 即可使用；本页面向需要修改工作台、控制服务或虚拟机底座的贡献者。

## 从源码构建

需要 Go 1.26.10 或更新版本、Node.js 24、`uv` 和 Xcode Command Line Tools。Go 版本以 `go.mod` 为准，前端依赖使用仓库中的 npm 锁文件，Python 工具统一通过 `uv` 运行。

```sh
git clone https://github.com/tan396222-prog/Claude-AntiBan.git
cd Claude-AntiBan
bash scripts/build-app.sh
```

构建会下载并校验固定版本的 Mihomo，构建前端和嵌入式客机组件，再生成 `_output/Cage.app`。未指定签名身份的本地开发构建使用 ad-hoc 签名；不能将其视为可通过 Gatekeeper 的正式发行包。

需要保留日常 App 时，指定独立输出且不使用 `--install`：`CAGE_BUILD_OUTPUT="$PWD/_output/validation/Cage.app" bash scripts/build-app.sh`。输出必须是绝对 `.app` 路径；此模式不清理其他副本。目标 App 或控制程序仍在运行时，构建会拒绝覆盖。开发构建也不能用于验证正式签名包的自动安装成功路径。

安装到当前用户的 Applications 目录：

```sh
bash scripts/build-app.sh --install
```

安装位置为 `~/Applications/Cage.app`。不要在已启动的 App 上直接覆盖构建结果；先保存文件、停止环境并退出。正式发行的签名、公证与安装包构建见[发布 macOS 安装包](docs/releasing.md)。仓库中保留的上游 Nix 和多平台发行配置供参考，不代表 Cage 桌面版支持这些发行目标。

仅修改界面时可运行 `npm run --prefix ui dev`，但独立前端服务器仍需要控制服务和正确会话，不能用空白页面代替真实 App 验收。整体构建入口是 `make cage`，会生成 `_output/bin/cage`。

## 代码与设计入口

| 目录 | 职责 |
|---|---|
| `desktop/` | Swift macOS 窗口；`desktop/windows/` 为 WinForms/WebView2 窗口；`desktop/linux/` 为 GTK/WebKitGTK 窗口 |
| `ui/` | React 工作台、代理管理、设置和网络状态 |
| `internal/workbench/` | 工作台控制接口、任务、终端和设置 |
| `internal/cage/` | 强制代理、网络门槛和客机服务 |
| `internal/managedproxy/` | 订阅、节点、代理核心和私有配置 |
| `third_party/`、`patches/` | 固定的上游源码与补丁 |
| `www/` | 项目主页 |

修改前阅读对应的[工作台设计](docs/design/workbench.md)、[网络检查设计](docs/design/network-checks.md)或[内置代理设计](docs/design/managed-proxy.md)。调整接口、数据归属或安全边界时，同时更新设计与用户说明。

修改后端任务、终端、浏览器或连接回收时，从[控制服务职责](docs/design/control-service.md)定位入口、资源拥有者和三平台适配；传输层与业务规则分别维护。

## 自动检查与发布

日常推送会运行三平台检查并生成测试包，版本号保持不变。只有用户明确要求发版时，才运行独立的发布入口；配置和失败恢复见 [CI/CD 指南](docs/ci-cd.md)。

## 验证改动

选择覆盖本次行为的检查。工作台的基础检查：

```sh
npm ci --prefix ui
npm run --prefix ui lint
npm test --prefix ui
node --test internal/cage/clipboard.test.cjs internal/cage/browser-audit.test.mjs internal/cage/browser-downloads.test.mjs
uv run internal/cage/save-download.test.py
uv run internal/cage/editor-migrate.test.py
go test -race ./internal/workbench ./internal/cage ./internal/managedproxy
go test ./internal/... ./cmd/...
```

修改底层网络时，还需执行相关模块测试：

```sh
go test github.com/containers/gvisor-tap-vsock/pkg/services/dns \
  github.com/lima-vm/lima/v2/pkg/hostagent/dns \
  github.com/lima-vm/lima/v2/pkg/networks/usernet
```

修改外观或主题管理时，执行 `node ui/scripts/appearance-smoke.mjs`、`node ui/scripts/theme-smoke.mjs` 和 `node ui/scripts/builtin-theme-smoke.mjs`，并运行 `go test -race ./internal/workbench -run 'Test(CodexAppearance|Appearance)'`。浏览器脚本需要可用的 Playwright 和前端开发服务，可通过 `CAGE_PLAYWRIGHT_MODULE`、`CAGE_UI_URL` 指定；使用隔离控制接口，不接入日常 VM。原生文件窗口还须在实际 App 中验证导入、导出与取消。

修改浏览器启动或控制逻辑时，在 Linux 客机的独立 X 显示中运行 `DISPLAY=:183 node scripts/browser-controls-smoke.mjs`。该检查使用临时配置和真实控制器，验证人工浏览标记、标签与导航、重启后的页面及 Cookie 恢复，并检查未认证请求和跨来源请求仍被拒绝。

修改网站图标或网页复制时，运行 `node --test internal/cage/browser-clipboard.test.mjs`、`node scripts/browser-clipboard-smoke.mjs`、`node scripts/favicon-extension-smoke.mjs` 和 `node scripts/browser-metadata-smoke.mjs`。后两项通过 `CAGE_TEST_BROWSER_PATH` 指定支持扩展的 Chromium，复制脚本使用隔离 headless 剪贴板。生产控制器测试只适配路径、监听端口和 headless 模式，完整 Linux 客机会话仍需上述独立显示测试。前端开发服务启动后，运行 `node ui/scripts/browser-metadata-smoke.mjs` 验证图标、复制事件、RFB 消息和焦点隔离；该脚本用记录替身接收原生剪贴板写入。Playwright 模块与前端地址可分别通过 `CAGE_PLAYWRIGHT_MODULE`、`CAGE_UI_URL` 指定。

修改更新或恢复界面时，运行 `node ui/scripts/recovery-ui-smoke.mjs`；修改画面可见性时，运行 `node ui/scripts/remote-visibility-smoke.mjs`。两者需要可用的 Playwright 和已启动的前端开发服务，可通过 `CAGE_UI_URL` 指定地址；测试使用隔离接口，后者还使用真实 RFB 握手与帧验证停止和恢复，不连接日常 VM。修改原生更新流程时运行 `bash scripts/test-app-updates.sh`，正式签名、公证及真实安装仍须单独验收。组件清单、固定工具安装器和网络续租回归已包含在上述 Go 包测试中，无需重复执行一套独立命令。

修改浏览器前进、后退时，运行 `node ui/scripts/browser-navigation-smoke.mjs`，并用 `CAGE_TEST_BROWSER=webkit` 验证 WebKit。界面测试覆盖推送更新、不依赖标签轮询、切换标签和状态轮询后的按钮状态；上述客机 `scripts/browser-controls-smoke.mjs` 还验证真实 Chromium 的完整页面及 `pushState` 导航，并只从事件流确认按钮状态更新。

修改上游源码时，保持 `patches/` 与 `third_party/` 一致，并按 `patches/README.md` 核对同步方式。UI 改动需要在实际 App 中验证状态、失败恢复和关键交互；编译通过不能证明登录、联网或用户操作已成功。

建议使用独立测试配置和非敏感测试项目，避免将开发验证写入日常环境。启动前的 Git 保护检查属于主机状态：单独指定 Cage 配置目录不会隔离全局 Git 设置，测试修改路径时应使用隔离的测试用户目录。应用必须先解释影响，用户明确确认后才将 `safe.bareRepository` 设置为 `explicit`，并在复检通过后启动环境；读取状态不得修改 Git 配置。真实网络验证应区分代理连通、环境放行、浏览器访问、Claude 授权与模型请求，不能互相替代。

修改日志、原生错误捕获或诊断导出时，执行 `go test -race ./internal/diagnostics ./internal/workbench ./internal/managedproxy ./internal/limaembed ./internal/cli`。构建工作台与 `cage` 后，用 `node ui/scripts/diagnostics-smoke.mjs /absolute/path/to/cage` 验证真实后台、界面下载、脱敏 ZIP 和离线导出；Playwright 路径可通过 `CAGE_PLAYWRIGHT_MODULE` 指定。三个平台的原生保存、取消和覆盖行为需要分别实测，客机日志 namespace 需要 Linux/systemd 验证。

## 开发诊断

配置与状态默认在 `~/.claude-cage/`。`CLAUDE_CAGE_CONFIG` 可以指向另一份私有配置，其父目录拥有独立状态。配置文件权限为 `0600`；不要把该目录作为共享项目或提交到 Git。

`environment.json` 固定记录环境的 VM 目录，`projects.json` 保存明确打开的共享目录，`workbench.json` 保存当前项目与偏好。选择项目不会改变 VM 身份。浏览器配置在客机的 `/home/cage-browser/profile`，受管工具的最低版本与包摘要见 `internal/cage/tool-versions.env`。替换 App、同步组件和升级工具是不同操作，契约见[工作台设计](docs/design/workbench.md#持久环境的组件与工具版本)。

```sh
bash scripts/fetch-proxy-core.sh
make cage
./_output/bin/cage check   # 检查配置的代理出口
./_output/bin/cage shell   # 打开共享 Cage 环境中的 shell
./_output/bin/cage stop    # 停止该环境
./_output/bin/cage desktop # 在本机浏览器打开一次性入口
```

`cage access`、独立 VNC 和编辑器端口仅供诊断，输出可能包含访问凭据，不要直接上传。普通用户通过 App 或 `cage desktop` 自动建立会话，无需输入内部访问密码。

代理节点统一通过 Cage 内的订阅、本地 YAML 或手动节点入口配置。运行时只使用 App 随附核心。来源、运行配置和日志位于私有配置的 `proxy/` 子目录；macOS 通过独立用户 launchd 任务运行，Linux 通过普通用户后台进程运行。来源与凭据文件权限为 `0600`。`config.json` 中的代理端点和版本由节点管理器生成，通用设置及恢复操作不能覆盖。

## 提交改进

提交说明应包含用户遇到的具体问题、改动后的行为、验证方式及尚未覆盖的情形。保留与本次任务无关的在途改动，不将本机路径、账号、代理订阅、密钥、构建产物或私人截图提交到仓库。

新增依赖需保留许可和来源。Cage 主许可为 MIT，随附组件有各自许可；更新 Mihomo 等独立程序时同时更新固定版本、校验值、来源与发行包中的许可资料。
