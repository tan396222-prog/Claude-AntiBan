<div align="center">

<h1>Claude 防封</h1>

**Gardez Miss Claude dans une cage pour que nous puissions mieux protéger notre temps ensemble**

<img src="assets/claude-cage-character.png" alt="Claude 防封" width="320" />

[![Release](https://img.shields.io/github/v/release/tan396222-prog/Claude-AntiBan?include_prereleases)](https://github.com/tan396222-prog/Claude-AntiBan/releases) [![CI](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml) [![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**[Site Web du projet](https://github.com/tan396222-prog/Claude-AntiBan) · [Français](https://github.com/tan396222-prog/Claude-AntiBan) · [Télécharger l’application](https://github.com/tan396222-prog/Claude-AntiBan/releases) · [Commencer](docs/getting-started.md) · [FAQ](docs/troubleshooting.md) · [Contribuer](CONTRIBUTING.md) · [Guide d’utilisation (chinois)](docs/README.md)**

[简体中文](README.md) · [繁體中文](README.zh-TW.md) · [Русский](README.ru.md) · [العربية](README.ar.md) · [فارسی](README.fa.md) · [한국어](README.ko.md) · [Español](README.es.md) · [မြန်မာဘာသာ](README.my.md) · [Беларуская](README.be.md) · [پښتو](README.ps.md) · [دری](README.prs.md) · [አማርኛ](README.am.md) · [Soomaali](README.so.md) · [ትግርኛ](README.ti.md) · [Français](README.fr.md)

</div>

## Assez d’Anthropic

En tant qu'utilisateurs ordinaires qui souhaitent simplement parler et collaborer avec Claude, nous en avons assez de configurer des instances VPS, des profils d'empreintes digitales de navigateur et des contrôles d'environnement, tout en nous inquiétant des interdictions de compte et des pertes financières. Nous présentons **Claude 防封** — **une application de bureau prête à l'emploi : configurez votre propre proxy réseau et gérez votre environnement isolé, votre fuseau horaire, votre langue et vos contrôles d'environnement en un seul endroit.**

Claude 防封 est une machine virtuelle Linux légère qui combine un terminal Linux, un navigateur et un éditeur de fichiers dans une seule application de bureau. Ses principales caractéristiques comprennent :

- Paramètres réseau isolés de l'hôte ; Cage achemine tout le trafic réseau à l'intérieur de son environnement via son propre proxy réseau
- Paramètres d'empreinte digitale du navigateur isolés de l'hôte, y compris le fuseau horaire et la langue, avec synchronisation automatique avec l'emplacement IP du proxy réseau
- Intégration tout au long du flux de travail Claude : connexion au navigateur, autorisation de compte et processus Claude Code
- Les contrôles de réseau et d'isolement intégrés interrompent l'accès à Internet lorsque les contrôles critiques échouent ou que les preuves sont incomplètes ; les contrôles communautaires et tiers fournissent des informations de référence supplémentaires

**Résultats de la vérification de l'environnement avec Cage :**

| Contrôles du réseau et de l’isolation | Contrôle communautaire du navigateur |
| :---: | :---: |
| <a href="assets/network-checks.png"><img src="assets/network-checks.png" alt="Contrôles du réseau et de l’isolation" width="400" /></a> | <a href="assets/community-browser-check.png"><img src="assets/community-browser-check-cropped.png" alt="Contrôle communautaire du navigateur" width="400" /></a> |

**L'interface principale de Cage :**

![Cage](www/public/workbench-2026-11.jpg)

## Ce que vous pouvez faire avec Cage

- **Travaillez dans une seule fenêtre.** Exécutez Claude Code dans le terminal, examinez les modifications dans l'éditeur et utilisez le navigateur Cage pour prévisualiser les services et vous connecter.
- **Contrôlez ce que vous partagez.** Seuls l'espace de travail par défaut et les dossiers de projet explicitement ouverts sont partagés. L'état de connexion Claude de l'hôte, les profils de navigateur et les clés SSH ne sont pas importés automatiquement.
- **Consultez l'état de votre réseau.** La gestion intégrée du proxy réseau vous permet d'importer des abonnements, de sélectionner des points de terminaison et de vérifier l'adresse IP de sortie. L'environnement reste hors ligne jusqu'à ce que les vérifications critiques soient réussies. Si le proxy réseau n'est pas disponible, il n'est pas possible de recourir à une connexion directe.
- **Continuez la prochaine fois.** Les outils, les fichiers et l'état de connexion persistent sur le disque virtuel. Les tâches en arrière-plan peuvent continuer à s'exécuter lorsque vous fermez la fenêtre.
- **Gérez plusieurs comptes Claude Code.** Attribuez un compte à chaque nouveau terminal Claude et consultez les quotas sur 5 heures et 7 jours. Les terminaux en cours ne changent pas automatiquement de compte.
- **Continuez depuis votre téléphone.** Le client mobile de développement se connecte à l’ordinateur pour les sessions CLI structurées et les approbations d’outils ; le navigateur distant exige une autorisation distincte. Le client mobile doit être compilé depuis les sources et ne prend pas le contrôle de n’importe quel terminal ordinaire.
- **Exécutez Cage dans le cloud.** Connectez-vous par SSH à Cage déployé sur un VPS Linux x86_64 compatible, sans KVM. Les données et la sortie réseau restent sur le serveur.

## Démarrage rapide

Envoyez cette demande à votre agent IA (codex, dsh, workbuddy — probablement pas Claude Code 😁) :

```markdown
Aide-moi à installer https://github.com/tan396222-prog/Claude-AntiBan et configurez le proxy réseau de Cage pour utiliser le point de terminaison xxx dans mon installation Clash locale.
```

Tout ce dont vous avez besoin est un point de terminaison proxy réseau fiable. Ce projet ne fait la promotion d'aucun fournisseur de proxy ; vous pouvez trouver un fournisseur vous-même, par exemple en recherchant le site Web Black Error.

## Téléchargement et installation

Cage prend en charge macOS, Windows et Linux : Apple Silicon avec macOS 14+, Windows 10 22H2 / 11 x64 et Debian 12 / Ubuntu 24.04 x86_64. Windows et Linux sont encore en cours de validation. Consultez les notes de version pour les paquets disponibles, les signatures et les tests réalisés. Intel Mac, Windows ARM64 et Linux ARM64 ne sont pas encore pris en charge. [macOS](docs/getting-started.md) · [Windows](docs/windows.md) · [Linux](docs/linux.md).

1. Télécharger l’application: [macOS / Windows / Linux](https://github.com/tan396222-prog/Claude-AntiBan/releases).
2. macOS: Ouvrez le package d'installation et faites glisser **Cage** dans **Applications**; puis lancez-le depuis Applications. Windows: [Guide d’installation](docs/windows.md#安装并开始工作) · Linux: [Guide d’installation](docs/linux.md#安装并开始工作).
3. Suivez les instructions de l'application pour configurer votre propre proxy réseau, démarrer l'environnement et ouvrir un projet.

Le programme d'installation comprend le workbench et le noyau du proxy réseau. Une utilisation quotidienne ne nécessite pas l'installation de Go, Node.js, Docker ou Clash. Le premier lancement nécessite toujours une connexion Internet pour télécharger l’image Linux et les outils de développement. Cage ne fournit ni service proxy réseau ni compte Claude.

Consultez les [Releases](https://github.com/tan396222-prog/Claude-AntiBan/releases) pour les versions publiques. Le code source, le site Web et les versions sont publics. Reportez-vous aux notes de chaque version pour la signature du programme d'installation, la notarisation Apple et la portée de la validation. L'installation sur une machine propre, la compatibilité sur les Mac physiques exécutant macOS 14, l'autorisation réelle du compte Claude et les demandes de modèle nécessitent toujours une validation complète.

Les mises à jour macOS et Windows passent par le proxy choisi, sans connexion directe de secours. Enregistrez et arrêtez les tâches avant l’installation. macOS exige une signature Developer ID du même éditeur et la vérification Apple ; Windows exige une signature Authenticode de confiance et la même clé publique de l’éditeur. La version préliminaire Windows non signée et Linux se mettent à jour manuellement. [Mise à jour et désinstallation](docs/getting-started.md#升级与卸载).

## Commencez en trois étapes

### 1. Sélectionnez un proxy réseau

Sur la page d'accueil, cliquez sur « Configurer le réseau et le proxy », puis sur « Ajouter une source ». Importez un abonnement YAML Clash/Mihomo ou un fichier YAML local et sélectionnez un point de terminaison sous « Nœuds ». Si aucun proxy réseau n'est disponible, vous pouvez sélectionner explicitement « Télécharger cet abonnement via le réseau hôte » ou importer directement un fichier local. Plus tard, gérez les points de terminaison sous « Paramètres → Réseau et proxy ».

### 2. Démarrez l'environnement

Revenez à l’atelier et cliquez sur « Démarrer l’environnement Cage ». Si la configuration Git de l'hôte nécessite une protection de sécurité, l'application explique d'abord les effets. Appliquez-le en cliquant sur « Activer la protection et continuer ». Les commits et les pulls pour les projets ordinaires ne sont pas affectés. Cage télécharge ensuite l'image, installe les outils et exécute des vérifications du réseau. Consultez la progression sous « Détails de l’opération ». Une fois l’environnement prêt et les vérifications critiques réussies, vous pouvez travailler en ligne. Si une vérification échoue, suivez les instructions pour résoudre le problème et réessayez.

### 3. Ouvrez un projet et exécutez Claude

Cliquez sur « Ouvrir le projet » pour sélectionner un dossier sur votre ordinateur, ouvrez un nouveau terminal et entrez `claude`; puis autorisez votre propre compte dans le navigateur Cage. Vous pouvez également commencer avec l'espace de travail par défaut sans préparer de projet. Pour les étapes complètes, voir [Commencer](docs/getting-started.md).

## Comprendre ces limites avant utilisation

- Les modifications et suppressions dans les projets partagés affectent directement les fichiers sur votre ordinateur. Utilisez le contrôle de version ou les sauvegardes. `.git` et les autres métadonnées sont en lecture seule par défaut ; effectuer les commits Git sur l’ordinateur hôte.
- Tous les projets partagent un environnement Cage, y compris ses outils, ses comptes et son système de fichiers Linux. Les projets ne sont pas isolés les uns des autres.
- L'invité a actuellement accès au TCP IPv4 public via le proxy réseau, le DNS étant envoyé via le même proxy. Les connexions publiques UDP, IPv6, ICMP et initiées par l'invité à l'hôte ou au réseau local sont bloquées. Les outils qui dépendent de ces capacités peuvent ne pas fonctionner.
- Les vérifications du réseau montrent la configuration actuelle et des échantillons de preuves. Ils ne peuvent pas garantir qu’un compte évitera les restrictions de la plateforme et ne remplacent pas un audit complet de sécurité de la virtualisation.

Pour plus de détails, voir [Limites du réseau et des données](docs/getting-started.md#网络与数据边界), [Conception de contrôles de réseau](docs/design/network-checks.md) et [Conception de proxy réseau intégré](docs/design/managed-proxy.md).

## Documentation et contributions

Pour l'index complet, voir [Index des documents](docs/README.md).

| Votre tâche | Commencez ici |
|---|---|
| Installez, configurez un proxy réseau et ouvrez votre premier projet | [Commencer](docs/getting-started.md) |
| Dépanner les téléchargements, le démarrage, la connectivité et la connexion | [FAQ et récupération](docs/troubleshooting.md) |
| Apprenez les commandes du clavier, l'apparence, la persistance des données et le comportement de sortie | [Utilisation quotidienne](docs/getting-started.md#日常使用) |
| Construire à partir des sources ou apporter des améliorations | [Guide de contribution](CONTRIBUTING.md) |
| Comprendre les responsabilités du module et les contraintes du réseau | [Conception d'établi](docs/design/workbench.md) |
| Signaler un problème reproductible | [GitHub Issues](https://github.com/tan396222-prog/Claude-AntiBan/issues) |

## Origines et licences

La cage est dérivée de [agent-vm](https://github.com/sylvinus/agent-vm) ; la ligne de base en amont est `652688aae92c35ce536b9be52262e1cc5ec6e03a`.

La licence principale du projet est [MIT](LICENSE). Les composants groupés conservent leurs licences respectives. Mihomo est distribué sous forme d'exécutable distinct sous GPL-3.0. Voir aussi [NOTICE](NOTICE), [Licences tierces](THIRD_PARTY_LICENSES), [Sources tierces](third_party/SOURCES) et [provenance noVNC](ui/vendor/novnc/README.cage.md). La description du projet en amont est conservée dans le [README en amont](docs/agent-vm-upstream.md).

Cage est un projet communautaire indépendant et n'est pas affilié à Anthropic.

## Références et remerciements

Merci aux projets suivants pour leurs capacités fondamentales et leurs références de conception :

- [agent-vm](https://github.com/sylvinus/agent-vm): la fondation amont de Cage.
- [Lima](https://github.com/lima-vm/lima): Gestion des machines virtuelles Linux.
- [gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock): mise en réseau de machines virtuelles.
- [sshocker](https://github.com/lima-vm/sshocker) et [SFTP](https://github.com/pkg/sftp): partage de fichiers entre la machine virtuelle et l'hôte.
- [noVNC](https://github.com/novnc/noVNC): affichage à distance et interaction dans le navigateur.
- [Mihomo](https://github.com/MetaCubeX/mihomo): le noyau proxy réseau intégré.
- [code-server](https://github.com/coder/code-server): l'éditeur de code basé sur un navigateur.
- [AIO Sandbox](https://github.com/agent-infra/sandbox): références pour l'environnement de travail et le design d'interaction.
- [claude-switcher](https://github.com/YOURSJEYO/claude-switcher) et [Claude-Guardian](https://github.com/DUNHKpcc/Claude-Guardian): références pour les vérifications du réseau et les diagnostics de sécurité.
