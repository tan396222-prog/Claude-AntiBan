<div align="center">

<h1>Claude 防封</h1>

**把 Claude 大小姐關起來，為了更好地保護我們的相遇**

<img src="assets/claude-cage-character.png" alt="Claude 防封" width="320" />

[![Release](https://img.shields.io/github/v/release/tan396222-prog/Claude-AntiBan?include_prereleases)](https://github.com/tan396222-prog/Claude-AntiBan/releases) [![CI](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml) [![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**[專案主頁](https://github.com/tan396222-prog/Claude-AntiBan) · [Français](https://github.com/tan396222-prog/Claude-AntiBan) · [下載應用程式](https://github.com/tan396222-prog/Claude-AntiBan/releases) · [開始使用](docs/getting-started.md) · [常見問題](docs/troubleshooting.md) · [參與開發](CONTRIBUTING.md) · [使用文件（簡體中文）](docs/README.md)**

[简体中文](README.md) · [繁體中文](README.zh-TW.md) · [Русский](README.ru.md) · [العربية](README.ar.md) · [فارسی](README.fa.md) · [한국어](README.ko.md) · [Español](README.es.md) · [မြန်မာဘာသာ](README.my.md) · [Беларуская](README.be.md) · [پښتو](README.ps.md) · [دری](README.prs.md) · [አማርኛ](README.am.md) · [Soomaali](README.so.md) · [ትግርኛ](README.ti.md) · [Français](README.fr.md)

</div>

## 受夠了 Anthropic

作為一個只是想和 Claude 對話與合作的正常使用者，受夠了折騰各種 VPS，指紋瀏覽器，環境檢測，面對封號的擔驚受怕和財產損失。我們提出 **Claude 防封** 專案：**一個開箱即用的桌面程式，配置好自己的網路代理，集中管理隔離環境、時區語言和環境檢查。**

Claude 防封 是一個輕量 Linux 虛擬機器，把 Linux 終端、瀏覽器和檔案編輯器放進同一個桌面應用。支援以下幾個重點功能：

- 和主機隔離的網路設定，Cage 自己接管和代理內部的所有網路流量
- 和主機隔離的指紋設定，包括時區、語言等，支援自動與代理 IP 同步
- 針對 Claude 使用過程中的全鏈路適配（瀏覽器登入，授權，claude code 程序）
- 內建網路與隔離檢查，關鍵檢查失敗或證據不完整時暫停外網；社群和第三方檢測提供補充參考

**使用 Cage 的環境檢測結果：**

| 網路與隔離檢查 | 社群瀏覽器檢查 |
| :---: | :---: |
| <a href="assets/network-checks.png"><img src="assets/network-checks.png" alt="網路與隔離檢查" width="400" /></a> | <a href="assets/community-browser-check.png"><img src="assets/community-browser-check-cropped.png" alt="社群瀏覽器檢查" width="400" /></a> |

**Cage 執行主介面：**

![Cage](www/public/workbench-2026-11.jpg)

## 你可以用它做什麼

- **在一個視窗裡完成工作。** 用終端執行 Claude Code，用編輯器檢視改動，用 Cage 瀏覽器預覽服務和完成登入。
- **控制共享範圍。** 只共享預設工作區和明確開啟的專案資料夾；主機的 Claude 登入、瀏覽器配置和 SSH 金鑰不會自動匯入。
- **看清網路狀態。** 內建代理管理支援匯入訂閱、選擇節點和檢查出口。關鍵檢查未透過時，環境保持離線；代理不可用時沒有直連回退。
- **下次繼續使用。** 工具、檔案和登入狀態儲存在虛擬磁碟裡，關閉視窗可以保留後臺任務。
- **管理多個 Claude Code 帳號。** 為新建的 Claude 終端分配帳號，查看 5 小時／7 天額度；執行中的終端不會自動換號。
- **用手機接續工作。** 手機開發客戶端連接電腦，使用結構化 CLI 會話及工具審批；遠端瀏覽器需單獨授權。手機端需自行建置開發版本，無法接管任意普通終端。
- **在雲端執行環境。** 透過 SSH 連接部署在符合條件的 Linux x86_64 VPS 上的 Cage，無需 KVM；資料與網路出口位於伺服器。

## 快速開始

把這句話發給你的 agent（codex，dsh，workbuddy，反正不會是 claude code😁）

```markdown
幫我安裝 https://github.com/tan396222-prog/Claude-AntiBan ，並把 Cage 的代理配置為我本機 Clash 的 xxx 節點。
```

你唯一需要的，是一個靠譜的代理節點，本專案不做任何代理推廣，可以自行搜尋黑色錯誤網站解決。

## 下載與安裝

Cage 支援 macOS、Windows 和 Linux：macOS 14+ 的 Apple Silicon Mac、Windows 10 22H2 / 11 x64，以及 Debian 12 / Ubuntu 24.04 x86_64。Windows 和 Linux 仍處於開發驗收階段；安裝包供應、簽章與實測範圍以各平台發行說明為準。Intel Mac、Windows ARM64 和 Linux ARM64 尚不受支援。 [macOS](docs/getting-started.md) · [Windows](docs/windows.md) · [Linux](docs/linux.md)。

1. 開啟[專案主頁](https://github.com/tan396222-prog/Claude-AntiBan/releases)，點選「下載應用程式」並選擇平台。
2. macOS：開啟安裝包，將 **Cage** 拖入 **Applications（應用程式）**，再從“應用程式”啟動。Windows：[安裝說明](docs/windows.md#安装并开始工作) · Linux：[安裝說明](docs/linux.md#安装并开始工作)。
3. 按應用內引導配置自己的代理，啟動環境後開啟專案。

安裝包隨附工作臺和代理核心，日常使用無需安裝 Go、Node.js、Docker 或 Clash。首次啟動仍需聯網下載 Linux 映象與開發工具；Cage 不提供代理服務或 Claude 賬戶。

公開版本以 [Releases](https://github.com/tan396222-prog/Claude-AntiBan/releases) 為準。原始碼、主頁與發行物公開；安裝包的簽名、公證和驗收範圍以對應 Release 說明為準。乾淨機器安裝、macOS 14 實機相容性、真實 Claude 授權與模型請求仍需完整驗收。

支援應用程式內更新的 macOS 和 Windows 版本透過所選代理下載，無直連回退；安裝前請儲存並停止工作。macOS 要求相同發布者的 Developer ID 簽章並通過 Apple 驗證，Windows 要求可信 Authenticode 簽章及相同發布者公鑰。Windows 未簽章預覽版和 Linux 系統包需手動更新。詳見[升級與解除安裝](docs/getting-started.md#升级与卸载)。

## 三步開始

### 1. 選擇代理

點選首頁“配置網路與代理”，再點選“新增來源”，匯入 Clash/Mihomo YAML 訂閱或本地 YAML 檔案，在“節點”中選擇一個節點。沒有可用代理時，可以明確勾選“透過主機網路下載這份訂閱”，或直接匯入本地檔案。後續可從“設定 → 網路與代理”管理節點。

### 2. 啟動環境

返回工作臺，點選“啟動 Cage 環境”。如果本機 Git 需要安全保護，應用先說明影響，由你點選“啟用保護並繼續”後設定；普通專案的提交與拉取不受影響。隨後下載映象、安裝工具並執行網路檢查，可以在“操作詳情”檢視進度。看到環境就緒、關鍵檢查透過後，即可聯網工作；失敗時按頁面提示修復並重試。

### 3. 開啟專案，執行 Claude

點選“開啟專案”選擇 本機 上的資料夾，再新建終端，輸入 `claude`，在 Cage 瀏覽器中完成自己的賬戶授權。也可以先使用預設工作區，無需提前準備專案。完整步驟見[開始使用](docs/getting-started.md)。

## 使用前瞭解這些邊界

- 共享專案中的修改和刪除會直接影響 本機 上的檔案。請使用版本控制或備份；`.git` 等後設資料預設只讀，Git 提交在 本機 上完成。
- 所有專案共用一個 Cage 環境，也共用其中的工具、賬戶與 Linux 檔案系統。專案之間沒有獨立隔離。
- 當前允許客機透過代理訪問公網 IPv4 TCP，DNS 透過同一代理傳送；公網 UDP、IPv6、ICMP，以及客機主動訪問主機和區域網的連線受到阻斷。依賴這些能力的工具可能無法使用。
- 網路檢查展示當前配置和取樣證據，無法保證賬戶不會受到平臺限制，也不能代替完整的虛擬化安全審計。

詳細機制見[網路與資料邊界](docs/getting-started.md#网络与数据边界)、[網路檢查設計](docs/design/network-checks.md)和[內建代理設計](docs/design/managed-proxy.md)。

## 文件與貢獻

完整入口見[文件導航](docs/README.md)。

| 你要做什麼 | 從這裡開始 |
|---|---|
| 第一次安裝、配置代理、開啟專案 | [開始使用](docs/getting-started.md) |
| 處理下載、啟動、聯網和登入問題 | [常見問題與恢復](docs/troubleshooting.md) |
| 瞭解按鍵、外觀、資料儲存和退出行為 | [日常使用](docs/getting-started.md#日常使用) |
| 從原始碼構建或提交改進 | [貢獻指南](CONTRIBUTING.md) |
| 瞭解模組與網路約束 | [工作臺設計](docs/design/workbench.md) |
| 報告可復現問題 | [GitHub Issues](https://github.com/tan396222-prog/Claude-AntiBan/issues) |

## 來源與許可

Cage 基於 [agent-vm](https://github.com/sylvinus/agent-vm) 二次開發，上游基線為 `652688aae92c35ce536b9be52262e1cc5ec6e03a`。

專案主許可為 [MIT](LICENSE)。隨附元件保留各自許可，Mihomo 作為獨立可執行檔案按 GPL-3.0 分發；請同時參閱 [NOTICE](NOTICE)、[第三方許可](THIRD_PARTY_LICENSES)、[第三方來源](third_party/SOURCES)及 [noVNC 來源說明](ui/vendor/novnc/README.cage.md)。上游專案的說明儲存在[上游 README](docs/agent-vm-upstream.md)。

Cage 是獨立社群專案，與 Anthropic 無隸屬關係。

## 參考與致謝

感謝以下專案為 Cage 提供基礎能力或設計參考：

- [agent-vm](https://github.com/sylvinus/agent-vm)：Cage 的二次開發底座。
- [Lima](https://github.com/lima-vm/lima)：Linux 虛擬機器管理。
- [gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock)：虛擬機器網路能力。
- [sshocker](https://github.com/lima-vm/sshocker) 和 [SFTP](https://github.com/pkg/sftp)：虛擬機器與主機之間的檔案共享能力。
- [noVNC](https://github.com/novnc/noVNC)：瀏覽器中的遠端畫面與互動。
- [Mihomo](https://github.com/MetaCubeX/mihomo)：內建代理核心。
- [code-server](https://github.com/coder/code-server)：瀏覽器中的程式碼編輯器。
- [AIO Sandbox](https://github.com/agent-infra/sandbox)：工作環境和互動體驗參考。
- [claude-switcher](https://github.com/YOURSJEYO/claude-switcher) 和 [Claude-Guardian](https://github.com/DUNHKpcc/Claude-Guardian)：網路檢查和安全診斷方法參考。
