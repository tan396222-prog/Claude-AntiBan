<div align="center">

<h1>Claude 防封</h1>

**Ku hay Miss Claude qafis, si aan si wanaagsan u ilaalin karno waqtigeena wada jirka ah**

<img src="assets/claude-cage-character.png" alt="Claude 防封" width="320" />

[![Release](https://img.shields.io/github/v/release/tan396222-prog/Claude-AntiBan?include_prereleases)](https://github.com/tan396222-prog/Claude-AntiBan/releases) [![CI](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml) [![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**[Mareegta mashruuca](https://github.com/tan396222-prog/Claude-AntiBan) · [Français](https://github.com/tan396222-prog/Claude-AntiBan) · [Soo dejiso abka](https://github.com/tan396222-prog/Claude-AntiBan/releases) · [Bilaabidda](docs/getting-started.md) · [FAQ](docs/troubleshooting.md) · [Wax ku darso](CONTRIBUTING.md) · [Hagaha isticmaalka (Shiine)](docs/README.md)**

[简体中文](README.md) · [繁體中文](README.zh-TW.md) · [Русский](README.ru.md) · [العربية](README.ar.md) · [فارسی](README.fa.md) · [한국어](README.ko.md) · [Español](README.es.md) · [မြန်မာဘာသာ](README.my.md) · [Беларуская](README.be.md) · [پښتو](README.ps.md) · [دری](README.prs.md) · [አማርኛ](README.am.md) · [Soomaali](README.so.md) · [ትግርኛ](README.ti.md) · [Français](README.fr.md)

</div>

## Waan ka daalnay Anthropic

Isticmaalayaasha caadiga ah ee raba inay la hadlaan Claude, waanu ka daalnay habaynta tusaalooyinka VPS, sawirka faraha browserka, iyo hubinta deegaanka, anagoo ka walwalsanayn xayiraadaha akoonnada iyo khasaaraha maaliyadeed. Waxaan soo bandhigaynaa **Claude 防封** -**codsi desktop diyaar u ah in la isticmaalo: habayso wakiilkaaga shabakad oo maamul deegaankaaga go'doonsan, aagga wakhtiga, luqadda, iyo hubinta deegaanka ee hal meel ah.**

Claude 防封 waa mashiin macmal ah oo Linux ah oo fudud kaas oo isku dara terminaalka Linux, browserka, iyo tafatiraha faylka hal codsi oo desktop ah. Tilmaamaha ugu muhiimsan waxaa ka mid ah:

- Dejinta shabakada oo ka go'an martigeliyaha; Dariiqyada qafisku waxay maraa dhammaan taraafikada shabakada gudaha deegaankeeda iyada oo u sii marinaysa wakiilkeeda shabakadeeda
- Dejinta faraha browserka ee ka go'doon martigeliyaha, oo ay ku jiraan aaga wakhtiga iyo luqadda, oo leh si toos ah ula mid ah goobta wakiilka shabakada
- Isku dhafka inta lagu jiro socodka shaqada Claude: soo gelida browserka, oggolaanshaha akoontiga, iyo nidaamka Claude Code
- Shabakad la dhisay iyo jeegaga go'doominta ayaa hakiya gelitaanka intarneedka marka hubinta muhiimka ahi ay fashilmaan ama caddayntu aanay dhammaystirnayn; jeegaga bulshada iyo qolo saddexaad waxay bixiyaan macluumaad tixraac dheeraad ah

**Natiijooyinka hubinta deegaanka ee Cage:**

| Hubinta shabakadda iyo go’doominta | Hubinta biraawsarka ee bulshada |
| :---: | :---: |
| <a href="assets/network-checks.png"><img src="assets/network-checks.png" alt="Hubinta shabakadda iyo go’doominta" width="400" /></a> | <a href="assets/community-browser-check.png"><img src="assets/community-browser-check-cropped.png" alt="Hubinta biraawsarka ee bulshada" width="400" /></a> |

**Interface ugu weyn ee Cage:**

![Cage](www/public/workbench-2026-11.jpg)

## Maxaad ku samayn kartaa Cage

- **Ka shaqee hal daaqad.** Orod Claude Code gudaha terminalka, dib u eeg isbeddelada tifaftiraha, oo isticmaal browserka Cage si aad u aragto adeegyada oo aad u soo gasho.
- **Xakamee waxa aad wadaagto.** Kaliya goobta shaqada ee caadiga ah iyo galka mashruuca ee si cad loo furay ayaa la wadaagaa. Xaaladda soo galitaanka Claude ee martida loo yahay, boggaga browserka, iyo furayaasha SSH si toos ah looma soo dejiyo.
- **Eeg heerka shabakadaada** Maaraynta wakiilnimada shabakada ee ku dhex jirta ayaa kuu ogolaanaysa inaad soo dejiso rukunsiyada, dooro meelaha dhamaadka, oo aad hubiso IP-ga ka baxa. Deegaanku internetka kuma xirmayo ilaa hubinnada muhiimka ahi ay si guul leh u gudbaan. Haddii wakiillada shabakadu maqan tahay, ma dhacayso xiriir toos ah.
- **Sii wad marka xigta** Qalabka, faylalka, iyo xaaladda gelitaanku waxay ku sii jiraan saxanka casriga ah. Hawlaha asalka ahi waxay sii socon karaan markaad daaqadda xidhid.
- **Maamul akoonno badan oo Claude Code ah.** Akoon ayaa loo qoondeeyaa terminal kasta oo Claude ah oo cusub; waxaad arki kartaa qoondada 5 saacadood iyo 7 maalmood. Akoonka terminal socda si toos ah isuma beddelo.
- **Shaqada ka sii wad telefoonka.** Macmiilka moobilka ee horumarinta wuxuu ku xirmaa kombiyuutarka si loo adeegsado kulamada CLI ee habaysan iyo oggolaanshaha qalabka; biraawsarka fog wuxuu u baahan yahay oggolaansho gaar ah. Waa inaad macmiilka moobilka ka dhistaa koodhka isha; ma maamulo terminal kasta oo caadi ah oo hore u furnaa.
- **Cage ku socodsii daruurta.** SSH kaga xirmo Cage lagu rakibay VPS ku habboon oo Linux x86_64 ah, adigoon u baahnayn KVM. Xogta iyo halka shabakadu ka baxdo waxay ku yaallaan serverka.

## Bilaw degdeg ah

U dir codsigan wakiilkaaga AI (codex, dsh, workbuddy - malaha maaha Claude Code 😁):

```markdown
Iga caawi in aan rakibo https://github.com/tan396222-prog/Claude-AntiBan oo habee wakiilka shabakada Cage si aad u isticmaasho xxx dhamaadka rakibida xaafadayda.
```

Waxa kaliya ee aad u baahan tahay waa barta ugu dambeysa ee wakiil shabakad la isku halayn karo. Mashruucani ma dalacsiinayo bixiye wakiil; Waxaad heli kartaa bixiye laftaada, tusaale ahaan adoo raadiya shabakada qaladka madow.

## Soo deji oo rakib

Cage wuxuu taageeraa macOS, Windows iyo Linux: Apple Silicon oo leh macOS 14+, Windows 10 22H2 / 11 x64 iyo Debian 12 / Ubuntu 24.04 x86_64. Windows iyo Linux weli waa la xaqiijinayaa. Helitaanka xirmooyinka, saxiixyada iyo tijaabooyinka waxaa lagu sheegay qoraallada sii-deynta. Intel Mac, Windows ARM64 iyo Linux ARM64 weli lama taageero. [macOS](docs/getting-started.md) · [Windows](docs/windows.md) · [Linux](docs/linux.md).

1. Soo dejiso abka: [macOS / Windows / Linux](https://github.com/tan396222-prog/Claude-AntiBan/releases).
2. macOS: Fur xirmada rakibaadda oo jiid **Cage** galay **Codsiyada**; ka dibna ka bilow Applications. Windows: [Hagaha rakibidda](docs/windows.md#安装并开始工作) · Linux: [Hagaha rakibidda](docs/linux.md#安装并开始工作).
3. Raac tilmaamaha app-ka si aad u habayso wakiilkaaga shabakad, bilaw deegaanka, oo aad u furto mashruuc.

Rakibahu waxa ka mid ah bench-ka shaqada iyo xudunta wakiilka network. Isticmaalka maalinlaha ah uma baahna rakibida Go, Node.js, Docker, ama Clash. Daahfurka ugu horreeya wuxuu weli u baahan yahay isku xirka internetka si loo soo dejiyo sawirka Linux iyo aaladaha horumarinta. Cage ma bixiso adeeg wakiil shabakad ama akoon Claude.

Noocyada dadweynaha ee la heli karo ka eeg [Releases](https://github.com/tan396222-prog/Claude-AntiBan/releases). Koodhka isha, mareegaha, iyo sii daynta waa dadwayne. Tixraac qoraalada sii daynta kasta ee saxeexa rakibaha, notarization Apple, iyo baaxadda ansixinta. Ku rakibida mashiinka nadiifka ah, waafaqid ku saabsan Macs-ka jireed ee ku shaqeeya macOS 14, oggolaanshaha koontada Claude dhabta ah, iyo codsiyada moodeelka ayaa wali u baahan ansaxinta buuxda.

Cusboonaysiinta macOS iyo Windows waxay adeegsataa wakiilka la doortay, iyada oo aan loo noqon xiriir toos ah. Kaydi shaqada oo jooji hawlaha ka hor rakibidda. macOS wuxuu u baahan yahay saxiixa Developer ID ee isla daabacaha iyo xaqiijinta Apple; Windows wuxuu u baahan yahay Authenticode la aamini karo iyo isla furaha guud ee daabacaha. Nooca Windows ee aan saxiixnayn iyo Linux gacanta ayaa loo cusboonaysiiyaa. [Cusboonaysiin iyo saarid](docs/getting-started.md#升级与卸载).

## Ku bilow saddex tallaabo

### 1. Dooro wakiil shabakad

Bogga guriga, dhagsii 'Configure network and proxy', ka dibna 'Ku dar isha'. Soo rar isdiiwaangelinta Clash/Mihomo YAML ama faylka YAML maxalli ah, oo dooro dhammaadka hoosta 'Nodes'. Haddii aysan jirin wakiil shabakadeed, waxaad si cad u dooran kartaa 'Soo deji rukuntan iyada oo loo marayo shabakadda martida loo yahay', ama si toos ah u soo deji faylka maxalliga ah. Ka dib, maamul meelaha dhamaadka ee hoos yimaada 'Settings → Network and proxy'.

### 2. Bilow deegaanka

Ku soo celi goobta shaqada oo dhagsii 'Bilow deegaanka Cage'. Haddii habaynta Git-ga martida loo yahay uu u baahan yahay ilaalin ammaan, codsigu wuxuu marka hore sharxayaa saamaynta. Codso adiga oo gujinaya 'Enable ilaalinta oo sii wad' Ballanqaadka iyo jiidashada mashaariicda caadiga ah ma saameynayso. Cage ka dib wuxuu soo dejiyaa sawirka, rakibaa aaladaha, oo wuxuu wadaa hubinta shabakada. Arag horumarka hoosta 'Faahfaahinta Hawlgalka' Marka deegaanku diyaar yahay oo jeegaga muhiimka ahi dhaafo, waxaad ka shaqayn kartaa onlayn. Haddii jeeggu guuldareysto, raac tilmaamaha si aad u xalliso arrinta oo isku day.

### 3. Fur mashruuc oo maamul Claude

Guji 'Mashruuca Furan' si aad u dooratid galka kombiyuutarka kaaga, fur terminal cusub, oo gal `claude`; dabadeed ogolaw akoonkaaga browserka Cage. Waxaad sidoo kale ku bilaabi kartaa goobta shaqada ee caadiga ah adigoon diyaarin mashruuc. Tallaabooyinka buuxa, eeg [Bilaabidda](docs/getting-started.md).

## Fahmo xadkan ka hor intaanad isticmaalin

- Wax ka beddelka iyo tirtiridda mashaariicda la wadaago waxay si toos ah u saameeyaan faylasha kombiyuutarka kaaga. Isticmaal xakamaynta nooca ama kaydinta. `.git` iyo xogta kale ee badan waxaa loo akhriyaa-kaliya si macquul ah; Git ku samee kombiyuutarka.
- Dhammaan mashruucyada waxay wadaagaan hal deegaan Cage, oo ay ku jiraan qalabkeeda, xisaabaadka, iyo nidaamka faylka Linux. Mashruucyadu maaha kuwo midba midka kale ka soocan.
- Martidu hadda waxay marin u heli karaan IPV4 TCP dadweynaha iyada oo loo marayo wakiilka shabakadda, iyadoo DNS loo soo diray isla wakiilkii. UDP dadweynaha, IPv6, ICMP, iyo isku xidhka martida-bilaabatay ee martigeliyaha ama shabakada maxaliga ah waa la xannibay. Aaladaha ku xiran awoodahaan waxaa laga yaabaa inaysan shaqeynin.
- Jeegaga shabakada waxay tusinaysaa qaabka hadda jira iyo cadaymaha la muunada ah. Ma dammaanad qaadi karaan in akoonku uu ka fogaanayo xannibaadaha goobta, mana beddelaan xisaab-xidhka amniga oo dhammaystiran.

Faahfaahinta, eeg [Xuduudaha shabakada iyo xogta](docs/getting-started.md#网络与数据边界), [Naqshad hubinta shabakada](docs/design/network-checks.md) iyo [Naqshadaynta wakiilnimada shabakadda ee ku dhex jirta](docs/design/managed-proxy.md).

## Dukumentiyada iyo wax ku biirinta

Si aad u hesho tusmada buuxda, eeg [Tusmada dhokumentiyada](docs/README.md).

| Hawshaada | Halkan ka bilow |
|---|---|
| Ku rakib, habee wakiil shabakad, oo fur mashruucaaga ugu horreeya | [Bilaabidda](docs/getting-started.md) |
| Xallinta soo dejinta, bilaabista, isku xidhka, iyo soo gelista | [FAQ iyo soo kabashada](docs/troubleshooting.md) |
| Baro kontaroolada kiiboodhka, muuqaalka, adkaysiga xogta, iyo habdhaqanka bixista | [Isticmaalka maalinlaha ah](docs/getting-started.md#日常使用) |
| Isha ka dhis ama ka qayb qaado hagaajin | [Hagaha wax ku biirinta](CONTRIBUTING.md) |
| Faham mas'uuliyadaha moduleka iyo caqabadaha shabakada | [Naqshadaynta goobta shaqada](docs/design/workbench.md) |
| Ka warbixi arrin dib loo soo saari karo | [GitHub Issues](https://github.com/tan396222-prog/Claude-AntiBan/issues) |

Mashruucu si xawli ah ayuu u socdaa. Ku biir kooxda bulshada si aad ula wadaagto jawaab celinta; ilaaliyayaasha bulshada waa la soo dhawaynayaa.
## Asalka iyo shatiga

Qafis waxaa laga soo qaatay [agent-vm](https://github.com/sylvinus/agent-vm) ; salka sare waa `652688aae92c35ce536b9be52262e1cc5ec6e03a`.

Shatiga ugu weyn ee mashruuca waa [MIT](LICENSE). Qaybaha xidhxidhan waxay haystaan ​​shatiyadooda. Mihomo waxaa loo qaybiyaa sidii mid gaar ah oo la fulin karo iyadoo hoos timaada GPL-3.0. Sidoo kale arag [NOTICE](NOTICE), [Shatiyada xisbiga saddexaad](THIRD_PARTY_LICENSES), [Ilaha dhinac saddexaad](third_party/SOURCES) iyo [noVNC](ui/vendor/novnc/README.cage.md). Sharaxaada mashruuca korka ayaa lagu xafiday gudaha [Kordhinta README](docs/agent-vm-upstream.md).

Cage waa mashruuc bulsho oo madax banaan oo aan xidhiidh la lahayn Anthropic.

## Tix-raac iyo mahadnaq

Waad ku mahadsan tahay mashaariicda soo socota ee kartida aasaasiga ah iyo tixraacyada naqshadeynta:

- [agent-vm](https://github.com/sylvinus/agent-vm): aasaaska sare ee Cage.
- [Lima](https://github.com/lima-vm/lima): Maareynta mashiinka farsamada ee Linux.
- [gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock): isku xirka mashiinka farsamada.
- [sshocker](https://github.com/lima-vm/sshocker) iyo [SFTP](https://github.com/pkg/sftp): wadaagista faylka u dhexeeya mashiinka farsamada iyo martigeliyaha.
- [noVNC](https://github.com/novnc/noVNC): soo bandhigida fog iyo isdhexgalka browserka.
- [Mihomo](https://github.com/MetaCubeX/mihomo):-ku-xidhan xudunta wakiilnimada.
- [code-server](https://github.com/coder/code-server): tifaftiraha koodhka ku salaysan browserka.
- [AIO Sandbox](https://github.com/agent-infra/sandbox): tixraacyada jawiga shaqada iyo naqshadeynta isdhexgalka.
- [claude-switcher](https://github.com/YOURSJEYO/claude-switcher) iyo [Claude-Guardian](https://github.com/DUNHKpcc/Claude-Guardian): tixraacyada hubinta shabakada iyo ogaanshaha amniga.
