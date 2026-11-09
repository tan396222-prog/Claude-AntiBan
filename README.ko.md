<div align="center">

<h1>Claude 防封</h1>

**클로드 양을 우리에 가둬서 우리가 함께하는 시간을 더 잘 보호할 수 있도록**

<img src="assets/claude-cage-character.png" alt="Claude 防封" width="320" />

[![Release](https://img.shields.io/github/v/release/tan396222-prog/Claude-AntiBan?include_prereleases)](https://github.com/tan396222-prog/Claude-AntiBan/releases) [![CI](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml) [![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**[프로젝트 웹사이트](https://github.com/tan396222-prog/Claude-AntiBan) · [Français](https://github.com/tan396222-prog/Claude-AntiBan) · [앱 다운로드](https://github.com/tan396222-prog/Claude-AntiBan/releases) · [시작하기](docs/getting-started.md) · [FAQ](docs/troubleshooting.md) · [기여](CONTRIBUTING.md) · [사용 문서 (중국어)](docs/README.md)**

[简体中文](README.md) · [繁體中文](README.zh-TW.md) · [Русский](README.ru.md) · [العربية](README.ar.md) · [فارسی](README.fa.md) · [한국어](README.ko.md) · [Español](README.es.md) · [မြန်မာဘာသာ](README.my.md) · [Беларуская](README.be.md) · [پښتو](README.ps.md) · [دری](README.prs.md) · [አማርኛ](README.am.md) · [Soomaali](README.so.md) · [ትግርኛ](README.ti.md) · [Français](README.fr.md)

</div>

## Anthropic에 지쳤습니다

클로드와 대화하고 협업하고 싶은 일반 사용자로서 VPS 인스턴스, 브라우저 지문 프로필, 환경 확인을 구성하는 데 지쳤으며 계정 금지 및 금전적 손실을 걱정합니다. 우리는 선물한다 **Claude 防封** — **즉시 사용 가능한 데스크탑 애플리케이션: 자체 네트워크 프록시를 구성하고 격리된 환경, 시간대, 언어 및 환경 확인을 한 곳에서 관리합니다.**

Claude 防封는 Linux 터미널, 브라우저 및 파일 편집기를 단일 데스크톱 애플리케이션에 결합한 경량 Linux 가상 머신입니다. 주요 기능은 다음과 같습니다:

- 호스트와 격리된 네트워크 설정 Cage는 자체 네트워크 프록시를 통해 환경 내부의 모든 네트워크 트래픽을 라우팅합니다.
- 네트워크 프록시의 IP 위치에 대한 자동 동기화를 통해 시간대 및 언어를 포함하여 호스트에서 격리된 브라우저 지문 설정
- Claude 워크플로우 전반에 걸친 통합: 브라우저 로그인, 계정 인증 및 Claude Code 프로세스
- 내장된 네트워크 및 격리 검사는 중요한 검사가 실패하거나 증거가 불완전한 경우 인터넷 액세스를 일시 중지합니다. 커뮤니티 및 제3자 확인을 통해 추가 참조 정보 제공

**Cage를 사용한 환경 점검 결과:**

| 네트워크 및 격리 검사 | 커뮤니티 브라우저 검사 |
| :---: | :---: |
| <a href="assets/network-checks.png"><img src="assets/network-checks.png" alt="네트워크 및 격리 검사" width="400" /></a> | <a href="assets/community-browser-check.png"><img src="assets/community-browser-check-cropped.png" alt="커뮤니티 브라우저 검사" width="400" /></a> |

**Cage의 기본 인터페이스:**

![Cage](www/public/workbench-2026-11.jpg)

## 케이지로 할 수 있는 일

- **단일 창에서 작업합니다.** 터미널에서 Claude Code를 실행하고, 편집기에서 변경 사항을 검토하고, Cage 브라우저를 사용하여 서비스를 미리 보고 로그인하세요.
- **공유하는 내용을 제어하세요.** 기본 작업공간과 명시적으로 열린 프로젝트 폴더만 공유됩니다. 호스트의 Claude 로그인 상태, 브라우저 프로필 및 SSH 키는 자동으로 가져오지 않습니다.
- **네트워크 상태를 확인하세요.** 내장된 네트워크 프록시 관리를 통해 구독을 가져오고, 엔드포인트를 선택하고, 종료 IP를 확인할 수 있습니다. 중요한 검사가 통과될 때까지 환경은 오프라인 상태로 유지됩니다. 네트워크 프록시를 사용할 수 없는 경우 직접 연결로 대체할 수 없습니다.
- **다음에 계속하세요.** 도구, 파일 및 로그인 상태는 가상 디스크에 유지됩니다. 창을 닫아도 백그라운드 작업이 계속 실행될 수 있습니다.
- **Claude Code 계정을 여러 개 관리하세요.** 새 Claude 터미널에 계정을 배정하고 5시간·7일 사용 한도를 확인할 수 있습니다. 실행 중인 터미널의 계정은 자동으로 바뀌지 않습니다.
- **휴대폰에서 작업을 이어가세요.** 개발용 모바일 클라이언트로 컴퓨터에 연결해 구조화된 CLI 세션과 도구 승인을 처리합니다. 원격 브라우저는 별도 허용이 필요합니다. 모바일 클라이언트는 소스에서 개발 버전을 빌드해야 하며, 임의의 일반 터미널을 제어하는 기능은 아닙니다.
- **클라우드에서 Cage를 실행하세요.** 요구 조건을 충족하는 Linux x86_64 VPS에 Cage를 배포하고 SSH로 연결합니다. KVM은 필요 없으며 데이터와 네트워크 출구는 서버에 있습니다.

## 빠른 시작

이 요청을 AI 에이전트(codex, dsh, workbuddy — Claude Code는 아닐 수도 있음 😁) 에 보냅니다.

```markdown
설치 도와주세요 https://github.com/tan396222-prog/Claude-AntiBan 로컬 Clash 설치에서 xxx 엔드포인트를 사용하도록 Cage의 네트워크 프록시를 구성합니다.
```

필요한 것은 안정적인 네트워크 프록시 엔드포인트뿐입니다. 이 프로젝트는 프록시 공급자를 홍보하지 않습니다. 예를 들어 Black Error 웹사이트를 검색하여 제공자를 직접 찾을 수 있습니다.

## 다운로드 및 설치

Cage는 macOS, Windows, Linux를 지원합니다. 대상은 macOS 14+의 Apple Silicon Mac, Windows 10 22H2 / 11 x64, Debian 12 / Ubuntu 24.04 x86_64입니다. Windows와 Linux는 개발 검증 중입니다. 설치 패키지 제공 여부, 서명 및 실제 검증 범위는 릴리스 노트를 확인하세요. Intel Mac, Windows ARM64, Linux ARM64는 아직 지원하지 않습니다. [macOS](docs/getting-started.md) · [Windows](docs/windows.md) · [Linux](docs/linux.md).

1. 앱 다운로드: [macOS / Windows / Linux](https://github.com/tan396222-prog/Claude-AntiBan/releases).
2. macOS: 설치 패키지를 열고 드래그하세요. **Cage** 으로 **응용**; 그런 다음 응용 프로그램에서 실행하십시오. Windows: [설치 안내](docs/windows.md#安装并开始工作) · Linux: [설치 안내](docs/linux.md#安装并开始工作).
3. 앱 내 지침에 따라 자신의 네트워크 프록시를 구성하고, 환경을 시작하고, 프로젝트를 엽니다.

설치 프로그램에는 워크벤치와 네트워크 프록시 코어가 포함되어 있습니다. 일상적인 사용에는 Go, Node.js, Docker 또는 Clash를 설치할 필요가 없습니다. 처음 실행 시에는 Linux 이미지와 개발 도구를 다운로드하기 위해 인터넷 연결이 필요합니다. Cage는 네트워크 프록시 서비스나 Claude 계정을 제공하지 않습니다.

공개 버전은 [Releases](https://github.com/tan396222-prog/Claude-AntiBan/releases)를 확인하세요. 소스 코드, 웹사이트, 릴리스는 공개되어 있습니다. 설치 프로그램 서명, Apple 공증 및 검증 범위에 대해서는 각 릴리스의 노트를 참조하세요. 클린 머신 설치, macOS 14를 실행하는 실제 Mac에서의 호환성, 실제 Claude 계정 인증 및 모델 요청에는 여전히 완전한 검증이 필요합니다.

macOS 및 Windows 인앱 업데이트는 선택한 프록시로 다운로드하며 직접 연결로 전환하지 않습니다. 설치 전에 작업을 저장하고 중지하세요. macOS는 동일 게시자의 Developer ID 서명과 Apple 검증이 필요하며, Windows는 신뢰할 수 있는 Authenticode 서명과 동일 게시자 공개 키가 필요합니다. 서명 없는 Windows 미리 보기와 Linux 패키지는 수동으로 업데이트합니다. [업데이트 및 제거](docs/getting-started.md#升级与卸载).

## 3단계로 시작하세요

### 1. 네트워크 프록시를 선택하세요

홈페이지에서 '네트워크 및 프록시 구성'을 클릭한 다음 '소스 추가'를 클릭하세요. Clash/Mihomo YAML 구독 또는 로컬 YAML 파일을 가져오고 '노드'에서 엔드포인트를 선택합니다. 네트워크 프록시를 사용할 수 없는 경우 '호스트 네트워크를 통해 이 구독 다운로드'를 명시적으로 선택하거나 로컬 파일을 직접 가져올 수 있습니다. 나중에 '설정 → 네트워크 및 프록시'에서 엔드포인트를 관리하세요.

### 2. 환경 시작

워크벤치로 돌아가서 'Cage 환경 시작'을 클릭하세요. 호스트의 Git 설정에 보안 보호가 필요한 경우 애플리케이션은 먼저 그 효과를 설명합니다. '보호 활성화 및 계속'을 클릭하여 적용하세요. 일반 프로젝트에 대한 커밋 및 끌어오기는 영향을 받지 않습니다. 그런 다음 Cage는 이미지를 다운로드하고 도구를 설치하며 네트워크 검사를 실행합니다. '작업 세부정보'에서 진행 상황을 확인하세요. 환경이 준비되고 중요한 점검을 통과하면 온라인으로 작업할 수 있습니다. 확인에 실패하면 지침에 따라 문제를 해결하고 다시 시도하세요.

### 3. 프로젝트를 열고 Claude를 실행하세요.

컴퓨터에서 폴더를 선택하려면 '프로젝트 열기'를 클릭하고 새 터미널을 열고 다음을 입력하세요. `claude`; 그런 다음 Cage 브라우저에서 자신의 계정을 인증하세요. 프로젝트를 준비하지 않고 기본 작업공간으로 시작할 수도 있습니다. 전체 단계는 다음을 참조하세요. [시작하기](docs/getting-started.md).

## 사용하기 전에 이러한 제한 사항을 이해하십시오.

- 공유 프로젝트의 편집 및 삭제는 컴퓨터의 파일에 직접적인 영향을 미칩니다. 버전 제어 또는 백업을 사용하십시오. `.git` 기타 메타데이터는 기본적으로 읽기 전용입니다. 컴퓨터에서 Git 커밋을 수행합니다.
- 모든 프로젝트는 도구, 계정 및 Linux 파일 시스템을 포함하여 하나의 Cage 환경을 공유합니다. 프로젝트는 서로 격리되지 않습니다.
- 게스트는 현재 네트워크 프록시를 통해 공용 IPv4 TCP에 액세스할 수 있으며, DNS는 동일한 프록시를 통해 전송됩니다. 호스트 또는 로컬 네트워크에 대한 공용 UDP, IPv6, ICMP 및 게스트 시작 연결이 차단됩니다. 이러한 기능에 의존하는 도구는 작동하지 않을 수 있습니다.
- 네트워크 검사에는 현재 구성과 샘플링된 증거가 표시됩니다. 계정이 플랫폼 제한을 피할 것이라고 보장할 수 없으며 완전한 가상화 보안 감사를 대체하지 않습니다.

자세한 내용은 다음을 참조하세요. [네트워크 및 데이터 경계](docs/getting-started.md#网络与数据边界), [네트워크 검사 디자인](docs/design/network-checks.md) 그리고 [내장형 네트워크 프록시 설계](docs/design/managed-proxy.md).

## 문서화 및 기여

전체 색인을 보려면 다음을 참조하세요. [문서 색인](docs/README.md).

| 귀하의 작업 | 여기서 시작하세요 |
|---|---|
| 네트워크 프록시 설치, 구성 및 첫 번째 프로젝트 열기 | [시작하기](docs/getting-started.md) |
| 다운로드, 시작, 연결 및 로그인 문제 해결 | [FAQ 및 복구](docs/troubleshooting.md) |
| 키보드 컨트롤, 모양, 데이터 지속성 및 종료 동작 알아보기 | [매일 사용](docs/getting-started.md#日常使用) |
| 소스에서 빌드하거나 개선에 기여 | [투고 가이드](CONTRIBUTING.md) |
| 모듈 책임 및 네트워크 제약 사항 이해 | [작업대 디자인](docs/design/workbench.md) |
| 재현 가능한 문제 보고 | [GitHub Issues](https://github.com/tan396222-prog/Claude-AntiBan/issues) |

## 원산지 및 라이선스

케이지는 다음에서 파생됩니다. [agent-vm](https://github.com/sylvinus/agent-vm) ; 업스트림 기준선은 다음과 같습니다. `652688aae92c35ce536b9be52262e1cc5ec6e03a`.

프로젝트의 기본 라이센스는 다음과 같습니다. [MIT](LICENSE). 번들 구성 요소는 해당 라이센스를 유지합니다. Mihomo는 GPL-3.0에 따라 별도의 실행 파일로 배포됩니다. 또한 참조하십시오 [NOTICE](NOTICE), [타사 라이선스](THIRD_PARTY_LICENSES), [제3자 소스](third_party/SOURCES) 그리고 [noVNC 출처](ui/vendor/novnc/README.cage.md). 업스트림 프로젝트의 설명은 [업스트림 읽어보기](docs/agent-vm-upstream.md).

## 참고자료 및 감사의 말

기본 기능 및 설계 참조에 대한 다음 프로젝트에 감사드립니다.

- [agent-vm](https://github.com/sylvinus/agent-vm): Cage의 업스트림 기반.
- [Lima](https://github.com/lima-vm/lima): Linux 가상 머신 관리.
- [gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock): 가상 머신 네트워킹.
- [sshocker](https://github.com/lima-vm/sshocker) 그리고 [SFTP](https://github.com/pkg/sftp): 가상 머신과 호스트 간의 파일 공유.
- [noVNC](https://github.com/novnc/noVNC): 브라우저에서의 원격 표시 및 상호 작용.
- [Mihomo](https://github.com/MetaCubeX/mihomo): 내장된 네트워크 프록시 코어입니다.
- [code-server](https://github.com/coder/code-server): 브라우저 기반 코드 편집기입니다.
- [AIO Sandbox](https://github.com/agent-infra/sandbox): 작업 환경 및 인터랙션 디자인에 대한 참고 자료입니다.
- [claude-switcher](https://github.com/YOURSJEYO/claude-switcher) 그리고 [Claude-Guardian](https://github.com/DUNHKpcc/Claude-Guardian): 네트워크 점검 및 보안 진단을 위한 참고 자료입니다.
