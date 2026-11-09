<div align="center">

<h1>Claude 防封</h1>

**Mantenga a la señorita Claude en una jaula para que podamos proteger mejor nuestro tiempo juntos.**

<img src="assets/claude-cage-character.png" alt="Claude 防封" width="320" />

[![Release](https://img.shields.io/github/v/release/tan396222-prog/Claude-AntiBan?include_prereleases)](https://github.com/tan396222-prog/Claude-AntiBan/releases) [![CI](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tan396222-prog/Claude-AntiBan/actions/workflows/ci.yml) [![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**[Sitio web del proyecto](https://github.com/tan396222-prog/Claude-AntiBan) · [Français](https://github.com/tan396222-prog/Claude-AntiBan) · [Descargar aplicación](https://github.com/tan396222-prog/Claude-AntiBan/releases) · [Empezando](docs/getting-started.md) · [Preguntas frecuentes](docs/troubleshooting.md) · [Contribuir](CONTRIBUTING.md) · [Guía de uso (chino)](docs/README.md)**

[简体中文](README.md) · [繁體中文](README.zh-TW.md) · [Русский](README.ru.md) · [العربية](README.ar.md) · [فارسی](README.fa.md) · [한국어](README.ko.md) · [Español](README.es.md) · [မြန်မာဘာသာ](README.my.md) · [Беларуская](README.be.md) · [پښتو](README.ps.md) · [دری](README.prs.md) · [አማርኛ](README.am.md) · [Soomaali](README.so.md) · [ትግርኛ](README.ti.md) · [Français](README.fr.md)

</div>

## Hartazgo con Anthropic

Como usuarios normales que sólo queremos hablar y colaborar con Claude, estamos cansados de configurar instancias de VPS, perfiles de huellas dactilares del navegador y comprobaciones del entorno, mientras nos preocupamos por las prohibiciones de cuentas y las pérdidas financieras. presentamos **Claude 防封** — **una aplicación de escritorio lista para usar: configure su propio proxy de red y administre su entorno aislado, zona horaria, idioma y comprobaciones de entorno en un solo lugar.**

Claude 防封 es una máquina virtual Linux liviana que combina una terminal Linux, un navegador y un editor de archivos en una sola aplicación de escritorio. Sus principales características incluyen:

- Configuraciones de red aisladas del host; Cage enruta todo el tráfico de red dentro de su entorno a través de su propio proxy de red.
- Configuración de huellas digitales del navegador aislada del host, incluida la zona horaria y el idioma, con sincronización automática con la ubicación IP del proxy de red
- Integración en todo el flujo de trabajo de Claude: inicio de sesión en el navegador, autorización de cuenta y proceso de Claude Code
- Las comprobaciones de red y aislamiento integradas pausan el acceso a Internet cuando las comprobaciones críticas fallan o la evidencia está incompleta; Los controles comunitarios y de terceros proporcionan información de referencia adicional.

**Resultados de la verificación ambiental con Cage:**

| Comprobación de red y aislamiento | Comprobación del navegador de la comunidad |
| :---: | :---: |
| <a href="assets/network-checks.png"><img src="assets/network-checks.png" alt="Comprobación de red y aislamiento" width="400" /></a> | <a href="assets/community-browser-check.png"><img src="assets/community-browser-check-cropped.png" alt="Comprobación del navegador de la comunidad" width="400" /></a> |

**Interfaz principal de Cage:**

![Cage](www/public/workbench-2026-11.jpg)

## ¿Qué puedes hacer con la jaula?

- **Trabaja en una sola ventana.** Ejecute Claude Code en la terminal, revise los cambios en el editor y use el navegador Cage para obtener una vista previa de los servicios e iniciar sesión.
- **Controla lo que compartes.** Solo se comparten el espacio de trabajo predeterminado y las carpetas de proyectos abiertas explícitamente. El estado de inicio de sesión de Claude, los perfiles del navegador y las claves SSH del host no se importan automáticamente.
- **Vea el estado de su red.** La administración de proxy de red incorporada le permite importar suscripciones, seleccionar puntos finales y verificar la IP de salida. El entorno permanece fuera de línea hasta que se superan las comprobaciones críticas. Si el proxy de red no está disponible, no se recurre a una conexión directa como alternativa.
- **Continúe la próxima vez.** Las herramientas, los archivos y el estado de inicio de sesión persisten en el disco virtual. Las tareas en segundo plano pueden seguir ejecutándose cuando cierras la ventana.
- **Gestione varias cuentas de Claude Code.** Asigne una cuenta a cada nuevo terminal de Claude y consulte las cuotas de 5 horas y 7 días. Los terminales en ejecución no cambian de cuenta automáticamente.
- **Continúe desde el móvil.** El cliente móvil de desarrollo se conecta al ordenador para usar sesiones CLI estructuradas y aprobar herramientas; el navegador remoto requiere una autorización independiente. Hay que compilar el cliente móvil desde el código fuente y no permite controlar cualquier terminal normal.
- **Ejecute Cage en la nube.** Conéctese por SSH a Cage desplegado en un VPS Linux x86_64 compatible, sin KVM. Los datos y la salida de red están en el servidor.

## Inicio rápido

Envíe esta solicitud a su agente de IA (codex, dsh, workbuddy; probablemente no Claude Code 😁):

```markdown
Ayúdame a instalar https://github.com/tan396222-prog/Claude-AntiBan y configurar el proxy de red de Cage para usar el punto final xxx en mi instalación local de Clash.
```

Todo lo que necesita es un punto final de proxy de red confiable. Este proyecto no promueve ningún proveedor de proxy; Puede encontrar un proveedor usted mismo, por ejemplo buscando en el sitio web de Black Error.

## Descarga e instalación

Cage admite macOS, Windows y Linux: Apple Silicon con macOS 14+, Windows 10 22H2 / 11 x64 y Debian 12 / Ubuntu 24.04 x86_64. Windows y Linux siguen en validación. Consulta las notas de cada versión para conocer los paquetes disponibles, las firmas y las pruebas realizadas. Intel Mac, Windows ARM64 y Linux ARM64 aún no son compatibles. [macOS](docs/getting-started.md) · [Windows](docs/windows.md) · [Linux](docs/linux.md).

1. Descargar aplicación: [macOS / Windows / Linux](https://github.com/tan396222-prog/Claude-AntiBan/releases).
2. macOS: Abra el paquete de instalación y arrastre **Cage** en **Aplicaciones**; luego ejecútelo desde Aplicaciones. Windows: [Guía de instalación](docs/windows.md#安装并开始工作) · Linux: [Guía de instalación](docs/linux.md#安装并开始工作).
3. Siga las instrucciones de la aplicación para configurar su propio proxy de red, iniciar el entorno y abrir un proyecto.

El instalador incluye el banco de trabajo y el núcleo del proxy de red. El uso diario no requiere instalar Go, Node.js, Docker o Clash. El primer lanzamiento aún requiere una conexión a Internet para descargar la imagen de Linux y las herramientas de desarrollo. Cage no proporciona un servicio de proxy de red ni una cuenta Claude.

Consulte las [Releases](https://github.com/tan396222-prog/Claude-AntiBan/releases) para conocer las versiones públicas. El código fuente, el sitio web y los lanzamientos son públicos. Consulte las notas de cada versión para conocer la firma del instalador, la certificación notarial de Apple y el alcance de la validación. La instalación en máquina limpia, la compatibilidad con Mac físicos que ejecutan macOS 14, la autorización de cuentas reales de Claude y las solicitudes de modelos aún requieren una validación completa.

Las actualizaciones de macOS y Windows usan el proxy seleccionado, sin recurrir a una conexión directa. Guarda y detén las tareas antes de instalar. macOS requiere Developer ID del mismo editor y verificación de Apple; Windows requiere una firma Authenticode de confianza y la misma clave pública del editor. La versión preliminar de Windows sin firma y los paquetes Linux se actualizan manualmente. [Actualizar y desinstalar](docs/getting-started.md#升级与卸载).

## Comience en tres pasos

### 1. Seleccione un proxy de red

En la página de inicio, haga clic en "Configurar red y proxy" y luego en "Agregar fuente". Importe una suscripción YAML de Clash/Mihomo o un archivo YAML local y seleccione un punto final en "Nodos". Si no hay ningún proxy de red disponible, puede seleccionar explícitamente "Descargar esta suscripción a través de la red de host" o importar un archivo local directamente. Luego, administre los puntos finales en 'Configuración → Red y proxy'.

### 2. Iniciar el entorno

Regrese a la mesa de trabajo y haga clic en "Iniciar entorno Cage". Si la configuración de Git del host necesita protección de seguridad, la aplicación primero explica los efectos. Aplíquelo haciendo clic en "Habilitar protección y continuar". Los compromisos y los pulls para proyectos ordinarios no se ven afectados. Luego, Cage descarga la imagen, instala herramientas y ejecuta comprobaciones de red. Vea el progreso en "Detalles de la operación". Una vez que el entorno esté listo y se pasen las comprobaciones críticas, podrá trabajar en línea. Si falla la verificación, siga las instrucciones para solucionar el problema y vuelva a intentarlo.

### 3. Abra un proyecto y ejecute Claude.

Haga clic en 'Abrir proyecto' para seleccionar una carpeta en su equipo, abra una nueva terminal e ingrese `claude`; luego autorice su propia cuenta en el navegador Cage. También puedes comenzar con el espacio de trabajo predeterminado sin preparar un proyecto. Para conocer los pasos completos, consulte [Empezando](docs/getting-started.md).

## Comprenda estos límites antes de usarlo

- Las ediciones y eliminaciones en proyectos compartidos afectan directamente a los archivos de su equipo. Utilice control de versiones o copias de seguridad. `.git` y otros metadatos son de sólo lectura de forma predeterminada; realizar confirmaciones de Git en equipo.
- Todos los proyectos comparten un entorno Cage, incluidas sus herramientas, cuentas y sistema de archivos Linux. Los proyectos no están aislados unos de otros.
- Actualmente, el invitado tiene acceso al TCP IPv4 público a través del proxy de red, y el DNS se envía a través del mismo proxy. Se bloquean las conexiones públicas UDP, IPv6, ICMP y iniciadas por invitados al host o a la red local. Es posible que las herramientas que dependen de estas capacidades no funcionen.
- Las comprobaciones de red muestran la configuración actual y pruebas de muestra. No pueden garantizar que una cuenta evite las restricciones de la plataforma y no reemplazan una auditoría completa de seguridad de virtualización.

Para más detalles, consulte [Límites de red y datos](docs/getting-started.md#网络与数据边界), [Diseño de controles de red.](docs/design/network-checks.md) y [Diseño de proxy de red integrado](docs/design/managed-proxy.md).

## Documentación y aportaciones

Para consultar el índice completo, consulte [Índice de documentación](docs/README.md).

| tu tarea | Empieza aquí |
|---|---|
| Instale, configure un proxy de red y abra su primer proyecto | [Empezando](docs/getting-started.md) |
| Solucionar problemas de descargas, inicio, conectividad e inicio de sesión | [Preguntas frecuentes y recuperación](docs/troubleshooting.md) |
| Aprenda los controles del teclado, la apariencia, la persistencia de datos y el comportamiento de salida | [Uso diario](docs/getting-started.md#日常使用) |
| Construya desde la fuente o contribuya con mejoras | [Guía de contribución](CONTRIBUTING.md) |
| Comprender las responsabilidades del módulo y las limitaciones de la red. | [Diseño de banco de trabajo](docs/design/workbench.md) |
| Informar un problema reproducible | [GitHub Issues](https://github.com/tan396222-prog/Claude-AntiBan/issues) |

## Orígenes y licencias

La jaula se deriva de [agent-vm](https://github.com/sylvinus/agent-vm) ; la línea de base aguas arriba es `652688aae92c35ce536b9be52262e1cc5ec6e03a`.

La licencia principal del proyecto es [MIT](LICENSE). Los componentes incluidos conservan sus respectivas licencias. Mihomo se distribuye como un ejecutable independiente bajo GPL-3.0. Ver también [NOTICE](NOTICE), [Licencias de terceros](THIRD_PARTY_LICENSES), [Fuentes de terceros](third_party/SOURCES) y [procedencia noVNC](ui/vendor/novnc/README.cage.md). La descripción del proyecto upstream se conserva en el [LÉAME ascendente](docs/agent-vm-upstream.md).

Cage es un proyecto comunitario independiente y no está afiliado a Anthropic.

## Referencias y agradecimientos

Gracias a los siguientes proyectos por capacidades fundamentales y referencias de diseño:

- [agent-vm](https://github.com/sylvinus/agent-vm): la base aguas arriba de Cage.
- [Lima](https://github.com/lima-vm/lima): Gestión de máquinas virtuales Linux.
- [gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock): redes de máquinas virtuales.
- [sshocker](https://github.com/lima-vm/sshocker) y [SFTP](https://github.com/pkg/sftp): intercambio de archivos entre la máquina virtual y el host.
- [noVNC](https://github.com/novnc/noVNC): visualización remota e interacción en el navegador.
- [Mihomo](https://github.com/MetaCubeX/mihomo): el núcleo de proxy de red integrado.
- [code-server](https://github.com/coder/code-server): el editor de código basado en navegador.
- [AIO Sandbox](https://github.com/agent-infra/sandbox): referencias para el ambiente de trabajo y el diseño de interacción.
- [claude-switcher](https://github.com/YOURSJEYO/claude-switcher) y [Claude-Guardian](https://github.com/DUNHKpcc/Claude-Guardian): referencias para comprobaciones de red y diagnósticos de seguridad.
